package signals

import "github.com/layerid/edge/internal/score"

// IPMatch compares the JS-reported public IP against the network-observed
// peer IP. From legacy scoring.py (IpMatchSignal): binary 1.0 if they
// match, else 0.0. Unavailable if either side is missing.
//
// The legacy compares the JS canvas-collected IP (which may differ from
// the transport IP through trusted proxies) against the network-observed
// peer_ip. In edge, trusted-proxy unwrap happens before the scorer sees
// Signals.IP, so we compare Signals.IP against the browser-reported IP.
//
// IP equality uses net.ParseIP (via ipEqual) so IPv4/IPv6 forms normalise
// — the legacy did a raw string compare, but normalising is strictly more
// faithful to its "do these resolve to the same address" intent.
func IPMatch(s score.Signals) (sc float64, available bool, detail string) {
	if s.IP == "" || s.BrowserReportedIP == "" {
		return 0, false, "missing peer or browser-reported IP"
	}
	if ipEqual(s.IP, s.BrowserReportedIP) {
		return 1.0, true, "browser-reported IP matches peer IP"
	}
	return 0.0, true, "browser-reported IP differs from peer IP"
}
