package memory_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/layerid/edge/internal/score"
	"github.com/layerid/edge/internal/store"
	"github.com/layerid/edge/internal/store/memory"
)

func newRepo(t *testing.T) *memory.Repository {
	t.Helper()
	return memory.New(func() time.Time {
		return time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	})
}

func sample(reqID string, tenant int64) store.Lookup {
	return store.Lookup{
		ReqID:    reqID,
		TenantID: tenant,
		Signals:  score.Signals{IP: "8.8.8.8"},
		Score:    0.72,
		Verdict:  score.VerdictAllow,
		Model:    "weighted@v1",
	}
}

func TestInitAndGet(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()

	if _, err := r.Init(ctx, sample("req-1", 42)); err != nil {
		t.Fatalf("init: %v", err)
	}

	got, err := r.Get(ctx, "req-1", 42)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Score != 0.72 {
		t.Errorf("score: got %v, want 0.72", got.Score)
	}

	// Cross-tenant: same req_id, different tenant → not found.
	if _, err := r.Get(ctx, "req-1", 99); !errors.Is(err, store.ErrCrossTenant) {
		t.Errorf("cross-tenant: got %v, want ErrCrossTenant", err)
	}

	// Missing: never-created req_id.
	if _, err := r.Get(ctx, "nope", 42); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("missing: got %v, want ErrNotFound", err)
	}
}

func TestInitIsIdempotent(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()

	first := sample("req-1", 42)
	if _, err := r.Init(ctx, first); err != nil {
		t.Fatalf("first init: %v", err)
	}

	// Re-init with the same req_id + tenant returns the original,
	// doesn't overwrite.
	second := first
	second.Score = 0.11
	got, err := r.Init(ctx, second)
	if err != nil {
		t.Fatalf("second init: %v", err)
	}
	if got.Score != 0.72 {
		t.Errorf("score: got %v, want 0.72 (idempotent)", got.Score)
	}
}

func TestConsumeFirstThenReplay(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()
	_, _ = r.Init(ctx, sample("req-1", 42))

	// First consume: replayed=false, consumed_at set.
	first, err := r.Consume(ctx, "req-1", 42, "10.0.0.1")
	if err != nil {
		t.Fatalf("first consume: %v", err)
	}
	if first.Replayed {
		t.Errorf("first consume: replayed=true, want false")
	}
	if first.Lookup.ConsumedAt == nil {
		t.Errorf("first consume: consumed_at not set")
	}

	// Second consume: replayed=true, same body.
	second, err := r.Consume(ctx, "req-1", 42, "10.0.0.1")
	if err != nil {
		t.Fatalf("second consume: %v", err)
	}
	if !second.Replayed {
		t.Errorf("second consume: replayed=false, want true")
	}
	if second.Lookup.Score != first.Lookup.Score {
		t.Errorf("second consume: score drifted from %v to %v", first.Lookup.Score, second.Lookup.Score)
	}
}

func TestConsumeNotFound(t *testing.T) {
	r := newRepo(t)
	if _, err := r.Consume(context.Background(), "nope", 42, "x"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("got %v, want ErrNotFound", err)
	}
}

func TestConsumeConcurrent(t *testing.T) {
	// The race-safe property from docs/consume.md: N concurrent calls,
	// exactly one returns Replayed=false, all N return the same body.
	r := newRepo(t)
	ctx := context.Background()
	_, _ = r.Init(ctx, sample("req-1", 42))

	const N = 100
	var wg sync.WaitGroup
	results := make([]store.ConsumeResult, N)
	errs := make([]error, N)

	for i := range N {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = r.Consume(ctx, "req-1", 42, "x")
		}(i)
	}
	wg.Wait()

	firstCount := 0
	var firstScore float64
	for i := range N {
		if errs[i] != nil {
			t.Fatalf("worker %d: %v", i, errs[i])
		}
		if !results[i].Replayed {
			firstCount++
			firstScore = results[i].Lookup.Score
		}
	}
	if firstCount != 1 {
		t.Errorf("got %d first-time consumes, want exactly 1", firstCount)
	}
	for i := range N {
		if results[i].Lookup.Score != firstScore {
			t.Errorf("worker %d returned score %v, expected %v across all callers", i, results[i].Lookup.Score, firstScore)
		}
	}
}
