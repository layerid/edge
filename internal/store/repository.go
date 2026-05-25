// Package store defines the persistence interface for lookups + quota.
//
// Two implementations live alongside this file:
//
//   - memory/  — in-process, used in tests
//   - postgres/ — production, atomic CTE for consume per docs/consume.md
//
// The interface intentionally hides the storage technology — handlers in
// internal/api/ work against Repository, swapping implementations in tests.
package store

import (
	"context"
	"errors"
	"time"

	"github.com/layerid/edge/internal/score"
)

// Lookup is the persistent shape of one fingerprint request, captured at
// init time. The signals_json column on disk maps to Signals here.
type Lookup struct {
	ReqID       string
	TenantID    int64
	Signals     score.Signals
	Score       float64
	Verdict     string
	Model       string
	EdgeRegion  string
	CreatedAt   time.Time
	ConsumedAt  *time.Time
	ConsumedBy  string
}

// ConsumeResult is what the consume CTE returns — either a fresh consume
// or a replay of a previously-consumed lookup. Replayed=true means the
// caller is retrying; same body, no quota debit.
type ConsumeResult struct {
	Lookup   Lookup
	Replayed bool
}

// Sentinel errors. Handlers translate these to HTTP/gRPC status codes.
var (
	// ErrNotFound: req_id doesn't exist (never created, or TTL'd out).
	ErrNotFound = errors.New("store: lookup not found")

	// ErrCrossTenant: req_id exists but belongs to a different tenant.
	// Surfaced as 404 (not 403) to avoid leaking req_id existence across
	// tenant boundaries.
	ErrCrossTenant = errors.New("store: lookup belongs to another tenant")
)

// Repository is the persistence interface. All methods are context-aware
// so handlers can cancel on client disconnect / deadline.
type Repository interface {
	// Init stores a fresh Lookup. Idempotent on (req_id, tenant_id) —
	// a duplicate Init returns the existing row, doesn't overwrite.
	Init(ctx context.Context, l Lookup) (Lookup, error)

	// Get returns a Lookup by req_id, scoped to tenant. Read-only —
	// doesn't debit quota, doesn't flip consumed_at.
	Get(ctx context.Context, reqID string, tenantID int64) (Lookup, error)

	// Consume implements the atomic idempotency-key contract from
	// docs/consume.md. First call flips consumed_at + debits quota;
	// subsequent calls return the same body with Replayed=true.
	Consume(ctx context.Context, reqID string, tenantID int64, consumedBy string) (ConsumeResult, error)
}
