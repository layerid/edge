// Package memory provides an in-process Repository for tests and dev.
//
// Production uses internal/store/postgres/ — see docs/consume.md for the
// atomic CTE that backs the consume contract. This implementation matches
// the same guarantees with a sync.Mutex.
package memory

import (
	"context"
	"sync"
	"time"

	"github.com/layerid/edge/internal/store"
)

// Repository is an in-process Repository implementation.
type Repository struct {
	mu      sync.Mutex
	lookups map[string]store.Lookup // key: req_id
	now     func() time.Time
}

// New constructs an empty Repository. now is the clock source; pass
// time.Now in production, a fake clock in tests for deterministic
// timestamps.
func New(now func() time.Time) *Repository {
	if now == nil {
		now = time.Now
	}
	return &Repository{
		lookups: make(map[string]store.Lookup),
		now:     now,
	}
}

func (r *Repository) Init(ctx context.Context, l store.Lookup) (store.Lookup, error) {
	if err := ctx.Err(); err != nil {
		return store.Lookup{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	// Idempotent on (req_id, tenant_id) — duplicate Init returns the
	// existing row, doesn't overwrite. Mirrors the Postgres UPSERT
	// behaviour with ON CONFLICT DO NOTHING.
	if existing, ok := r.lookups[l.ReqID]; ok && existing.TenantID == l.TenantID {
		return existing, nil
	}

	if l.CreatedAt.IsZero() {
		l.CreatedAt = r.now()
	}
	r.lookups[l.ReqID] = l
	return l, nil
}

func (r *Repository) Get(ctx context.Context, reqID string, tenantID int64) (store.Lookup, error) {
	if err := ctx.Err(); err != nil {
		return store.Lookup{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	l, ok := r.lookups[reqID]
	if !ok {
		return store.Lookup{}, store.ErrNotFound
	}
	if l.TenantID != tenantID {
		return store.Lookup{}, store.ErrCrossTenant
	}
	return l, nil
}

// Consume mirrors the atomic CTE in postgres/. Holds the mutex across
// the read-then-write to serialise concurrent callers; the loser sees
// the winner's consumed_at and falls through to the replay branch.
func (r *Repository) Consume(ctx context.Context, reqID string, tenantID int64, consumedBy string) (store.ConsumeResult, error) {
	if err := ctx.Err(); err != nil {
		return store.ConsumeResult{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	l, ok := r.lookups[reqID]
	if !ok {
		return store.ConsumeResult{}, store.ErrNotFound
	}
	if l.TenantID != tenantID {
		return store.ConsumeResult{}, store.ErrCrossTenant
	}

	if l.ConsumedAt != nil {
		// Retry: same body, no new debit.
		return store.ConsumeResult{Lookup: l, Replayed: true}, nil
	}

	now := r.now()
	l.ConsumedAt = &now
	l.ConsumedBy = consumedBy
	r.lookups[reqID] = l
	return store.ConsumeResult{Lookup: l, Replayed: false}, nil
}
