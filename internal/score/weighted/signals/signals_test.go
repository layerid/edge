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
