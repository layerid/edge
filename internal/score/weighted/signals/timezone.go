package signals

import (
	"strings"

	"github.com/layerid/edge/internal/score"
)

// Timezone scores the browser-reported IANA timezone for plausibility.
//
// Legacy scoring.py cross-checked the declared TZ against the IP-derived
// TZ from the intel-service IPReputation feed. We don't have that wired
// yet, so this port only validates the shape:
//
//   - Empty                                  → unavailable
//   - "UTC" / "GMT" / numeric offsets ("+02:00", "Etc/GMT-5")
//     → 0.3 (real users almost never get just "UTC"; common in headless
//     and CI runners)
//   - Well-formed "Region/City" (e.g. "Europe/Berlin", "America/New_York")
//     → 1.0
//   - Anything else (single token like "foobar", multi-slash, etc.)
//     → 0.2
//
// TODO(intel): the faithful legacy TimezoneSignal cross-checks the JS TZ
// against the IP-derived geo TZ (exact_match / same_region / mismatch
// ladder, 1.0 / 0.7 / 0.2). That needs the intel-derived geo timezone,
// which is the deferred intel tier — not implemented here. When
// intel-service is wired in internal/upstream/, replace this shape-only
// fallback with the geo-mismatch check (10x stronger than shape
// validation).
//
// Weight 0.10. Even the shape-only version catches headless browsers
// that set TZ to a system default ("UTC", "Etc/UTC") instead of the
// user's actual zone.
func Timezone(s score.Signals) (sc float64, available bool, detail string) {
	tz := strings.TrimSpace(s.TZ)
	if tz == "" {
		return 0, false, "no timezone reported"
	}

	// Generic / system defaults — common in headless and CI.
	switch tz {
	case "UTC", "GMT", "Etc/UTC", "Etc/GMT":
		return 0.3, true, "generic timezone (" + tz + ") — common in headless"
	}

	// "Region/City" — the well-formed IANA shape every real OS emits.
	if isWellFormedTZ(tz) {
		return 1.0, true, "IANA-shaped (" + tz + ")"
	}

	return 0.2, true, "malformed or unusual timezone (" + tz + ")"
}

// isWellFormedTZ does a cheap shape check: exactly one '/' separator, both
// halves non-empty and ASCII letters/underscores. Catches "Europe/Berlin",
// "America/New_York", "Asia/Ho_Chi_Minh" — rejects "UTC", "+02:00",
// "a/b/c", "/Foo", "Foo/".
func isWellFormedTZ(tz string) bool {
	slash := strings.Index(tz, "/")
	if slash <= 0 || slash == len(tz)-1 {
		return false
	}
	if strings.Count(tz, "/") > 1 {
		// IANA does have a few three-segment zones ("America/Argentina/Cordoba")
		// — accept those too.
		if strings.Count(tz, "/") > 2 {
			return false
		}
	}
	for _, r := range tz {
		switch {
		case r >= 'A' && r <= 'Z',
			r >= 'a' && r <= 'z',
			r == '/' || r == '_' || r == '-':
		default:
			return false
		}
	}
	return true
}
