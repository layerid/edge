package signals

import "github.com/layerid/edge/internal/score"

// WindowScreenRatio compares browser window dimensions to screen
// dimensions. Real users have window slightly smaller than screen due to
// OS chrome (taskbar, dock, browser tabs); ratio between 0.7 and 0.99 is
// the normal-user band.
//
// Headless browsers often default to 1.0 (window == screen) or to fixed
// sizes that don't match the screen. Antidetect tools sometimes set
// window dimensions larger than the screen — instantly suspicious.
//
// Ported from legacy scoring.py.
//
// Unavailable when either window or screen dimensions weren't reported.
func WindowScreenRatio(s score.Signals) (sc float64, available bool, detail string) {
	if s.ScreenW <= 0 || s.ScreenH <= 0 || s.WindowW <= 0 || s.WindowH <= 0 {
		return 0, false, "missing screen or window dims"
	}
	rw := float64(s.WindowW) / float64(s.ScreenW)
	rh := float64(s.WindowH) / float64(s.ScreenH)
	ratio := (rw + rh) / 2

	switch {
	case ratio > 1.0:
		return 0.1, true, "window larger than screen — likely antidetect"
	case ratio >= 0.7 && ratio <= 0.99:
		return 1.0, true, "normal browser chrome"
	case ratio >= 0.5 && ratio < 0.7:
		return 0.7, true, "small window — possible split-screen / phone"
	case ratio == 1.0:
		return 0.4, true, "window exactly equals screen — likely headless"
	default:
		return 0.4, true, "unusual ratio"
	}
}
