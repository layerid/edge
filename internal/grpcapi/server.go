// Package grpcapi wires the scoring engine into the gRPC Edge service.
// It is the gRPC sibling of internal/api (the HTTP surface) and shares the
// same scorer Registry + store Repository instances.
//
// The servicer is intentionally thin: it transcodes the proto wire format
// to/from the domain types in internal/score, persists via internal/store,
// and delegates all scoring to a registered score.Scorer. No business
// logic lives here — see internal/score and internal/store for that.
package grpcapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/layerid/edge/internal/score"
	"github.com/layerid/edge/internal/store"
	edgev1 "github.com/layerid/edge/proto/edge/v1"
)

// Server implements edgev1.EdgeServer. It must embed
// UnimplementedEdgeServer for forward compatibility — when the proto adds
// an RPC, an un-updated server keeps compiling (the new method falls back
// to codes.Unimplemented instead of breaking the build).
type Server struct {
	edgev1.UnimplementedEdgeServer

	reg           *score.Registry
	defaultScorer string
	store         store.Repository
	region        string
	version       string
}

// Options configures the gRPC server. Mirrors internal/api.Options so the
// same dependency-injected instances (Registry, Store) can be reused for
// both surfaces — see cmd/layerid-edge/main.go.
type Options struct {
	Registry      *score.Registry
	Store         store.Repository
	DefaultScorer string
	Region        string
	Version       string
}

// New constructs the gRPC servicer.
func New(o Options) *Server {
	return &Server{
		reg:           o.Registry,
		defaultScorer: o.DefaultScorer,
		store:         o.Store,
		region:        o.Region,
		version:       o.Version,
	}
}

// Score scores a request and persists the lookup so a later Consume can
// race-safely retrieve it. Mirrors internal/api.handleInit: it assigns (or
// reuses) a req_id, scores the transcoded signals, and stores the result.
func (s *Server) Score(ctx context.Context, req *edgev1.ScoreRequest) (*edgev1.ScoreResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "nil ScoreRequest")
	}

	// Sidecar trust boundary: tenant is derived from the api_key parsed as
	// "tenant_<N>". This is a localhost-sidecar shortcut — the BFF and edge
	// run in the same pod, so we trust the api_key on the gRPC hop instead
	// of verifying a real JWT (the HTTP surface does that).
	// TODO(auth): replace with a signed sidecar token once the BFF mints one.
	tenantID := parseTenant(req.GetApiKey())

	sig := signalsFromProto(req)

	reqID := req.GetReqId()
	if reqID == "" {
		reqID = s.region + "-" + newToken()
	}
	sig.ReqID = reqID
	sig.TenantID = tenantID
	sig.ObservedAt = time.Now()

	scorer, err := s.reg.Get(s.defaultScorer)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	result, scoreErr := scorer.Score(sig)
	insufficient := errors.Is(scoreErr, score.ErrInsufficientData)
	if scoreErr != nil && !insufficient {
		return nil, status.Error(codes.Internal, "scorer error: "+scoreErr.Error())
	}

	lookup := store.Lookup{
		ReqID:      reqID,
		TenantID:   tenantID,
		Signals:    sig,
		Score:      result.Score,
		Verdict:    result.Verdict,
		Model:      result.Model,
		EdgeRegion: s.region,
		CreatedAt:  time.Now().UTC(),
	}
	stored, err := s.store.Init(ctx, lookup)
	if err != nil {
		return nil, status.Error(codes.Internal, "store init: "+err.Error())
	}

	resp := scoreResponseFromLookup(stored, req.GetShadow())
	// Per-signal breakdown from the SAME scorer that produced the score.
	// Explain may return nil (opaque scorers) -> leave explanation nil.
	resp.Explanation = toProtoExplanation(scorer.Explain(sig))
	return resp, nil
}

// Consume implements the one-time race-safe consume contract. It is
// idempotent on success: because ScoreResponse has no "replayed" field,
// both the first call and any replay return the stored ScoreResponse with
// an OK status — we never surface ALREADY_CONSUMED. ErrNotFound and
// ErrCrossTenant both map to codes.NotFound so req_id existence never leaks
// across tenant boundaries.
func (s *Server) Consume(ctx context.Context, req *edgev1.ConsumeRequest) (*edgev1.ScoreResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "nil ConsumeRequest")
	}
	reqID := req.GetReqId()
	if reqID == "" {
		return nil, status.Error(codes.InvalidArgument, "req_id is required")
	}

	// Same sidecar trust boundary as Score — see the comment there.
	tenantID := parseTenant(req.GetApiKey())

	result, err := s.store.Consume(ctx, reqID, tenantID, "grpc")
	switch {
	case errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrCrossTenant):
		return nil, status.Error(codes.NotFound, "lookup not found")
	case err != nil:
		return nil, status.Error(codes.Internal, "store consume: "+err.Error())
	}

	// result.Replayed is intentionally ignored: idempotent success for both
	// first consume and replay (proto has no replayed field). shadow is
	// always false here — ConsumeRequest carries no shadow flag, and we
	// don't persist the original Score-time shadow value on the Lookup.
	resp := scoreResponseFromLookup(result.Lookup, false)

	// The scorer is pure, so recompute the per-signal breakdown from the
	// persisted Signals rather than storing an explanation column. A missing
	// or opaque scorer simply leaves explanation nil.
	if scorer, err := s.reg.Get(s.defaultScorer); err == nil {
		resp.Explanation = toProtoExplanation(scorer.Explain(result.Lookup.Signals))
	}
	return resp, nil
}

// Health is the K8s liveness/readiness probe. upstream_ok is false and
// cache_size is 0 in this phase — intel-service is not wired yet, and the
// store is in-memory with no exposed cache counter.
func (s *Server) Health(_ context.Context, _ *edgev1.HealthRequest) (*edgev1.HealthResponse, error) {
	return &edgev1.HealthResponse{
		Ok:         true,
		Version:    s.version,
		UpstreamOk: false,
		CacheSize:  0,
	}, nil
}

// --- Transcoders -------------------------------------------------------

// signalsFromProto maps the proto ScoreRequest into the domain
// score.Signals. The domain type is the source of truth; the proto
// transcodes into it. The device submessage now carries window
// dimensions, app/transport timing, and the browser-reported IP, so the
// window_screen_ratio, timings, proxy_detection, and ip_match signals can
// all fire from a full payload.
func signalsFromProto(req *edgev1.ScoreRequest) score.Signals {
	sig := score.Signals{
		IP:  req.GetIp(),
		JA3: req.GetJa3(),
	}

	d := req.GetDevice()
	if d == nil {
		return sig
	}

	// Pointer-valued (tri-state) device fields — populated only when the
	// device submessage is present so "missing" stays distinguishable from
	// "false" downstream.
	headless := d.GetHeadless()
	sig.Headless = &headless
	webdriver := d.GetWebdriver()
	sig.Webdriver = &webdriver
	rttRatio := d.GetRttRatio()
	sig.RTTRatio = &rttRatio

	sig.TZ = d.GetTimezone()
	sig.Locale = d.GetLocale()
	sig.ScreenW = d.GetScreenW()
	sig.ScreenH = d.GetScreenH()
	sig.WindowW = d.GetWindowW()
	sig.WindowH = d.GetWindowH()
	sig.WebGLVendor = d.GetWebgl()
	sig.CanvasHash = d.GetCanvas()
	sig.AudioHash = d.GetAudio()
	sig.UA = d.GetUserAgentHash()
	sig.FontsCount = int32(len(d.GetFonts()))

	// Timing + transport signals feed timings and proxy_detection.
	sig.AppMs = d.GetAppTimingMs()
	sig.TCPRTTMs = d.GetTcpRttMs()
	// Browser-claimed public IP feeds ip_match (compared against sig.IP).
	sig.BrowserReportedIP = d.GetBrowserReportedIp()

	if st := d.GetStun(); st != nil {
		sig.STUNLeak = &score.STUNResult{
			Leaked:         st.GetLeaked(),
			PublicIP:       st.GetPublicIp(),
			LocalIP:        st.GetLocalIp(),
			WebRTCLocalIPs: st.GetWebrtcLocalIps(),
		}
	}

	return sig
}

// scoreResponseFromLookup builds the proto ScoreResponse from a stored
// Lookup. The Signals payload carries the normalised device-derived
// signals; the intel submessages (ja3_breadth, residential_proxy,
// dht_sightings, tcp_fingerprint) are left nil/empty until intel-service
// is wired in internal/upstream.
func scoreResponseFromLookup(l store.Lookup, shadow bool) *edgev1.ScoreResponse {
	return &edgev1.ScoreResponse{
		ReqId:   l.ReqID,
		Score:   l.Score,
		Verdict: l.Verdict,
		Signals: signalsPayloadFromDomain(l.Signals),
		Shadow:  shadow,
		Ts:      time.Now().Unix(),
	}
}

// signalsPayloadFromDomain transcodes the device-derived signals back out
// to the proto Signals message. Intel-derived submessages stay nil/empty.
func signalsPayloadFromDomain(sig score.Signals) *edgev1.Signals {
	out := &edgev1.Signals{}

	if sig.Headless != nil {
		out.Headless = *sig.Headless
	}
	if sig.Webdriver != nil {
		out.Webdriver = *sig.Webdriver
	}
	if sig.RTTRatio != nil {
		out.RttRatio = *sig.RTTRatio
	}
	// stun_leak surfaces the leaked public IP (empty string when not leaked
	// or no STUN data).
	if sig.STUNLeak != nil && sig.STUNLeak.Leaked {
		out.StunLeak = sig.STUNLeak.PublicIP
	}

	return out
}

// toProtoExplanation transcodes the domain *score.Explanation into the proto
// *edgev1.Explanation. A nil input (opaque/ML scorers that don't explain)
// yields a nil result, so resp.Explanation stays unset on the wire.
func toProtoExplanation(e *score.Explanation) *edgev1.Explanation {
	if e == nil {
		return nil
	}
	signals := make([]*edgev1.SignalContribution, 0, len(e.Signals))
	for _, c := range e.Signals {
		signals = append(signals, &edgev1.SignalContribution{
			Name:         c.Name,
			Available:    c.Available,
			Score:        c.Score,
			Weight:       c.Weight,
			Contribution: c.Contribution,
			Detail:       c.Detail,
		})
	}
	return &edgev1.Explanation{
		Signals: signals,
		Total:   e.Total,
	}
}

// --- Helpers -----------------------------------------------------------

// parseTenant extracts the numeric tenant id from an api_key shaped like
// "tenant_<N>". Anything empty or unparseable yields tenantID 0 (the
// sidecar trust boundary — see Score). This is the gRPC-side analogue of
// internal/api.parseTenantSub, which is unexported there.
func parseTenant(apiKey string) int64 {
	const prefix = "tenant_"
	if len(apiKey) <= len(prefix) || apiKey[:len(prefix)] != prefix {
		return 0
	}
	var n int64
	for _, c := range apiKey[len(prefix):] {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int64(c-'0')
	}
	return n
}

// newToken returns 16 hex chars of randomness — the same id idiom as
// internal/api.newToken, replicated here to keep the two surfaces
// independent (no cross-package coupling on an unexported helper).
func newToken() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
