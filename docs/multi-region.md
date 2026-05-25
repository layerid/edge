# Multi-region SaaS topology

Edge runs in 7 regions at launch. Customer DNS picks the nearest. Self-host is the same binary in the customer's VPC.

## Regions

| Region | POPs | Why |
|---|---|---|
| `us-east-1` (Virginia) | 2 | Default for North American customers; lowest latency to the eastern US population centre |
| `us-west-2` (Oregon) | 2 | West-coast customers + redundant NA region |
| `eu-west-1` (Ireland) | 2 | English-speaking EU customers, default European region |
| `eu-central-1` (Frankfurt) | 2 | **GDPR-aligned**, hard residency boundary for German / continental EU customers |
| `ap-southeast-1` (Singapore) | 2 | South-east Asia + Australia hub |
| `ap-northeast-1` (Tokyo) | 2 | North-east Asia, lowest latency to Japan / Korea |
| `sa-east-1` (São Paulo) | 1 | LATAM coverage, single POP at launch |

Phase 2: `ap-south-1` (Mumbai) when India customers materialise. `me-south-1` (Bahrain) for MENA. `af-south-1` (Cape Town) for Africa.

## Per-region topology

```
   ┌─ Region: eu-central-1 (Frankfurt) ─────────────────────────────────┐
   │                                                                    │
   │   GeoDNS / regional LB                                             │
   │           │                                                        │
   │           ▼                                                        │
   │   ┌──────────────────────┐                                         │
   │   │   layerid-edge       │     local hot cache                     │
   │   │   2+ replicas        │ ◀──▶  ┌──────────────┐                  │
   │   │   gRPC + REST        │       │ Redis cluster│ TTL 5min          │
   │   └─────────┬────────────┘       └──────────────┘                  │
   │             │ on cache miss                                        │
   │             ▼                                                      │
   │   ┌──────────────────────┐                                         │
   │   │ Postgres read replica│ ◀── streaming repl ───┐                 │
   │   │ • lookups            │                       │                 │
   │   │ • intel features     │                       │                 │
   │   │ • tenant scoring cfg │                       │                 │
   │   └──────────────────────┘                       │                 │
   │                                                  │                 │
   └──────────────────────────────────────────────────┼─────────────────┘
                                                      │
                                          INTEL-SERVICE CORE (single region)
                                          │ • proxy → collector            │
                                          │ • probe-server → probe-api     │
                                          │ • dht-crawler → collector      │
                                          │ • writes flow OUT to replicas  │
```

What lives in each region:
- **Edge replicas** — `layerid-edge` binary, stateless, behind a regional LB
- **Redis cluster** — local intel-feature hot cache (5-minute TTL by default)
- **Postgres read replica** — streamed from the intel-service core
- Lookup writes also go to the regional Postgres (which then replicates back to core for centralised analytics)

What stays central:
- **intel-service** ingest pipeline (proxy, probe-server, probe-api, dht-crawler, collector) — single primary, no benefit to regionalising data observation
- **Cross-region analytics** (dashboard rollups, customer billing) — read from the warehouse, not the OLTP path

## Routing

### DNS-based (stage 1, launch)

`api.layerid.io` resolves via GeoDNS:
- Client IP → nearest region via Maxmind GeoLite2-Country
- Cloudflare or Route 53 health-checks each region; unhealthy regions drop out of rotation
- 60-second TTL — failover takes ~1 minute

Per-region pinning for customers who need it:
- `us.api.layerid.io` → us-east-1 + us-west-2 only
- `eu.api.layerid.io` → eu-west-1 + eu-central-1 only
- `de.api.layerid.io` → eu-central-1 only (hard German residency)
- `apac.api.layerid.io` → ap-southeast-1 + ap-northeast-1

Customer picks in the dashboard. Dashboard generates them a CNAME they can use.

### Anycast (stage 2)

`collect.layerid.io` (the SDK loader endpoint) moves to anycast first because it's high-volume and stateless. The API itself stays GeoDNS until cost-effective anycast IPs are worth the spend.

## Data residency guarantees

Three tiers:

1. **Default**: customer's data routes to whichever region GeoDNS picks. Best-effort residency — most US customers will land in us-east; most EU customers in eu-west.

2. **Regional pinning** (Standard tier and above): customer pins to a region. Their lookups never leave that region's Postgres replica + Redis. Dashboard queries route to that region's read replica. **The intel features they consume come from there too** — the regional replica has the full intel snapshot.

3. **Self-host** (Enterprise): customer's edge runs in their own VPC. Lookups never leave the customer's perimeter. Only **upstream intel refresh** crosses the boundary, and that's a one-way snapshot pull, not per-request.

The dashboard residency banner shows which tier they're on:
- `"Regional: eu-central-1 (Frankfurt)"`
- `"Self-hosted at edge-prod.eu.<customer>.internal"`

## Region selection on Score / Consume

The edge that handles a `Score()` call is the one DNS picked. The corresponding `Consume(req_id)` call **must hit the same region** because the lookup row exists there. Options:

1. **Same DNS picks same region** (95% of the time, since the customer's backend doesn't move). Naturally consistent.
2. **req_id encodes region** — `req_id` is `<region>-<uuid>` (e.g. `eu1-7f3a8b2c…`). Consume RPC inspects the prefix and proxies to the correct region if the current region doesn't have the row. ~1ms extra hop, fail-safe.
3. **Cross-region read replicas** — lookups table replicates across regions with eventual consistency. Acceptable if the consume happens >10s after the score (typical login flow is seconds; replication lag is ms).

**Decision: option 2.** `req_id` carries its region. Cheap, deterministic, no replication assumptions.

```go
type ReqID struct {
    Region string // "us1", "us2", "eu1", "eu2", "apac1", "apac2", "sa1"
    UUID   string
}

// "us1-7f3a8b2c-..." 
```

## Failure modes

| Failure | Customer impact | Mitigation |
|---|---|---|
| One POP in a region dies | None — LB removes it from rotation | Health checks 5s, drop instance |
| Whole region dies | GeoDNS drops the region, customers route to next-nearest | Failover ~60s; customer's score quality unchanged |
| Intel-service core dies | Edges fall back to local cache (5min TTL) | Score quality degrades over time as cache stales; recovers when core's back |
| Postgres core primary fails | Synchronous replica promotes; writes pause ~10s | RPO = 0 for consume audit trail (sync replication on that table) |
| Customer's region is geographically far (e.g. New Zealand customer hitting Tokyo) | Higher latency (~200ms instead of 50ms) | They can ask for regional pinning; eventually we add a Sydney POP |
| Network partition between region and core | Region operates on cached intel; cache misses return `verdict: "insufficient_data"` | Customer can decide policy — most fall through to "allow with caution" |

## Observability per region

Each region exports:
- Prometheus metrics on `:9090` (RPS, latency histograms, cache hit ratio, upstream errors)
- OpenTelemetry traces for the Score/Consume request path
- Logs to a regional ELK / Loki instance
- Health endpoint that aggregates: `upstream_ok` (can reach intel-service core), `cache_size`, `postgres_replica_lag`

Cross-region rollup dashboard for ops: 7 region tiles each showing health + traffic + latency, with one click to drill into a single region's detail.

## What this costs (rough)

7 regions × 2 POPs × (1 edge + 1 cache pod) = 14 edge pods + 7 Redis clusters + 7 Postgres replicas.

Stage 1 on Hetzner CCX33 nodes (~€60/mo each, 8-core 32GB): each region = 1 node minimum, ~€420/mo for the multi-region SaaS. Doubles to ~€840 for the second-POP-per-region redundancy. Postgres replica streaming adds bandwidth cost; estimate €100/mo at launch traffic.

Total infrastructure cost at launch: **~€1,000/mo for 7-region geo-redundant deployment**. Scales linearly with traffic; doubles roughly every 10× request volume.

This is reasonable. The expensive things will be (a) the offensive honeypot infrastructure (separate, single-region), (b) per-region database storage as the lookup history grows, (c) Cloudflare egress as the SDK gets popular.

## Migration from single-region (current) to multi-region (target)

1. **Phase 1**: stand up `us-east-1` only with the new edge. All traffic to it. Customers don't notice anything.
2. **Phase 2**: add `eu-central-1` and `eu-west-1`. Enable GeoDNS, EU traffic starts routing there. US customers unaffected.
3. **Phase 3**: add APAC + SA regions. Open per-region pinning in the dashboard. Customers with residency needs migrate.
4. **Phase 4**: add a second POP per existing region for in-region redundancy.

The migration is invisible to customers — `api.layerid.io` is the only DNS they see, and it just gets faster.
