package signals

import "github.com/layerid/edge/internal/score"

// WindowScreenRatio compares browser window dimensions to screen
// dimensions on BOTH axes independently. Real users have a window slightly
// smaller than the screen on each axis due to OS chrome (taskbar, dock,
// browser tabs); headless browsers often default to window == screen, and
// antidetect tooling sometimes sets the window larger than the screen.
//
// Ported verbatim from legacy fingerprints/ scoring.py
// (WindowScreenRatioSignal.calculate). The legacy evaluates the width
// ratio and height ratio as a two-axis band check — it does NOT average
// them. Weight 0.05 (VERY_LOW).
//
// Unavailable when any of the four dimensions is non-positive.
//
//	WW > SW || WH > SH                         → 0.2 "window_exceeds_screen"
//	WW == SW && WH == SH                       → 0.4 "exact_match_suspicious"
//	0.7<=wr<=0.99 && 0.6<=hr<=0.99             → 1.0 "normal_ratio"
//	0.5<=wr<=1.0  && 0.4<=hr<=1.0              → 0.7 "acceptable_ratio"
//	wr < 0.3 || hr < 0.3                       → 0.5 "very_small_window"
//	else                                       → 0.6 "unusual_ratio"
func WindowScreenRatio(s score.Signals) (sc float64, available bool, detail string) {
	if s.WindowW <= 0 || s.WindowH <= 0 || s.ScreenW <= 0 || s.ScreenH <= 0 {
		return 0, false, "missing screen or window dims"
	}

	ww, wh := float64(s.WindowW), float64(s.WindowH)
	sw, sh := float64(s.ScreenW), float64(s.ScreenH)
	wr := ww / sw
	hr := wh / sh

	switch {
	case ww > sw || wh > sh:
		return 0.2, true, "window_exceeds_screen"
	case ww == sw && wh == sh:
		return 0.4, true, "exact_match_suspicious"
	case wr >= 0.7 && wr <= 0.99 && hr >= 0.6 && hr <= 0.99:
		return 1.0, true, "normal_ratio"
	case wr >= 0.5 && wr <= 1.0 && hr >= 0.4 && hr <= 1.0:
		return 0.7, true, "acceptable_ratio"
	case wr < 0.3 || hr < 0.3:
		return 0.5, true, "very_small_window"
	default:
		return 0.6, true, "unusual_ratio"
	}
}
