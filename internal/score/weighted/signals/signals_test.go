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
		{100, 100, 0.3, true, "too small"},
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
	cases := []struct {
		sw, sh, ww, wh int32
		wantScore      float64
		wantAvail      bool
		label          string
	}{
		{0, 0, 0, 0, 0.0, false, "missing"},
		{1920, 1080, 1600, 900, 1.0, true, "normal chrome"},   // ratio ~0.83
		{1920, 1080, 1920, 1080, 0.4, true, "exact match (headless)"},
		{1920, 1080, 2000, 1100, 0.1, true, "window > screen"},
		{1920, 1080, 900, 540, 0.7, true, "small window"},     // ratio 0.46+0.5/2 ≈ 0.48 < 0.5 hmm
	}
	// The "small window" case above: rw=0.469, rh=0.5, ratio=0.484
	// — falls into the "unusual ratio" 0.4 branch, not 0.7. Adjust:
	cases[4].wantScore = 0.4
	cases[4].label = "unusual ratio (low)"

	// Add one that genuinely lands in the 0.5–0.7 band:
	cases = append(cases, struct {
		sw, sh, ww, wh int32
		wantScore      float64
		wantAvail      bool
		label          string
	}{1920, 1080, 1200, 700, 0.7, true, "split-screen / phone"})

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
		{"Google Inc. (SwiftShader)", 0.1, true, "SwiftShader software"},
		{"Mesa/X.org llvmpipe", 0.1, true, "llvmpipe"},
		{"Mesa OffScreen", 0.1, true, "Mesa OffScreen"},
		{"ANGLE (Google, SwiftShader, ...)", 0.1, true, "ANGLE Google"},
		{"Mystery GPU Co", 0.6, true, "unknown vendor"},
	}
	for _, c := range cases {
		sc, avail, _ := WebGLRenderer(score.Signals{WebGLVendor: c.vendor})
		if sc != c.wantScore || avail != c.wantAvail {
			t.Errorf("%s (%q): got (%v, %v), want (%v, %v)", c.label, c.vendor, sc, avail, c.wantScore, c.wantAvail)
		}
	}
}
