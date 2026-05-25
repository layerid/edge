package score_test

import (
	"errors"
	"testing"

	"github.com/layerid/edge/internal/score"
	"github.com/layerid/edge/internal/score/constscorer"
	"github.com/layerid/edge/internal/score/weighted"
)

// TestScorerInterface validates that the canonical scorers all satisfy the
// Scorer interface contract.
func TestScorerInterface(t *testing.T) {
	scorers := []score.Scorer{
		constscorer.Allow(),
		constscorer.Deny(),
		constscorer.StepUp(),
		weighted.New(),
	}
	for _, s := range scorers {
		if s.Name() == "" {
			t.Errorf("scorer has empty Name()")
		}
		if s.Version() == "" {
			t.Errorf("scorer %q has empty Version()", s.Name())
		}
	}
}

// TestConstScorer verifies that const scorers return their fixed verdict
// regardless of input.
func TestConstScorer(t *testing.T) {
	tests := []struct {
		scorer  score.Scorer
		verdict string
	}{
		{constscorer.Allow(), score.VerdictAllow},
		{constscorer.Deny(), score.VerdictDeny},
		{constscorer.StepUp(), score.VerdictStepUp},
	}
	for _, tt := range tests {
		r, err := tt.scorer.Score(score.Signals{})
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", tt.scorer.Name(), err)
		}
		if r.Verdict != tt.verdict {
			t.Errorf("%s: verdict %q, want %q", tt.scorer.Name(), r.Verdict, tt.verdict)
		}
		if r.Model == "" {
			t.Errorf("%s: empty Model on result", tt.scorer.Name())
		}
	}
}

// TestRegistry validates basic registry behaviour: register, get, names.
func TestRegistry(t *testing.T) {
	r := score.NewRegistry()
	if err := r.Register(constscorer.Allow()); err != nil {
		t.Fatalf("register allow: %v", err)
	}
	if err := r.Register(constscorer.Deny()); err != nil {
		t.Fatalf("register deny: %v", err)
	}

	got, err := r.Get("const-allow")
	if err != nil {
		t.Fatalf("get const-allow: %v", err)
	}
	if got.Name() != "const-allow" {
		t.Errorf("get returned %q, want const-allow", got.Name())
	}

	if _, err := r.Get("nonexistent"); err == nil {
		t.Errorf("get nonexistent: expected error, got nil")
	}

	names := r.Names()
	if len(names) != 2 {
		t.Errorf("names: got %d, want 2 (%v)", len(names), names)
	}
}

// TestWeightedInsufficient confirms that the WeightedAverage scorer returns
// ErrInsufficientData when too few signals fire. Because only ip_match is
// implemented and the other 13 are notImplemented stubs returning
// available=false, every call should land in the insufficient-data branch.
func TestWeightedInsufficient(t *testing.T) {
	s := weighted.New()
	input := score.Signals{IP: "8.8.8.8"}
	r, err := s.Score(input)
	if !errors.Is(err, score.ErrInsufficientData) {
		t.Errorf("expected ErrInsufficientData, got err=%v r=%+v", err, r)
	}
	if r.Verdict != score.VerdictInsufficient {
		t.Errorf("verdict %q, want insufficient_data", r.Verdict)
	}
}

// TestWeightedExplain confirms that Explain returns a per-signal breakdown
// even when Score returns ErrInsufficientData.
func TestWeightedExplain(t *testing.T) {
	s := weighted.New()
	exp := s.Explain(score.Signals{IP: "8.8.8.8"})
	if exp == nil {
		t.Fatal("Explain returned nil")
	}
	if len(exp.Signals) != 14 {
		t.Errorf("expected 14 signals in explanation, got %d", len(exp.Signals))
	}
	// All signals should be in the weights map.
	if len(exp.Weights) != 14 {
		t.Errorf("expected 14 weights, got %d", len(exp.Weights))
	}
}
