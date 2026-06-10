package signals

import (
	"fmt"

	"github.com/layerid/edge/internal/score"
)

// ProxyDetection compares the app-layer timing against the transport RTT.
// A direct connection has app timing close to the raw TCP RTT; a proxy
// chain inflates the ratio because every hop adds round-trips above the
// transport handshake the edge observes.
//
// Ported verbatim from legacy fingerprints/ scoring.py
// (ProxyDetectionSignal.calculate). Weight 0.25 (HIGH).
//
// Unavailable unless both timings are present and the transport RTT is at
// least 5ms — below that the ratio is dominated by measurement noise.
//
// ratio = AppMs / TCPRTTMs:
//
//	<= 2.0  → 1.0  "direct_connection"
//	<= 4.0  → 0.8  "likely_direct"
//	<= 8.0  → 0.5  "possible_proxy"
//	<= 15.0 → 0.2  "likely_proxy"
//	else    → 0.0  "proxy_detected"
func ProxyDetection(s score.Signals) (sc float64, available bool, detail string) {
	if s.TCPRTTMs <= 0 || s.AppMs <= 0 || s.TCPRTTMs < 5 {
		return 0, false, "no transport/app timing for proxy detection"
	}

	ratio := float64(s.AppMs) / float64(s.TCPRTTMs)

	var (
		scoreVal float64
		label    string
	)
	switch {
	case ratio <= 2.0:
		scoreVal, label = 1.0, "direct_connection"
	case ratio <= 4.0:
		scoreVal, label = 0.8, "likely_direct"
	case ratio <= 8.0:
		scoreVal, label = 0.5, "possible_proxy"
	case ratio <= 15.0:
		scoreVal, label = 0.2, "likely_proxy"
	default:
		scoreVal, label = 0.0, "proxy_detected"
	}

	return scoreVal, true, fmt.Sprintf("%s (ratio %.2f)", label, ratio)
}
