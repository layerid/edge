// Package signals holds the individual signal implementations for the
// WeightedAverage scorer. Each signal is one file. Adding a new signal is
// three steps: implement SignalFunc, add it to DefaultRegistry, set its
// default weight.
package signals

import (
	"sort"

	"github.com/layerid/edge/internal/score"
)

// SignalFunc evaluates one signal against the input.
//
// Returns:
//   - sc: the signal's score in [0.0, 1.0]. 1.0 = consistent with a legit
//     human user, 0.0 = consistent with abuse.
//   - available: false if this signal couldn't be computed (e.g. STUN
//     blocked, no WebGL data). Unavailable signals are excluded from the
//     weighted average — they don't penalise.
//   - detail: optional human-readable explanation for the dashboard.
type SignalFunc func(input score.Signals) (sc float64, available bool, detail string)

// Entry is one row in the signal registry.
type Entry struct {
	Name          string
	DefaultWeight float64
	Fn            SignalFunc
}

// Registry is the ordered set of signals the WeightedAverage scorer
// evaluates. Order matters for output stability (the Explanation lists
// signals in registration order).
type Registry struct {
	entries []Entry
	weights map[string]float64
}

// NewRegistry constructs an empty registry.
func NewRegistry() *Registry {
	return &Registry{
		weights: make(map[string]float64),
	}
}

// Register adds a signal. Re-registering replaces the previous entry's
// function but preserves the weight (useful for testing).
func (r *Registry) Register(name string, defaultWeight float64, fn SignalFunc) {
	r.entries = append(r.entries, Entry{
		Name:          name,
		DefaultWeight: defaultWeight,
		Fn:            fn,
	})
	if _, exists := r.weights[name]; !exists {
		r.weights[name] = defaultWeight
	}
}

// SetWeight overrides the active weight for a registered signal. Used for
// per-tenant weight customisation.
func (r *Registry) SetWeight(name string, w float64) {
	r.weights[name] = w
}

// Evaluate runs every signal and returns the contributions in registration
// order. Each contribution carries the active weight (per-tenant if set,
// otherwise the signal's default).
func (r *Registry) Evaluate(input score.Signals) []score.SignalContribution {
	out := make([]score.SignalContribution, 0, len(r.entries))
	for _, e := range r.entries {
		sc, available, detail := e.Fn(input)
		w := r.weights[e.Name]
		c := score.SignalContribution{
			Name:      e.Name,
			Available: available,
			Score:     sc,
			Weight:    w,
			Detail:    detail,
		}
		if available {
			c.Contribution = sc * w
		}
		out = append(out, c)
	}
	return out
}

// Names returns the registered signal names in registration order.
func (r *Registry) Names() []string {
	names := make([]string, len(r.entries))
	for i, e := range r.entries {
		names[i] = e.Name
	}
	return names
}

// Weights returns a copy of the active weight map. Sorted output.
func (r *Registry) Weights() map[string]float64 {
	out := make(map[string]float64, len(r.weights))
	for k, v := range r.weights {
		out[k] = v
	}
	return out
}

// signalNamesSorted returns the registry's signal names sorted alphabetically.
// Kept for completeness — registration order is the public surface.
func (r *Registry) signalNamesSorted() []string {
	out := r.Names()
	sort.Strings(out)
	return out
}

// DefaultRegistry returns the canonical 14-signal registry, ported from
// the legacy fingerprints/ scoring.py. Weights match the legacy values;
// see docs/scoring-model.md for the table.
//
// Signals not yet implemented (port pending) are registered with
// notImplemented as the function and weight 0 so they show in Explain
// output but never affect the score.
func DefaultRegistry() *Registry {
	r := NewRegistry()

	// Implemented (real ports go here as they land):
	r.Register("ip_match", 0.15, IPMatch)

	// Pending ports — registered as stubs with weight 0. Replace each
	// notImplemented with the real SignalFunc when porting from legacy.
	r.Register("ua_match",           0.15, notImplemented)
	r.Register("os_match",           0.20, notImplemented)
	r.Register("headers_order",      0.10, notImplemented)
	r.Register("timings",            0.05, notImplemented)
	r.Register("proxy_detection",    0.25, notImplemented)
	r.Register("timezone",           0.10, notImplemented)
	r.Register("screen_resolution",  0.05, notImplemented)
	r.Register("window_screen_ratio",0.05, notImplemented)
	r.Register("webgl_renderer",     0.05, notImplemented)
	r.Register("webdriver",          0.20, notImplemented)
	r.Register("residential_proxy",  0.15, notImplemented)
	r.Register("ip_reputation",      0.25, notImplemented)
	r.Register("stun_webrtc",        0.25, notImplemented)

	return r
}

// notImplemented marks a signal as registered but not yet ported. Returns
// available=false so it doesn't affect the weighted average. Useful during
// the legacy→Go port so we can land signals one at a time.
func notImplemented(_ score.Signals) (float64, bool, string) {
	return 0, false, "port pending"
}
