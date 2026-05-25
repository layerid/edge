package score

import (
	"fmt"
	"sort"
	"sync"
)

// Registry is the runtime lookup table from scorer name → Scorer instance.
//
// Edges configure a default scorer per tenant tier; per-tenant overrides come
// from the customer_scorer_override table (handled in fingerprints-migration/
// backend, not here).
type Registry struct {
	mu      sync.RWMutex
	scorers map[string]Scorer
}

func NewRegistry() *Registry {
	return &Registry{scorers: make(map[string]Scorer)}
}

// Register adds a scorer under its declared Name(). Re-registering a name
// with a different version is allowed (and intentional — that's the
// migration story for scorer upgrades). Re-registering identical
// name+version returns an error.
func (r *Registry) Register(s Scorer) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	name := s.Name()
	if name == "" {
		return fmt.Errorf("score: scorer has empty Name()")
	}
	if existing, ok := r.scorers[name]; ok {
		if existing.Version() == s.Version() {
			return fmt.Errorf("score: scorer %q@%q already registered", name, s.Version())
		}
	}
	r.scorers[name] = s
	return nil
}

// Get looks up a scorer by name. Returns ErrNotFound if missing.
func (r *Registry) Get(name string) (Scorer, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.scorers[name]
	if !ok {
		return nil, fmt.Errorf("score: scorer %q not registered", name)
	}
	return s, nil
}

// Names returns the sorted list of registered scorer names. Useful for
// /admin endpoints and tests.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.scorers))
	for n := range r.scorers {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
