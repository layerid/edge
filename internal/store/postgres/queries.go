// Package postgres holds the production Postgres Repository implementation.
//
// The SQL queries below are the source of truth — same statements appear
// in tests and in production. The actual sql.DB / pgxpool.Pool wiring
// lands in repository.go when a Postgres driver dependency is added; for
// now, this file documents the contract.
//
// See docs/consume.md for why the consume statement is one CTE rather
// than the legacy update + redis lock + idempotency flag pattern.
package postgres

// Schema lives in deploy/postgres/migrations/0001_initial.sql.

// queryInit upserts a fresh lookup. ON CONFLICT DO NOTHING makes Init
// idempotent — a duplicate call doesn't overwrite the existing row's
// signals or score. The RETURNING clause yields the row whether it was
// inserted or already present.
const queryInit = `
WITH inserted AS (
    INSERT INTO edge.lookups
        (req_id, tenant_id, ip, ja3, signals_json, score, verdict, edge_region, created_at)
    VALUES ($1, $2, $3, $4, $5, $6, $7, $8, COALESCE($9, NOW()))
    ON CONFLICT (req_id, created_at) DO NOTHING
    RETURNING req_id, tenant_id, ip, ja3, signals_json, score, verdict,
              edge_region, created_at, consumed_at, consumed_by
)
SELECT * FROM inserted
UNION ALL
SELECT req_id, tenant_id, ip, ja3, signals_json, score, verdict,
       edge_region, created_at, consumed_at, consumed_by
  FROM edge.lookups
 WHERE req_id = $1
   AND tenant_id = $2
   AND NOT EXISTS (SELECT 1 FROM inserted)
LIMIT 1;
`

// queryGet is a read-only fetch.
const queryGet = `
SELECT req_id, tenant_id, ip, ja3, signals_json, score, verdict,
       edge_region, created_at, consumed_at, consumed_by
  FROM edge.lookups
 WHERE req_id = $1
 LIMIT 1;
`

// queryConsume is the atomic idempotency-key statement from docs/consume.md.
// One CTE handles both first-time consume (UPDATE matches) and retry
// (UPDATE doesn't match, SELECT returns the cached row with replayed=true).
//
// Concurrent callers serialise at the row lock on UPDATE. The winner sets
// consumed_at; losers fall through to the SELECT and return the same body.
const queryConsume = `
WITH attempt AS (
    UPDATE edge.lookups
       SET consumed_at = NOW(),
           consumed_by = $3
     WHERE req_id = $1
       AND tenant_id = $2
       AND consumed_at IS NULL
     RETURNING req_id, tenant_id, ip, ja3, signals_json, score, verdict,
               edge_region, created_at, consumed_at, consumed_by,
               false AS replayed
)
SELECT * FROM attempt
UNION ALL
SELECT req_id, tenant_id, ip, ja3, signals_json, score, verdict,
       edge_region, created_at, consumed_at, consumed_by,
       true AS replayed
  FROM edge.lookups
 WHERE req_id = $1
   AND tenant_id = $2
   AND consumed_at IS NOT NULL
   AND NOT EXISTS (SELECT 1 FROM attempt)
LIMIT 1;
`

// queryDebitQuota records the consumed lookup against the tenant's
// monthly quota. ON CONFLICT DO NOTHING makes retries no-op — a replayed
// consume doesn't re-debit. Run in the same transaction as queryConsume.
const queryDebitQuota = `
INSERT INTO edge.quota_debits (tenant_id, req_id)
VALUES ($1, $2)
ON CONFLICT (tenant_id, req_id) DO NOTHING;
`

// queryAuditConsume appends to the audit log. Runs for every consume
// attempt (first-time AND replay), independently of whether the consume
// itself succeeded — used for dispute resolution and anomaly detection.
const queryAuditConsume = `
INSERT INTO edge.consume_audit
    (tenant_id, req_id, consumed_at, consumed_by, replayed, edge_region)
VALUES ($1, $2, $3, $4, $5, $6);
`
