package signals

import "github.com/layerid/edge/internal/score"

// ScreenResolution scores screen size for naturalness. Common resolutions
// from real-device populations get 1.0; valid-aspect-ratio non-common
// resolutions get 0.8; weird sizes (under-bounds, over-8K, etc.) get
// progressively lower.
//
// Ported verbatim from legacy fingerprints/ scoring.py
// (ScreenResolutionSignal.calculate) + value_objects.ScreenResolution.
// Weight 0.05 (VERY_LOW) — a determined bot can match common resolutions,
// so the real value is catching headless browsers on default sizes.
//
// Unavailable if either dimension is non-positive.
//
//	is_common                                      → 1.0 "common_resolution"
//	is_valid_aspect && 320<=w<=7680 && 240<=h<=4320 → 0.8 "valid_aspect"
//	w < 320 || h < 240                             → 0.1 "too_small"
//	w > 7680 || h > 4320                           → 0.3 "unusually_large"
//	w == h && w not in {768,1024}                  → 0.5 "square_unusual"
//	else                                           → 0.6 "uncommon"
func ScreenResolution(s score.Signals) (sc float64, available bool, detail string) {
	w, h := s.ScreenW, s.ScreenH
	if w <= 0 || h <= 0 {
		return 0, false, "no screen dimensions reported"
	}

	switch {
	case isCommonResolution(w, h):
		return 1.0, true, "common_resolution"
	case isValidAspect(w, h) && w >= 320 && w <= 7680 && h >= 240 && h <= 4320:
		return 0.8, true, "valid_aspect"
	case w < 320 || h < 240:
		return 0.1, true, "too_small"
	case w > 7680 || h > 4320:
		return 0.3, true, "unusually_large"
	case w == h && !isCommonSquare(w):
		return 0.5, true, "square_unusual"
	default:
		return 0.6, true, "uncommon"
	}
}

// commonResolutions is the verbatim (width, height) list from legacy
// value_objects.ScreenResolution.is_common. The is_common check matches
// either (w, h) or the rotated (h, w) — see isCommonResolution.
var commonResolutions = [][2]int32{
	{1920, 1080},
	{1366, 768},
	{1536, 864},
	{1440, 900},
	{1280, 720},
	{1280, 800},
	{1600, 900},
	{2560, 1440},
	{3840, 2160},
	{1920, 1200},
	{2560, 1600},
	{1680, 1050},
	{1280, 1024},
	{1024, 768},
	{375, 667},
	{414, 896},
	{390, 844},
	{428, 926},
	{393, 873},
	{360, 640},
	{375, 812},
	{414, 736},
	{768, 1024},
	{820, 1180},
}

// isCommonResolution matches the resolution against the common list in
// either orientation — (w, h) or (h, w) — exactly like the legacy
// `(self.width, self.height) in common or (self.height, self.width) in common`.
func isCommonResolution(w, h int32) bool {
	for _, p := range commonResolutions {
		if (p[0] == w && p[1] == h) || (p[0] == h && p[1] == w) {
			return true
		}
	}
	return false
}

// isValidAspect ports legacy value_objects.ScreenResolution.is_valid_aspect
// verbatim: aspect_ratio = width/height (NOT folded to >=1), compared
// against the target list with tolerance < 0.1. Portrait ratios have their
// own targets in the list, so no folding is needed.
func isValidAspect(w, h int32) bool {
	if h == 0 {
		return false
	}
	a := float64(w) / float64(h)
	targets := []float64{
		16.0 / 9, 16.0 / 10, 4.0 / 3, 3.0 / 2,
		9.0 / 16, 10.0 / 16, 3.0 / 4, 2.0 / 3,
	}
	for _, t := range targets {
		if absF(a-t) < 0.1 {
			return true
		}
	}
	return false
}

// isCommonSquare reports whether a square edge length is one of the
// legacy-whitelisted square sizes ({768, 1024}) excluded from the
// "square_unusual" branch.
func isCommonSquare(edge int32) bool {
	return edge == 768 || edge == 1024
}

func absF(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}
