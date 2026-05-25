package signals

import "github.com/layerid/edge/internal/score"

// Webdriver flags the `navigator.webdriver` browser property.
//
// Real browsers expose `false`; automation frameworks (Selenium,
// Playwright, undetected-chromedriver pre-patch) expose `true`. Modern
// antidetect tooling does patch this away, so we can't trust a `false`
// result on its own — but a `true` is a near-certain bot signal. Weight
// stays high (0.20) because the false-positive rate is essentially zero.
//
// Ported from legacy scoring.py.
//
// Unavailable when the SDK didn't report the field (e.g. SDK not loaded,
// non-browser client).
func Webdriver(s score.Signals) (sc float64, available bool, detail string) {
	if s.Webdriver == nil {
		return 0, false, "no webdriver flag reported"
	}
	if *s.Webdriver {
		return 0.0, true, "navigator.webdriver === true (automation)"
	}
	return 1.0, true, "navigator.webdriver === false"
}
