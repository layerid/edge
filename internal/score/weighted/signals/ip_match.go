package signals

import "github.com/layerid/edge/internal/score"

// IPMatch is the canonical reference signal — kept tiny so it's a clear
// template for the other 13 ports. From legacy scoring.py: binary 1.0 if
// the JS-reported IP matches the network-observed peer IP, else 0.0.
// Unavailable if either side is missing.
//
// The legacy compares JS canvas-collected IP (which may differ from
// transport IP through trusted proxies) against the network-observed
// peer_ip. In v2, edge already does trusted-proxy unwrap before the
// scorer sees Signals.IP — so we compare Signals.IP against whatever the
// browser SDK reported.
//
// Real port note: legacy uses lowercase + IPv4/IPv6 normalisation; this
// stub trusts the upstream normalisation. Add net.ParseIP equality when
// the browser-IP field lands in Signals.
func IPMatch(s score.Signals) (sc float64, available bool, detail string) {
	if s.IP == "" {
		return 0, false, "no peer IP"
	}
	// TODO(port): browser-reported IP isn't in Signals yet. When ported,
	// compare against s.BrowserReportedIP and return 1.0 / 0.0.
	return 1.0, false, "browser-reported IP not collected yet"
}
