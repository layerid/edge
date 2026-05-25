package signals

import "github.com/layerid/edge/internal/score"

// ScreenResolution scores screen size for naturalness. Common resolutions
// from real-device populations get 1.0; valid-aspect-ratio non-common
// resolutions get 0.8; weird sizes (under-300px, over-8K, etc.) get
// progressively lower.
//
// Ported from legacy scoring.py; kept simple — a sufficiently determined
// bot can match common resolutions, so weight is low (0.05). Real value
// is catching headless browsers running on default screen sizes.
//
// Unavailable if screen dimensions weren't reported.
func ScreenResolution(s score.Signals) (sc float64, available bool, detail string) {
	if s.ScreenW <= 0 || s.ScreenH <= 0 {
		return 0, false, "no screen dimensions reported"
	}
	// Bounds first: a screen reported as 12000x6000 might fit some
	// aspect ratio mathematically but no real device looks like that —
	// reject before falling through to the friendlier aspect check.
	if s.ScreenW < 300 || s.ScreenH < 300 {
		return 0.3, true, "too small for a real device"
	}
	if s.ScreenW > 8000 || s.ScreenH > 8000 {
		return 0.3, true, "implausibly large"
	}
	if isCommonResolution(s.ScreenW, s.ScreenH) {
		return 1.0, true, "common resolution"
	}
	if isValidAspect(s.ScreenW, s.ScreenH) {
		return 0.8, true, "valid aspect, uncommon size"
	}
	return 0.6, true, "unusual but possible"
}

// commonResolutions holds the (width, height) pairs most observed in the
// legacy population data. List intentionally short — adding more catches
// fewer false positives.
var commonResolutions = map[[2]int32]struct{}{
	{1920, 1080}: {},
	{1366, 768}:  {},
	{1536, 864}:  {},
	{1440, 900}:  {},
	{1280, 720}:  {},
	{1280, 800}:  {},
	{2560, 1440}: {},
	{3840, 2160}: {},
	{390, 844}:   {}, // iPhone 14
	{393, 852}:   {}, // iPhone 15
	{375, 812}:   {}, // iPhone 11/12/13 mini
	{414, 896}:   {}, // iPhone XR/11
	{428, 926}:   {}, // iPhone Plus / Pro Max
	{412, 915}:   {}, // Pixel 7/8
	{360, 800}:   {}, // common Android
}

func isCommonResolution(w, h int32) bool {
	_, ok := commonResolutions[[2]int32{w, h}]
	return ok
}

// isValidAspect returns true if the screen has a plausible 4:3, 16:9,
// 16:10, 18:9, 19.5:9, 21:9, or 3:4 / 9:16 / 9:18 / 9:21 ratio (mobile
// portrait equivalents). Allows ±2% drift for browser chrome / DPR.
func isValidAspect(w, h int32) bool {
	if w == 0 || h == 0 {
		return false
	}
	a := float64(w) / float64(h)
	if a < 1 {
		a = 1 / a
	}
	targets := []float64{4.0 / 3, 16.0 / 9, 16.0 / 10, 18.0 / 9, 19.5 / 9, 21.0 / 9}
	for _, t := range targets {
		if a > t*0.98 && a < t*1.02 {
			return true
		}
	}
	return false
}
