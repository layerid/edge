// Package score defines the scoring engine's domain types and interfaces.
// These types are the source of truth — wire-format proto messages (in proto/)
// transcode to/from these, never the other way around.
package score

import (
	"errors"
	"time"
)

// Signals holds every observation we have about a request. Each field is
// optional — a Scorer must handle missing data gracefully (return
// ErrInsufficientData rather than inventing a fake value).
//
// New signals are added here by appending fields. Old fields are never
// removed in place — mark them deprecated, leave them populated for
// migration windows, then drop in a major version.
type Signals struct {
	// Identity --------------------------------------------------------
	ReqID    string
	TenantID int64
	IP       string // peer IP, post-trusted-proxy unwrap
	JA3      string // TLS JA3 hash; empty for non-TLS
	UA       string // raw user-agent string from HTTP request

	// Intel features (from intel-service) -----------------------------
	JA3Breadth        *JA3Breadth
	ResidentialProxy  *ResidentialProxy
	TCPFingerprint    string         // "iot_cctv" | "android_tv" | "real_macos" | ...
	DHTSightings      *DHTSightings
	IPReputation      *IPReputation
	GeoCountry        string         // ISO 3166-1 alpha-2
	GeoCity           string

	// Device signals (from browser SDK) -------------------------------
	Headless     *bool
	Webdriver    *bool
	STUNLeak     *STUNResult
	RTTRatio     *float64 // app_timing / tcp_rtt
	TZ           string   // browser-reported timezone
	Locale       string
	ScreenW      int32
	ScreenH      int32
	WindowW      int32
	WindowH      int32
	CanvasHash   string
	WebGLVendor  string
	AudioHash    string
	FontsCount   int32

	// Network-layer signals (from edge packet capture) ----------------
	TTL    int32
	IPID   int32
	MSS    int32
	WinSize int32

	// Timing ---------------------------------------------------------
	TCPRTTMs int32
	AppMs    int32

	// Metadata --------------------------------------------------------
	ObservedAt time.Time
}

type JA3Breadth struct {
	Label        string  // "narrow" | "moderate" | "wide"
	SNICount     int32   // distinct SNIs seen in last 7d
	TopSNIShare  float64 // 0..1
}

type ResidentialProxy struct {
	SeenIn     []string // ["iproyal", "webshare", ...]
	Confidence float64  // 0..1
	LastSeenAt time.Time
}

type DHTSightings struct {
	Count      int32
	Categories []string // ["software-crack", "game", ...]
}

type IPReputation struct {
	IsProxy     bool
	IsHosting   bool
	IsMobile    bool
	IsBlacklist bool
	ASN         int32
	ASNOrg      string
}

type STUNResult struct {
	Leaked   bool
	PublicIP string
	LocalIP  string
}

// Result is what a Scorer returns. Verdict is one of the constants below.
type Result struct {
	Score   float64 // [0.0, 1.0] — 1.0 = consistent with legit, 0.0 = abuse
	Verdict string  // VerdictAllow | VerdictStepUp | VerdictDeny | VerdictLog | VerdictInsufficient
	Model   string  // <Scorer.Name>@<Scorer.Version>
}

const (
	VerdictAllow        = "allow"
	VerdictStepUp       = "step-up"
	VerdictDeny         = "deny"
	VerdictLog          = "log"
	VerdictInsufficient = "insufficient_data"
)

// Explanation gives per-signal contributions for the dashboard /
// calibration page. Pure scorers can return one; opaque (ML / customer-
// hosted) scorers may return nil.
type Explanation struct {
	Signals []SignalContribution
	Weights map[string]float64
	Total   float64 // sum of (score * weight) for available signals
}

type SignalContribution struct {
	Name         string  // e.g. "ip_match"
	Available    bool    // false = this signal didn't fire (excluded from total)
	Score        float64 // [0.0, 1.0]
	Weight       float64
	Contribution float64 // Score * Weight
	Detail       string  // human-readable explanation
}

// ErrInsufficientData signals that too few signals fired for a meaningful
// verdict. Scorers return this to surface verdict: "insufficient_data" rather
// than invent a score from 2-of-14 available signals.
var ErrInsufficientData = errors.New("score: insufficient signals to compute verdict")

// Scorer is the strategy interface. Implementations live under internal/score/{name}/.
//
// Contract:
//   - Score is a pure function: same input → same output. No I/O.
//   - Same Name() + Version() pair MUST produce identical results across calls.
//   - Returning ErrInsufficientData is allowed; any other error is a bug.
//   - Explain may return nil (e.g. for opaque ML scorers).
type Scorer interface {
	Name() string
	Version() string
	Score(input Signals) (Result, error)
	Explain(input Signals) *Explanation
}
