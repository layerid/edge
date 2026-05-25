# Race-safe consume

`Consume(req_id)` returns the score for `req_id` and debits the customer's quota **exactly once**. Retries with the same `req_id` get the same response — they don't error, they don't double-debit.

> **Source of truth (legacy)**: `../../fingerprints/src/modules/fingerprints/application/use_cases.py:158-232` (`ConsumeRequestUseCase`) plus `../../fingerprints/src/modules/fingerprints/infrastructure/mongodb_repository.py` (`consume()` method). v2 ports the **guarantees** (idempotent, one-quota-debit) but **changes the mechanism** — Postgres atomic update is enough, the Redis distributed-lock the legacy used is dropped.

## Design: idempotency-key semantics (Stripe-style)

The `req_id` is the idempotency key. The contract is the same as Stripe's `Idempotency-Key` header:

- **First call** with a given `req_id`: does the work, returns the result, side effects (quota debit) happen exactly once.
- **Retry** with the same `req_id`: returns the **same response** from the first call. No double-debit. No error.

This is the standard pattern customers expect. Erroring on retry with `ALREADY_CONSUMED` (as the legacy did) is a UX bug — a customer whose first request timed out at the LB has done nothing wrong and should be allowed to retry.

## Why this matters

A customer's login form submits with `req_id`. Their backend hits `/v1/request/{req_id}/consume`. The response includes the score; the customer's policy engine acts on it; the user is allowed / stepped up / denied.

If `consume` could fire twice for the same `req_id`:
- Customer billing would double-debit
- A bot could send the same `req_id` to two backends and bypass quota
- The "use a fresh fingerprint per login attempt" guarantee leaks

The legacy got this right with Mongo upsert + Redis lock + idempotency flag. v2 ports the structure and tightens the storage.

## Contract

```proto
rpc Consume (ConsumeRequest) returns (ScoreResponse);

message ConsumeRequest {
  string req_id = 1;
  string api_key = 2;
}

message ScoreResponse {
  // … fields …
  bool replayed = 99;  // true if this is a retry returning the cached response
}
```

Behaviour:

- **First call** with a given `req_id` for a given tenant: computes the verdict (or returns the precomputed one from the init/warmup flow), marks the lookup row's `consumed_at`, debits the tenant's quota, publishes domain event, returns `ScoreResponse` with `replayed=false`.
- **Retry** with the same `req_id` (any number of times): returns the cached `ScoreResponse` from the first consume with `replayed=true`. No quota debit. No domain event re-published. Allowed indefinitely while the lookup row still exists.
- **Cross-tenant**: a `req_id` belongs to the tenant that issued it. Another tenant's call returns gRPC `NOT_FOUND`.
- **TTL**: lookup row held for 35 days. After that, the `req_id` simply doesn't exist (data rotated out via partition drop).

## Implementation — one atomic SQL statement

Postgres alone provides the guarantees. No Redis lock. The schema:

```sql
CREATE TABLE lookups (
    req_id         UUID NOT NULL,
    tenant_id      BIGINT NOT NULL,
    ip             INET,
    ja3            VARCHAR(64),
    signals_json   JSONB,
    score          NUMERIC(4,3),
    verdict        VARCHAR(32),
    created_at     TIMESTAMPTZ NOT NULL,
    consumed_at    TIMESTAMPTZ,
    consumed_by    VARCHAR(255),
    PRIMARY KEY (req_id, created_at)         -- partitioned by created_at
) PARTITION BY RANGE (created_at);
CREATE INDEX lookups_tenant_created ON lookups (tenant_id, created_at);
CREATE INDEX lookups_unconsumed ON lookups (tenant_id) WHERE consumed_at IS NULL;
```

The consume operation is a single CTE that handles both first-time and retry in one statement:

```sql
WITH attempt AS (
    UPDATE lookups
       SET consumed_at = NOW(),
           consumed_by = $3
     WHERE req_id = $1
       AND tenant_id = $2
       AND consumed_at IS NULL
     RETURNING signals_json, score, verdict, consumed_at AS now_consumed_at,
               false AS replayed
)
SELECT * FROM attempt
UNION ALL
SELECT signals_json, score, verdict, consumed_at AS now_consumed_at,
       true AS replayed
  FROM lookups
 WHERE req_id = $1
   AND tenant_id = $2
   AND consumed_at IS NOT NULL
   AND NOT EXISTS (SELECT 1 FROM attempt)
LIMIT 1;
```

Three outcomes from one query:
- **First-time consume**: UPDATE matches, returns the row with `replayed=false`.
- **Retry**: UPDATE doesn't match (consumed_at already set), SELECT returns the cached row with `replayed=true`.
- **Not found**: zero rows. Edge returns gRPC `NOT_FOUND`.

The `WHERE consumed_at IS NULL` clause on UPDATE is what makes this atomic. Two concurrent UPDATEs at the row lock; exactly one wins. The loser falls through to the SELECT and returns the same cached response the winner already wrote.

### Why no Redis lock

The legacy used a Redis distributed lock to prevent duplicate **scoring work** under concurrent consume retries. In v2 that's a non-issue:

1. **Score is precomputed by warmup.** The init flow (when the browser SDK loads) fires `Lookup(ip)` to intel-service and the result lands in the lookup row before `consume` is ever called. By the time consume runs, the score is already in `lookups.signals_json + lookups.score`. The atomic UPDATE just returns what's already there. No "expensive work" to lock around.
2. **Concurrent retries with same `req_id` are rare in practice.** Customers retry sequentially on network failure, not in parallel.
3. **The Postgres row lock already serialises** the rare concurrent case. The "loser" of the race gets the same cached result via the SELECT branch.
4. **One less moving part.** No Redis lock means no lock TTL drama, no Lua scripts, no "what happens if Redis is down during consume."

If a customer somehow generates 1000 concurrent consumes on the same `req_id`, all 1000 succeed with the same response. One wins the UPDATE; 999 fall through to the SELECT. Postgres handles it.

### Quota debit (in the same transaction)

The CTE above only marks `consumed_at`. Quota debit happens in the same transaction:

```sql
INSERT INTO quota_debits (tenant_id, req_id, debited_at)
VALUES ($1, $2, NOW())
ON CONFLICT (tenant_id, req_id) DO NOTHING;
```

`ON CONFLICT DO NOTHING` makes this idempotent at the row level. Retries with the same `req_id` simply no-op (the row already exists). The quota counter is rolled up nightly from this table.

Crucially: this INSERT only runs if `replayed=false` (i.e. it's a real first-time consume). Retries skip it entirely — though the constraint would catch it anyway.

### Audit log (separate, append-only)

Every consume attempt (first-time or retry) writes to the audit log:

```sql
CREATE TABLE consume_audit (
    id            BIGSERIAL PRIMARY KEY,
    tenant_id     BIGINT NOT NULL,
    req_id        UUID NOT NULL,
    consumed_at   TIMESTAMPTZ NOT NULL,
    consumed_by   VARCHAR(255),
    replayed      BOOLEAN NOT NULL,
    edge_region   VARCHAR(16)
);
CREATE INDEX consume_audit_tenant_time ON consume_audit (tenant_id, consumed_at);
```

This is for forensics, not idempotency. If a customer's traffic shows the same `req_id` being consumed 47 times, the audit log shows you 1 first-time consume + 46 retries, with timestamps and source IPs. Useful for dispute resolution and anomaly detection.

## Why not Mongo any more

The legacy used Mongo `find_one_and_update` with `upsert=true` — works fine. v2 picks Postgres because:

1. **Dashboard queries are heavy joins / aggregations.** Daily/hourly rollups, signal breakdowns, geo distributions. Mongo aggregation pipeline is slower and less ergonomic than `GROUP BY` / window functions.
2. **Schema discipline.** `signals_json` is JSONB — gets the flexibility of a document store where it matters (signal values evolve over time) without losing relational integrity for tenant / billing / API key tables.
3. **Postgres is already in `intel-service`.** One database engine across the workspace beats two. The lookups table can even live in the same Postgres instance as intel-service (different schema), reusing connection pools, backups, replication.
4. **Read replicas.** Postgres streaming replication to per-region read replicas (see `multi-region.md`) is mature. Mongo replica sets work but the read-from-secondary story is messier.

## When idempotency breaks

Four edge cases worth knowing:

1. **TTL expiry mid-call.** Lookup created at t0, customer waits 35 days, consume at t0+35d. The lookup is purged before consume lands. Customer sees `NOT_FOUND`. **Expected behaviour** — consume only meaningful within fingerprint validity window.

2. **Postgres failover during consume.** UPDATE lands on primary, primary crashes before WAL replicates. Replica promotes, consume retried — the new primary doesn't see the original consumed_at. Double-consume risk. **Mitigation**: synchronous replication on the lookups table specifically. Cost: ~2ms p99 added per consume. Worth it.

3. **Customer retries on network blip.** Their request to `/consume` timed out at the load balancer; they retry with the same `req_id`. The retry succeeds with `replayed=true` and the same response body. **No double-debit**, no error. This is the entire reason for the idempotency-key design.

4. **Customer wants to "preview" before consume.** Use `Score()` (the non-consuming sibling RPC). Same response shape, no quota debit, no `consumed_at` mark. `Consume()` is for the irreversible "I'm about to act on this score" path.

## Testing the guarantees

Three property tests in `tests/consume/`:

1. **N concurrent calls to consume(req_id) → exactly one gets `replayed=false`, all return the same body.** Run with N=100, repeat 1000 times. Failure of this test is the only acceptable reason to delay shipping.
2. **Consume → Consume returns identical body with `replayed=true` the second time.** Trivial but explicit.
3. **Score → Consume → Score**. The first Score returns the data with no quota debit. Consume returns the data and debits quota. The second Score works (it's read-only) and shows `consumed_at != null` for diagnostic purposes.

Run these on CI on every PR touching `internal/consume/`.

## Why this is better than the legacy

The legacy returned `ALREADY_CONSUMED` errors on retry, forcing customers to handle "did my call succeed or not?" ambiguity in their own code. Most customers handled it poorly — either retrying anyway (and seeing the error and giving up) or never retrying (and losing the score). 

The idempotency-key design eliminates the ambiguity: **the customer can always retry safely.** First call or fifth, they get the same answer, quota is debited once. This is the Stripe pattern, well-understood by anyone integrating B2B APIs.

The cost: one extra column in `lookups` (`replayed` is computed, not stored), and the CTE is slightly more complex than the single UPDATE. Worth it.
