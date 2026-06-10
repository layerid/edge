package signals

import (
	"net"

	"github.com/layerid/edge/internal/score"
)

// StunWebrtc cross-checks the STUN-discovered public IP and the WebRTC
// host-candidate IPs against the network-observed peer IP. It is the
// strongest single device signal for unmasking proxies: a residential
// proxy hides the transport IP but the browser's own WebRTC/STUN stack
// still reports the real exit, so a mismatch (or a conspicuously absent
// WebRTC stack) is high-signal.
//
// Ported verbatim from legacy fingerprints/ scoring.py
// (StunSignal.calculate), 7-case ladder preserved in order. Weight 0.25
// (HIGH).
//
// Inputs (mapped from edge Signals):
//   - network_ip      = s.IP
//   - stun_ip         = s.STUNLeak.PublicIP
//   - webrtc_ips      = s.STUNLeak.WebRTCLocalIPs
//   - stun_present    = STUNLeak != nil && PublicIP != ""
//   - network_blocked = false
//
// network_blocked is hardwired to false: the legacy derived it from
// `fp.has_js and not fp.has_network`, but edge has no has_network channel
// (the BFF always hands edge a transport peer IP). That collapses the
// "residential" sub-branches of cases 1 and 4 to their stun-blocked /
// disabled variants — faithful to the legacy code with network_blocked
// false, not a behaviour change. Revisit if a has_network signal lands.
//
// Available iff stun_present OR there is at least one WebRTC IP.
func StunWebrtc(s score.Signals) (sc float64, available bool, detail string) {
	var (
		stunIP    string
		webrtcIPs []string
	)
	if s.STUNLeak != nil {
		stunIP = s.STUNLeak.PublicIP
		webrtcIPs = s.STUNLeak.WebRTCLocalIPs
	}
	networkIP := s.IP
	stunPresent := s.STUNLeak != nil && s.STUNLeak.PublicIP != ""
	const networkBlocked = false

	hasWebrtcIPs := len(webrtcIPs) > 0
	if !stunPresent && !hasWebrtcIPs {
		return 0, false, "no stun or webrtc data"
	}

	webrtcMatchesNetwork := false
	if hasWebrtcIPs && networkIP != "" {
		for _, ip := range webrtcIPs {
			if ipEqual(ip, networkIP) {
				webrtcMatchesNetwork = true
				break
			}
		}
	}
	stunMatchesNetwork := stunPresent && stunIP != "" && networkIP != "" && ipEqual(stunIP, networkIP)
	stunMismatchesNetwork := stunPresent && stunIP != "" && networkIP != "" && !ipEqual(stunIP, networkIP)

	// Case 1: webrtcLocalIPs present but STUN missing (antidetect).
	if hasWebrtcIPs && !stunPresent {
		if networkBlocked {
			return 0.0, true, "antidetect_residential"
		}
		return 0.1, true, "antidetect_stun_blocked"
	}

	// Case 2: STUN IP mismatch.
	if stunMismatchesNetwork {
		return 0.0, true, "stun_ip_mismatch"
	}

	// Case 3: WebRTC IPs don't match network.
	if hasWebrtcIPs && networkIP != "" && !webrtcMatchesNetwork {
		if stunMatchesNetwork {
			return 0.2, true, "webrtc_spoofed"
		}
		return 0.1, true, "webrtc_ip_mismatch"
	}

	// Case 4: No WebRTC at all.
	if !hasWebrtcIPs && !stunPresent {
		if networkBlocked {
			return 0.1, true, "webrtc_disabled_residential"
		}
		return 0.3, true, "webrtc_disabled"
	}

	// Case 5: STUN matches but no webrtc IPs.
	if stunMatchesNetwork && !hasWebrtcIPs {
		return 0.7, true, "stun_match_no_webrtc"
	}

	// Case 6: Everything matches.
	if stunMatchesNetwork {
		if webrtcMatchesNetwork {
			return 1.0, true, "match"
		}
		return 0.9, true, "stun_match"
	}

	// Case 7: WebRTC matches but no STUN.
	if hasWebrtcIPs && webrtcMatchesNetwork && !stunPresent {
		return 0.5, true, "webrtc_match_no_stun"
	}

	return 0.5, true, "partial"
}

// ipEqual compares two IP strings for equality using net.ParseIP so IPv4 /
// IPv6 representations normalise (e.g. "::ffff:1.2.3.4" == "1.2.3.4").
// Falls back to a string compare when either side fails to parse.
func ipEqual(a, b string) bool {
	pa, pb := net.ParseIP(a), net.ParseIP(b)
	if pa == nil || pb == nil {
		return a == b
	}
	return pa.Equal(pb)
}
