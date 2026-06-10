package signals

import (
	"testing"

	"github.com/layerid/edge/internal/score"
)

func TestTimings(t *testing.T) {
	cases := []struct {
		appMs     int32
		wantScore float64
		wantAvail bool
	}{
		{0, 0.0, false},
		{120, 1.0, true},
		{300, 1.0, true},
		{301, 0.5, true},
		{600, 0.5, true},
		{601, 0.0, true},
		{2500, 0.0, true},
	}
	for _, c := range cases {
		sc, avail, _ := Timings(score.Signals{AppMs: c.appMs})
		if sc != c.wantScore || avail != c.wantAvail {
			t.Errorf("AppMs=%d: got (%v, %v), want (%v, %v)", c.appMs, sc, avail, c.wantScore, c.wantAvail)
		}
	}
}

func TestScreenResolution(t *testing.T) {
	cases := []struct {
		w, h      int32
		wantScore float64
		wantAvail bool
		label     string
	}{
		{0, 0, 0.0, false, "missing"},
		{1920, 1080, 1.0, true, "common"},
		{390, 844, 1.0, true, "iphone 14"},
		{3200, 1800, 0.8, true, "valid aspect (16:9)"},
		{100, 100, 0.1, true, "too small"},
		{12000, 6000, 0.3, true, "too large"},
		{1000, 333, 0.6, true, "unusual ratio (~3:1)"},
	}
	for _, c := range cases {
		sc, avail, _ := ScreenResolution(score.Signals{ScreenW: c.w, ScreenH: c.h})
		if sc != c.wantScore || avail != c.wantAvail {
			t.Errorf("%s (%dx%d): got (%v, %v), want (%v, %v)", c.label, c.w, c.h, sc, avail, c.wantScore, c.wantAvail)
		}
	}
}

func TestWindowScreenRatio(t *testing.T) {
	// Legacy two-axis band check (WindowScreenRatioSignal): width ratio and
	// height ratio are evaluated independently, NOT averaged.
	cases := []struct {
		sw, sh, ww, wh int32
		wantScore      float64
		wantAvail      bool
		label          string
	}{
		{0, 0, 0, 0, 0.0, false, "missing"},
		{1920, 1080, 1600, 900, 1.0, true, "normal chrome"},          // wr=hr=0.833 → normal_ratio
		{1920, 1080, 1920, 1080, 0.4, true, "exact match (headless)"}, // exact_match_suspicious
		{1920, 1080, 2000, 1100, 0.2, true, "window > screen"},        // window_exceeds_screen
		{1920, 1080, 900, 540, 0.6, true, "low ratio"},                // wr=0.469,hr=0.5 → unusual_ratio
		{1920, 1080, 1200, 700, 0.7, true, "split-screen / phone"},    // wr=0.625,hr=0.648 → acceptable_ratio
		{1920, 1080, 400, 700, 0.5, true, "very narrow window"},       // wr=0.208<0.3 → very_small_window
	}

	for _, c := range cases {
		sc, avail, _ := WindowScreenRatio(score.Signals{
			ScreenW: c.sw, ScreenH: c.sh,
			WindowW: c.ww, WindowH: c.wh,
		})
		if sc != c.wantScore || avail != c.wantAvail {
			t.Errorf("%s (s=%dx%d w=%dx%d): got (%v, %v), want (%v, %v)",
				c.label, c.sw, c.sh, c.ww, c.wh, sc, avail, c.wantScore, c.wantAvail)
		}
	}
}

func TestWebdriver(t *testing.T) {
	tr, fa := true, false
	cases := []struct {
		webdriver *bool
		wantScore float64
		wantAvail bool
		label     string
	}{
		{nil, 0.0, false, "not reported"},
		{&fa, 1.0, true, "false (real browser)"},
		{&tr, 0.0, true, "true (automation)"},
	}
	for _, c := range cases {
		sc, avail, _ := Webdriver(score.Signals{Webdriver: c.webdriver})
		if sc != c.wantScore || avail != c.wantAvail {
			t.Errorf("%s: got (%v, %v), want (%v, %v)", c.label, sc, avail, c.wantScore, c.wantAvail)
		}
	}
}

func TestTimezone(t *testing.T) {
	cases := []struct {
		tz        string
		wantScore float64
		wantAvail bool
		label     string
	}{
		{"", 0.0, false, "empty"},
		{"Europe/Berlin", 1.0, true, "IANA"},
		{"America/New_York", 1.0, true, "IANA with underscore"},
		{"America/Argentina/Cordoba", 1.0, true, "three-segment IANA"},
		{"Asia/Ho_Chi_Minh", 1.0, true, "three-token middle"},
		{"UTC", 0.3, true, "bare UTC"},
		{"Etc/UTC", 0.3, true, "Etc/UTC"},
		{"+02:00", 0.2, true, "numeric offset"},
		{"foobar", 0.2, true, "single token gibberish"},
		{"/Foo", 0.2, true, "leading slash"},
		{"Foo/", 0.2, true, "trailing slash"},
		{"a/b/c/d", 0.2, true, "four segments"},
	}
	for _, c := range cases {
		sc, avail, _ := Timezone(score.Signals{TZ: c.tz})
		if sc != c.wantScore || avail != c.wantAvail {
			t.Errorf("%s (%q): got (%v, %v), want (%v, %v)", c.label, c.tz, sc, avail, c.wantScore, c.wantAvail)
		}
	}
}

func TestWebGLRenderer(t *testing.T) {
	cases := []struct {
		vendor    string
		wantScore float64
		wantAvail bool
		label     string
	}{
		{"", 0.0, false, "empty"},
		{"Apple Inc.", 1.0, true, "Apple"},
		{"Intel Inc.", 1.0, true, "Intel"},
		{"NVIDIA Corporation", 1.0, true, "NVIDIA"},
		{"Qualcomm", 1.0, true, "Qualcomm"},
		{"Google Inc. (SwiftShader)", 0.2, true, "SwiftShader software"},
		{"Mesa/X.org llvmpipe", 0.2, true, "llvmpipe"},
		{"Mesa OffScreen", 0.2, true, "Mesa OffScreen"},
		{"ANGLE (Google, SwiftShader, ...)", 0.2, true, "ANGLE Google + SwiftShader"},
		{"VMware SVGA 3D", 0.2, true, "VMware virtual GPU"},
		{"Mystery GPU Co", 0.6, true, "unknown vendor"},
	}
	for _, c := range cases {
		sc, avail, _ := WebGLRenderer(score.Signals{WebGLVendor: c.vendor})
		if sc != c.wantScore || avail != c.wantAvail {
			t.Errorf("%s (%q): got (%v, %v), want (%v, %v)", c.label, c.vendor, sc, avail, c.wantScore, c.wantAvail)
		}
	}
}

func TestProxyDetection(t *testing.T) {
	cases := []struct {
		tcpRTT, appMs int32
		wantScore     float64
		wantAvail     bool
		label         string
	}{
		{0, 0, 0.0, false, "no timing"},
		{50, 100, 0.0, false, "no tcp rtt"},   // appMs set, tcpRTT 0 → unavailable wait
		{4, 10, 0.0, false, "tcp rtt < 5"},    // below the 5ms floor
		{20, 30, 1.0, true, "direct (ratio 1.5)"},
		{20, 40, 1.0, true, "direct (ratio 2.0)"},
		{20, 70, 0.8, true, "likely_direct (ratio 3.5)"},
		{20, 120, 0.5, true, "possible_proxy (ratio 6.0)"},
		{20, 240, 0.2, true, "likely_proxy (ratio 12.0)"},
		{20, 400, 0.0, true, "proxy_detected (ratio 20.0)"},
	}
	// Fix the mislabeled "no tcp rtt" expectation: appMs=100, tcpRTT=50 →
	// ratio 2.0 → 1.0. Replace with a genuine missing-tcp case.
	cases[1] = struct {
		tcpRTT, appMs int32
		wantScore     float64
		wantAvail     bool
		label         string
	}{0, 100, 0.0, false, "no tcp rtt"}
	for _, c := range cases {
		sc, avail, _ := ProxyDetection(score.Signals{TCPRTTMs: c.tcpRTT, AppMs: c.appMs})
		if sc != c.wantScore || avail != c.wantAvail {
			t.Errorf("%s (tcp=%d app=%d): got (%v, %v), want (%v, %v)", c.label, c.tcpRTT, c.appMs, sc, avail, c.wantScore, c.wantAvail)
		}
	}
}

func TestIPMatch(t *testing.T) {
	cases := []struct {
		ip, browser string
		wantScore   float64
		wantAvail   bool
		label       string
	}{
		{"", "", 0.0, false, "both empty"},
		{"203.0.113.7", "", 0.0, false, "no browser ip"},
		{"", "203.0.113.7", 0.0, false, "no peer ip"},
		{"203.0.113.7", "203.0.113.7", 1.0, true, "match"},
		{"203.0.113.7", "198.51.100.9", 0.0, true, "mismatch"},
		{"::ffff:1.2.3.4", "1.2.3.4", 1.0, true, "v4-mapped v6 == v4"},
	}
	for _, c := range cases {
		sc, avail, _ := IPMatch(score.Signals{IP: c.ip, BrowserReportedIP: c.browser})
		if sc != c.wantScore || avail != c.wantAvail {
			t.Errorf("%s (%q vs %q): got (%v, %v), want (%v, %v)", c.label, c.ip, c.browser, sc, avail, c.wantScore, c.wantAvail)
		}
	}
}

func TestStunWebrtc(t *testing.T) {
	cases := []struct {
		network   string
		stun      *score.STUNResult
		wantScore float64
		wantAvail bool
		label     string
	}{
		{"203.0.113.7", nil, 0.0, false, "no stun data"},
		{"203.0.113.7", &score.STUNResult{}, 0.0, false, "empty stun, no webrtc"},
		// Case 1: webrtc present, stun missing → antidetect_stun_blocked (0.1).
		{"203.0.113.7", &score.STUNResult{WebRTCLocalIPs: []string{"203.0.113.7"}}, 0.1, true, "webrtc no stun (antidetect)"},
		// Case 2: stun mismatch → 0.0.
		{"203.0.113.7", &score.STUNResult{PublicIP: "198.51.100.9"}, 0.0, true, "stun mismatch"},
		// Case 4: no webrtc, stun present but no public_ip is "not present" →
		// here PublicIP empty means stun_present=false; with empty webrtc both
		// false → unavailable; so use a stun_present-but-no-webrtc-match below.
		// Case 5: stun matches, no webrtc → 0.7.
		{"203.0.113.7", &score.STUNResult{PublicIP: "203.0.113.7"}, 0.7, true, "stun match no webrtc"},
		// Case 6: everything matches → 1.0.
		{"203.0.113.7", &score.STUNResult{PublicIP: "203.0.113.7", WebRTCLocalIPs: []string{"203.0.113.7"}}, 1.0, true, "full match"},
		// Case 3: stun matches but webrtc doesn't → webrtc_spoofed (0.2).
		{"203.0.113.7", &score.STUNResult{PublicIP: "203.0.113.7", WebRTCLocalIPs: []string{"10.0.0.1"}}, 0.2, true, "webrtc spoofed"},
		// Case 7: webrtc matches, no stun → 0.5.
		{"203.0.113.7", &score.STUNResult{WebRTCLocalIPs: []string{"203.0.113.7", "10.0.0.1"}}, 0.5, true, "webrtc match no stun? — case1 wins"},
	}
	// Note: the last case has webrtc present + stun absent, so Case 1
	// (antidetect_stun_blocked) fires FIRST in the legacy ladder, not Case 7.
	// Case 7 is only reachable when has_webrtc_ips && stun_present is false
	// AND Case 1 already returned — which it always does for webrtc+no-stun.
	// So correct the expectation to 0.1 (Case 1).
	cases[len(cases)-1].wantScore = 0.1
	cases[len(cases)-1].label = "webrtc present no stun → antidetect (case 1)"

	for _, c := range cases {
		sc, avail, detail := StunWebrtc(score.Signals{IP: c.network, STUNLeak: c.stun})
		if sc != c.wantScore || avail != c.wantAvail {
			t.Errorf("%s: got (%v, %v, %q), want (%v, %v)", c.label, sc, avail, detail, c.wantScore, c.wantAvail)
		}
	}
}
