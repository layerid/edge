// Package constscorer returns fixed verdicts regardless of input. Used for:
//
//   - Tests (deterministic responses)
//   - Canary endpoints (force a specific verdict on certain traffic)
//   - "Panic kill switch" if we ship a broken model and need to fall back
//     to "allow all" / "step-up all" while we roll back.
package constscorer

import (
	"github.com/layerid/edge/internal/score"
)

type Scorer struct {
	name    string
	version string
	result  score.Result
}

// New returns a Scorer that always emits result, named name@version.
func New(name, version string, result score.Result) *Scorer {
	r := result
	if r.Model == "" {
		r.Model = name + "@" + version
	}
	return &Scorer{
		name:    name,
		version: version,
		result:  r,
	}
}

// Allow is a convenience constructor: always returns verdict:"allow" with
// score 1.0. Used as the "fail open" panic fallback.
func Allow() *Scorer {
	return New("const-allow", "v1", score.Result{
		Score:   1.0,
		Verdict: score.VerdictAllow,
	})
}

// Deny is a convenience constructor: always returns verdict:"deny" with
// score 0.0. Used for blocked traffic in tests.
func Deny() *Scorer {
	return New("const-deny", "v1", score.Result{
		Score:   0.0,
		Verdict: score.VerdictDeny,
	})
}

// StepUp is a convenience constructor: always returns verdict:"step-up"
// with score 0.5.
func StepUp() *Scorer {
	return New("const-step-up", "v1", score.Result{
		Score:   0.5,
		Verdict: score.VerdictStepUp,
	})
}

func (s *Scorer) Name() string    { return s.name }
func (s *Scorer) Version() string { return s.version }

func (s *Scorer) Score(_ score.Signals) (score.Result, error) {
	return s.result, nil
}

func (s *Scorer) Explain(_ score.Signals) *score.Explanation {
	return &score.Explanation{
		Signals: nil,
		Weights: nil,
		Total:   s.result.Score,
	}
}
