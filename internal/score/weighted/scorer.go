// Package weighted implements the 14-signal weighted-average scorer ported
// from the legacy fingerprints/ monolith.
//
// Each signal is its own file under signals/. Adding a new signal:
//
//  1. Implement SignalFunc in signals/<name>.go.
//  2. Register in signals/registry.go with a default weight of 0 (shadow).
//  3. After calibration, promote the default weight to its real value.
//
// See ../../docs/scoring-model.md for the full design.
package weighted

import (
	"github.com/layerid/edge/internal/score"
	"github.com/layerid/edge/internal/score/weighted/signals"
)

// Version of this scorer. Bumped whenever the model's behaviour changes
// in a way customers can observe (weights, thresholds, signal logic).
const (
	scorerName    = "weighted"
	scorerVersion = "v1"
)

// Thresholds map the final weighted score to a verdict. Values match the
// legacy fingerprints/ scoring.py (low: ≥0.7, medium: 0.4-0.7, high: <0.4).
const (
	thresholdLow    = 0.7
	thresholdMedium = 0.4
)

// minSignalsRequired is the floor for emitting any verdict. Below this,
// the scorer returns ErrInsufficientData so the edge surfaces
// verdict:"insufficient_data" rather than inventing a score from too
// little data. 14 total signals; require at least 6 to fire.
const minSignalsRequired = 6

// Scorer is the WeightedAverage strategy. Constructed with a SignalRegistry
// so the signal set + default weights can be swapped per tenant.
type Scorer struct {
	signals *signals.Registry
}

// New constructs a Scorer with the default signal registry (the 14 signals
// from legacy). Tests and per-tenant configs can pass a custom registry.
func New() *Scorer {
	return &Scorer{signals: signals.DefaultRegistry()}
}

// NewWithRegistry constructs a Scorer with a custom signal registry — used
// when a tenant has overridden weights or signals.
func NewWithRegistry(r *signals.Registry) *Scorer {
	return &Scorer{signals: r}
}

func (s *Scorer) Name() string    { return scorerName }
func (s *Scorer) Version() string { return scorerVersion }

// Score evaluates each registered signal against the input, takes the
// weighted average of available results, and maps to a verdict.
func (s *Scorer) Score(input score.Signals) (score.Result, error) {
	contributions := s.signals.Evaluate(input)

	var available int
	var weightSum, weightedSum float64
	for _, c := range contributions {
		if !c.Available {
			continue
		}
		available++
		weightSum += c.Weight
		weightedSum += c.Score * c.Weight
	}

	if available < minSignalsRequired {
		return score.Result{
			Score:   0,
			Verdict: score.VerdictInsufficient,
			Model:   s.modelStr(),
		}, score.ErrInsufficientData
	}

	final := 0.0
	if weightSum > 0 {
		final = weightedSum / weightSum
	}

	return score.Result{
		Score:   final,
		Verdict: verdictFor(final),
		Model:   s.modelStr(),
	}, nil
}

// Explain returns the per-signal breakdown used by the dashboard.
func (s *Scorer) Explain(input score.Signals) *score.Explanation {
	contributions := s.signals.Evaluate(input)

	signalsOut := make([]score.SignalContribution, 0, len(contributions))
	weights := make(map[string]float64, len(contributions))
	var total float64

	for _, c := range contributions {
		signalsOut = append(signalsOut, c)
		weights[c.Name] = c.Weight
		if c.Available {
			total += c.Score * c.Weight
		}
	}

	return &score.Explanation{
		Signals: signalsOut,
		Weights: weights,
		Total:   total,
	}
}

func (s *Scorer) modelStr() string {
	return scorerName + "@" + scorerVersion
}

func verdictFor(s float64) string {
	switch {
	case s >= thresholdLow:
		return score.VerdictAllow
	case s >= thresholdMedium:
		return score.VerdictStepUp
	default:
		return score.VerdictDeny
	}
}
