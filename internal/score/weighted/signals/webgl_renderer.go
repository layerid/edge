package signals

import (
	"strings"

	"github.com/layerid/edge/internal/score"
)

// WebGLRenderer scores the WebGL GPU vendor/renderer string.
//
// Real devices report a real GPU vendor — "Apple Inc.", "Intel Inc.",
// "NVIDIA Corporation", "Qualcomm" (Adreno on Android), "ARM" (Mali on
// Android). Headless Chromium without GPU acceleration falls back to
// software renderers — "SwiftShader", "ANGLE", "Mesa", or the empty
// string when WebGL is disabled outright.
//
// Ported from legacy scoring.py. Weight stays low (0.05) because:
//   1. The string is trivial to spoof — antidetect tools rewrite it.
//   2. Some real users disable WebGL or use ungpu'd corp builds.
//
// The signal is still useful because the EASIEST headless setup (plain
// puppeteer / playwright with no GPU passthrough) hits SwiftShader.
//
// Unavailable when the field wasn't reported (SDK didn't send it).
func WebGLRenderer(s score.Signals) (sc float64, available bool, detail string) {
	v := strings.TrimSpace(s.WebGLVendor)
	if v == "" {
		return 0, false, "no webgl vendor reported"
	}

	low := strings.ToLower(v)
	for _, marker := range softwareRendererMarkers {
		if strings.Contains(low, marker) {
			return 0.1, true, "software renderer (" + v + ") — likely headless"
		}
	}
	for _, marker := range knownGoodVendorMarkers {
		if strings.Contains(low, marker) {
			return 1.0, true, "known GPU vendor (" + v + ")"
		}
	}
	// Unknown vendor — neither suspicious nor confirmed-good. Neutral.
	return 0.6, true, "unknown vendor (" + v + ")"
}

// softwareRendererMarkers — substrings that indicate CPU-side rendering,
// which is the default for headless Chromium without --use-gl=desktop.
var softwareRendererMarkers = []string{
	"swiftshader",
	"llvmpipe",
	"software",
	"mesa offscreen",
	"angle (google",
}

// knownGoodVendorMarkers — substrings that match real-device GPUs.
var knownGoodVendorMarkers = []string{
	"apple",
	"intel",
	"nvidia",
	"amd",
	"ati ",
	"qualcomm",
	"adreno",
	"arm ",
	"mali",
	"powervr",
}
