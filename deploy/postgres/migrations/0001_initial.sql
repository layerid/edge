-- 0001_initial.sql — layerid-edge schema, initial migration.
--
-- This is the source of truth for the `edge` schema. Run via:
--   psql -d layerid -f 0001_initial.sql
--
-- Co-exists in the same Postgres instance as intel-service (separate schema).
-- The fingerprints-migration/backend repo owns the customer-facing tables
-- (tenants, api_keys, billing_meter); see ../../../fingerprints-migration/
-- backend/DESIGN.md. The tables here are the consume + audit + scoring-
-- weight tables that the edge runtime reads/writes directly.

BEGIN;

CREATE SCHEMA IF NOT EXISTS edge;
SET search_path = edge, public;

-- ─── lookups ───────────────────────────────────────────────────────
-- One row per req_id. Holds the precomputed signals + score from warmup,
-- plus the consumed_at flag flipped by Consume. Partitioned monthly so
-- hot data is fast and cold data archives via partition drop.
CREATE TABLE IF NOT EXISTS lookups (
    req_id          UUID NOT NULL,
    tenant_id       BIGINT NOT NULL,
    ip              INET,
    ja3             VARCHAR(64),
    signals_json    JSONB NOT NULL,
    score           NUMERIC(4,3),
    verdict         VARCHAR(32),
    edge_region     VARCHAR(16),
    created_at      TIMESTAMPTZ NOT NULL,
    consumed_at     TIMESTAMPTZ,
    consumed_by     VARCHAR(255),
    PRIMARY KEY (req_id, created_at)
) PARTITION BY RANGE (created_at);

-- Bootstrap partition (extend via pg_partman in production).
CREATE TABLE IF NOT EXISTS lookups_p2026_05 PARTITION OF lookups
    FOR VALUES FROM ('2026-05-01') TO ('2026-06-01');
CREATE TABLE IF NOT EXISTS lookups_p2026_06 PARTITION OF lookups
    FOR VALUES FROM ('2026-06-01') TO ('2026-07-01');

CREATE INDEX IF NOT EXISTS lookups_tenant_created ON lookups (tenant_id, created_at);
CREATE INDEX IF NOT EXISTS lookups_unconsumed     ON lookups (tenant_id) WHERE consumed_at IS NULL;
CREATE INDEX IF NOT EXISTS lookups_ip             ON lookups (tenant_id, ip);

-- ─── quota_debits ──────────────────────────────────────────────────
-- One row per consumed lookup. ON CONFLICT DO NOTHING enforces single
-- debit on retries. Rolled up nightly into billing_meter (owned by the
-- BFF, not here).
CREATE TABLE IF NOT EXISTS quota_debits (
    tenant_id       BIGINT NOT NULL,
    req_id          UUID NOT NULL,
    debited_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, req_id)
);
CREATE INDEX IF NOT EXISTS quota_debits_tenant_time ON quota_debits (tenant_id, debited_at);

-- ─── consume_audit ─────────────────────────────────────────────────
-- Append-only log of every consume attempt. Forensics + dispute
-- resolution; not in the consume hot path's correctness contract.
CREATE TABLE IF NOT EXISTS consume_audit (
    id              BIGSERIAL PRIMARY KEY,
    tenant_id       BIGINT NOT NULL,
    req_id          UUID NOT NULL,
    consumed_at     TIMESTAMPTZ NOT NULL,
    consumed_by     VARCHAR(255),
    replayed        BOOLEAN NOT NULL,
    edge_region     VARCHAR(16)
);
CREATE INDEX IF NOT EXISTS consume_audit_tenant_time ON consume_audit (tenant_id, consumed_at);
CREATE INDEX IF NOT EXISTS consume_audit_req_id      ON consume_audit (req_id);

-- ─── scoring_weights ───────────────────────────────────────────────
-- Per-tenant overrides for the WeightedAverage scorer's signal weights.
-- Falls back to the default weight registered in code when no row exists.
CREATE TABLE IF NOT EXISTS scoring_weights (
    tenant_id       BIGINT NOT NULL,
    signal_name     VARCHAR(64) NOT NULL,
    weight          NUMERIC(4,3) NOT NULL CHECK (weight >= 0 AND weight <= 1),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (tenant_id, signal_name)
);

-- ─── customer_scorer_override ──────────────────────────────────────
-- Per-tenant override of which scorer to use. NULL = use tier default.
-- The dashboard sets this when a customer opts into a non-default scorer
-- (e.g. shadow-mode evaluation of an experimental model).
CREATE TABLE IF NOT EXISTS customer_scorer_override (
    tenant_id       BIGINT PRIMARY KEY,
    scorer_name     VARCHAR(64),
    shadow_against  VARCHAR(64),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ─── intel_cache ───────────────────────────────────────────────────
-- Optional local cache of intel-service responses. Edge populates on
-- intel lookup; serves from cache for the TTL window. Redis is the
-- primary cache; this is a fallback for "Redis down" + warm restarts.
CREATE TABLE IF NOT EXISTS intel_cache (
    cache_key       VARCHAR(128) PRIMARY KEY,
    payload         JSONB NOT NULL,
    fetched_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at      TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS intel_cache_expires ON intel_cache (expires_at);

COMMIT;
