package signals

import (
	"strings"

	"github.com/layerid/edge/internal/score"
)

// WebGLRenderer scores the WebGL GPU vendor/renderer string.
//
// Real devices report a real GPU vendor; headless Chromium without GPU
// acceleration falls back to software renderers (SwiftShader, llvmpipe,
// Mesa) and VMs report their virtual GPUs (VMware, VirtualBox, Parallels).
//
// Ported verbatim from legacy fingerprints/ scoring.py (WebGLRendererSignal)
// + value_objects.WebGLRenderer. Suspicious is checked BEFORE legitimate —
// "ANGLE (..., SwiftShader)" is suspicious even though it may also name a
// real vendor. Weight 0.05 (VERY_LOW): the string is trivial to spoof, but
// the easiest headless setup hits SwiftShader.
//
//	is_suspicious   → 0.2 "suspicious_<pattern>"
//	is_legitimate   → 1.0 "legitimate_gpu"
//	else            → 0.6 "unknown_renderer"
//
// Unavailable when the field wasn't reported.
func WebGLRenderer(s score.Signals) (sc float64, available bool, detail string) {
	v := strings.TrimSpace(s.WebGLVendor)
	if v == "" {
		return 0, false, "no webgl vendor reported"
	}

	low := strings.ToLower(v)
	for _, p := range webglSuspiciousPatterns {
		if strings.Contains(low, p) {
			return 0.2, true, "suspicious_" + strings.ReplaceAll(p, " ", "_")
		}
	}
	for _, vendor := range webglLegitimateVendors {
		if strings.Contains(low, vendor) {
			return 1.0, true, "legitimate_gpu"
		}
	}
	return 0.6, true, "unknown_renderer"
}

// webglSuspiciousPatterns — verbatim from value_objects.WebGLRenderer.
// SUSPICIOUS_PATTERNS. Substrings that indicate CPU-side / virtual GPU
// rendering.
var webglSuspiciousPatterns = []string{
	"swiftshader",
	"llvmpipe",
	"virtualbox",
	"vmware",
	"parallels",
	"microsoft basic",
	"mesa",
	"software",
}

// webglLegitimateVendors — verbatim from value_objects.WebGLRenderer.
// LEGITIMATE_VENDORS.
var webglLegitimateVendors = []string{
	"nvidia",
	"amd",
	"intel",
	"apple",
	"qualcomm",
	"arm",
	"mali",
}
