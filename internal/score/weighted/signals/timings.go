package signals

import "github.com/layerid/edge/internal/score"

// Timings ports the legacy "timings" signal:
//
//   AppMs ≤ 300  → 1.0  (snappy: real user, real network)
//   AppMs ≤ 600  → 0.5  (acceptable; could be a slow phone or a small proxy)
//   AppMs >  600 → 0.0  (something's adding latency — proxy chain, automation)
//
// Unavailable if AppMs <= 0 (means we didn't capture timing).
//
// Source: ../../../fingerprints/src/modules/fingerprints/domain/scoring.py.
func Timings(s score.Signals) (sc float64, available bool, detail string) {
	if s.AppMs <= 0 {
		return 0, false, "no app timing captured"
	}
	switch {
	case s.AppMs <= 300:
		return 1.0, true, "snappy (≤300ms)"
	case s.AppMs <= 600:
		return 0.5, true, "acceptable (≤600ms)"
	default:
		return 0.0, true, "slow (>600ms) — likely proxied or automated"
	}
}
