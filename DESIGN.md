# DESIGN.md — layerid-edge

The design + migration plan, captured here so the next session can resume without re-deriving it.

## Three-layer architecture

```
┌──────────────────────────────────────────────────────────────────────────┐
│ intel-service/        — DATA PLANE (LayerID-only, closed source)         │
│ ────────────────                                                         │
│ • observations about IPs / JA3 / TCP from sybil peers, probes, DHT       │
│ • Lookup(ip, ja3) → features (JA3 breadth, residential pool, IoT class…) │
│ • the asset that justifies the SaaS — what we know that others don't     │
└──────────────────────────────────────────────────────────────────────────┘
                                       ▲
                                       │ gRPC (intel features)
                                       │
┌──────────────────────────────────────────────────────────────────────────┐
│ layerid-edge/         — DECISION PLANE (open source, Apache 2.0)         │
│ ────────────────                                                         │
│ • Score(device_signals, ip, ja3) → { score, verdict, signals }           │
│ • Fetches intel features from intel-service (with local LRU cache)       │
│ • Joins intel features with device signals from the JS SDK               │
│ • Applies the scoring model (default weights OR customer overrides)      │
│ • Race-safe one-time `consume(req_id)` semantics                         │
│ • Metrics (Prometheus), shadow-mode flag, custom-weight slots            │
│                                                                          │
│ Customer runs this in their VPC. We run this in our SaaS. Same binary.   │
└──────────────────────────────────────────────────────────────────────────┘
                                       ▲
                                       │ gRPC (or HTTP)
                                       │
┌──────────────────────────────────────────────────────────────────────────┐
│ fingerprints-migration/backend/  — SAAS GLUE (LayerID-only, closed)      │
│ ────────────────────────                                                 │
│ • Customer auth (OAuth via layerid-id)                                   │
│ • API key management, billing, rate limiting                             │
│ • Dashboard glue (the customer-facing console)                           │
│ • DELEGATES every Score call to its embedded layerid-edge sidecar        │
│ • Adds nothing scoring-wise — pure BFF                                   │
└──────────────────────────────────────────────────────────────────────────┘
```

### Why this split

- **intel-service = data.** Hard to replicate (we observe a botnet from the inside). Stays proprietary; this is the moat.
- **layerid-edge = code.** Easy to replicate if anyone wanted to. Open-sourcing it is a feature — auditable scoring, customer-portable. We get a community contributing.
- **fingerprints-migration/backend = SaaS plumbing.** Customer-facing surface only. Has no business doing scoring itself.

Dogfooding falls out for free: what we ship to customers is what we run ourselves. Bugs in the edge are bugs we hit first. New signals/features ship to OSS and our SaaS simultaneously.

## Where scoring lives today

`fingerprints/` (legacy monolith) has all the scoring + decision logic. The JS SDK collection, browser signal extraction, score aggregation, replay-safe `consume` endpoint, customer API key management. Live in production.

`fingerprints-migration/backend/` only has the **auth/BFF migration** — OAuth via layerid-id, session management, the new Nuxt frontend's API surface. **The scoring engine was never migrated.** That was always phase 2, deliberately punted while the auth work landed.

So the scoring logic is still in legacy, waiting for a home. `layerid-edge` is that home.

## Migration plan — five phases

### Phase 0 — Decide & document (done in this commit)

- Architecture written into this `DESIGN.md`
- Workspace `CLAUDE.md` already updated with the signals-not-verdicts framing
- `fingerprints-migration/DECISIONS.md` should be updated next session with: "Scoring lives in `layerid-edge/`, not in this repo and not in `intel-service`."

### Phase 1 — Scaffold `layerid-edge` (this commit)

- Directory tree exists
- `proto/edge.proto` stub defines the Score / Consume / Health RPCs
- README + this DESIGN.md committed
- No Go code yet

### Phase 2 — Port scoring from legacy `fingerprints/` into `layerid-edge` (1-2 weeks)

The scoring model from `../fingerprints/` (weights, signal-combination logic, decision thresholds) moves into `internal/score/` in Go. **Port faithfully first**, refactor later — don't try to be clever during the move.

Race-safe consume semantics (one-time use of `req_id` with replay block) moves into `internal/consume/`. Backing store is pluggable — Redis for SaaS, in-memory for single-instance self-host.

The browser JS SDK (`../layerid-fingerprint-js/`) stays put — it just changes endpoint from legacy `fingerprints/` to whatever wraps `layerid-edge`.

**Tests are the canary.** Every test from `fingerprints/` against the scoring/consume path must pass against `layerid-edge`. Anything that doesn't port is a scoring behaviour change you should be deliberate about.

Why Go: small binary, easy distribution, the language of the cloud-native ecosystem (Helm consumers expect Go). Single binary, 30-50MB image.

### Phase 3 — Wire `fingerprints-migration/backend` to consume embedded edge (3-5 days)

`backend` becomes a thin BFF:

1. Customer hits `POST /api/score` (BFF)
2. BFF authenticates (OAuth session OR API key)
3. BFF calls localhost `layerid-edge.Score()` via gRPC
4. BFF returns the score response to the customer

Deploy as a single pod with two containers (`backend` + `layerid-edge`) or as a sidecar in K8s. Either way, cross-process call is localhost gRPC.

`fingerprints-migration/backend` has **zero scoring logic of its own**. Just BFFs.

### Phase 4 — Retire legacy `fingerprints/` (1 week shadow + cutover)

1. Run `fingerprints-migration/backend + layerid-edge` alongside legacy `fingerprints/` for a week
2. Mirror customer requests to both, log score deltas
3. Investigate any meaningful drift (you'll find a bug or two — port bugs are real)
4. Cut DNS over, archive `fingerprints/`

### Phase 5 — Ship customer self-host (1-2 weeks after stability)

- Publish `layerid-edge` as a Helm chart in `oci://ghcr.io/layerid/charts/edge`
- Single binary release on GitHub Releases for non-K8s deploys
- Versioned to track `intel-service/proto/honeypot.proto` API version
- Three operating modes:
  - **Cached** (default): local LRU + Redis warm cache, fetches misses upstream, refreshes async
  - **Pure-proxy**: every Lookup hits upstream — for customers who want zero cache complexity
  - **Offline** (enterprise): nightly intel snapshot pull, no per-request upstream call

Docs to write:
- Self-host quickstart (single binary, point at api.layerid.io for upstream intel)
- K8s deployment guide (Helm chart, mTLS to LayerID core)
- Shadow-mode evaluation guide
- Signal reference (every feature the API returns, what it means, calibration tips)

## Proto contract

See [`proto/edge.proto`](./proto/edge.proto). Summary:

```proto
service Edge {
  rpc Score   (ScoreRequest)   returns (ScoreResponse);
  rpc Consume (ConsumeRequest) returns (ScoreResponse);  // race-safe one-time
  rpc Health  (HealthRequest)  returns (HealthResponse);
}
```

The proto is the public contract — additive changes only, `reserved` for removals, long deprecation windows for renames. Same discipline as `intel-service/proto/honeypot.proto`.

## What lands in this repo, what stays out

**Lands here:**
- The scoring model
- The consume / replay-block semantics
- Local intel cache
- Shadow-mode plumbing
- Custom-weight runtime
- Prometheus metrics
- Helm chart + Dockerfile + GoReleaser config

**Stays out:**
- Intel data itself (lives in `../intel-service/`)
- Customer auth, billing, dashboard (lives in `../fingerprints-migration/backend/`)
- The browser JS SDK (lives in `../layerid-fingerprint-js/`)
- Any vendor reverse-engineering (lives in `../proxy_sdks/`)

## Detailed design docs

Subsystem-level design — read these in order when resuming:

| Doc | What |
|---|---|
| [`docs/scoring-model.md`](./docs/scoring-model.md) | The 14-signal weighted model ported from legacy. Weights, thresholds, what's preserved, what's improved (shadow-mode, per-customer overrides, outcomes calibration). |
| [`docs/consume.md`](./docs/consume.md) | Race-safe one-time consume — Postgres atomic update + Redis lock + audit log. Five edge cases worth knowing. |
| [`docs/multi-region.md`](./docs/multi-region.md) | 7-region SaaS topology, per-region Postgres replicas + Redis cache, GeoDNS routing, `req_id` carries region prefix. |
| [`docs/auth.md`](./docs/auth.md) | JWT API keys for both downstream (customer → edge) and upstream (edge → intel-service). JWKS, key rotation, revocation. |

Plus the BFF design doc that pairs with this:

| Doc | What |
|---|---|
| [`../fingerprints-migration/backend/DESIGN.md`](../fingerprints-migration/backend/DESIGN.md) | The customer-facing BFF — REST API, dashboard data layer, key management, Postgres schema, OAuth integration with `layerid-id`. |

## What's enterprise-only vs open-to-everyone

Updated from the original `enterprise` framing. Most capabilities are tier-portable; only the human-intensive bits stay gated:

| Capability | Free | Standard | Pro | Enterprise |
|---|---|---|---|---|
| Full signal set in Lookup | ✓ | ✓ | ✓ | ✓ |
| Self-hosted edge (OSS) | ✓ | ✓ | ✓ | ✓ + support contract |
| Shadow-mode evaluation | ✓ | ✓ | ✓ | ✓ |
| Custom scoring weights | ✓ | ✓ | ✓ | ✓ |
| Threat-dashboard (own data) | ✓ | ✓ | ✓ | ✓ |
| Region pinning | — | ✓ | ✓ | ✓ |
| Outcomes API + calibration | ✓ | ✓ | ✓ | ✓ |
| Pre-disclosure feed | — | — | — | ✓ |
| Bring-your-own-honeypot | — | — | — | ✓ |
| Direct researcher access | community | email | named TAM | quarterly hunts |
| SLA + status credits | best-effort | 99.9% | 99.95% | 99.99% |

The enterprise edge isn't "more features" — it's **human time + pre-disclosure exclusivity + custom integration**.

## Open questions for the next session

1. **License**: confirmed Apache 2.0? Or MIT? Apache 2.0 is the safer default for an enterprise-friendly engine (explicit patent grant).
2. **Should `intel-service/proto/honeypot.proto` move to a shared `../protocol/` package**, or stay in `intel-service/` with edge importing it via Go module path? The workspace already has a `protocol/` repo — this might be its first real consumer.
3. **`internal/score/` weight format** — embedded YAML config (with customer override layer in DB)?
4. **Postgres tooling**: pgroll vs Alembic for migrations? pgroll's zero-downtime story is better for the production cutover.
5. **Stripe vs Paddle vs self-billing** for the billing meter rollup.

When work resumes here, start with phase 2 (port scoring) — that's the largest single step and unblocks everything after. The docs above tell you exactly what to port and what to improve.
