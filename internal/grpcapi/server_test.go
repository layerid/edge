package grpcapi_test

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/layerid/edge/internal/grpcapi"
	"github.com/layerid/edge/internal/score"
	"github.com/layerid/edge/internal/score/constscorer"
	"github.com/layerid/edge/internal/score/weighted"
	"github.com/layerid/edge/internal/store/memory"
	edgev1 "github.com/layerid/edge/proto/edge/v1"
)

// fullDevice returns a ScoreRequest with every proto device field populated
// — the "full device payload" the contract asks the test to score. The
// transport peer IP (Ip), the STUN public IP, the WebRTC host candidate,
// and browser_reported_ip all agree on 203.0.113.7 so ip_match and
// stun_webrtc both fire as a clean human (full STUN/WebRTC match).
func fullDevice(apiKey, reqID string) *edgev1.ScoreRequest {
	return &edgev1.ScoreRequest{
		Ip:     "203.0.113.7",
		Ja3:    "e7d705a3286e19ea42f587b344ee6865",
		ApiKey: apiKey,
		ReqId:  reqID,
		Device: &edgev1.DeviceSignals{
			Canvas:        "canvas-abc123",
			Webgl:         "Apple Inc.",
			Audio:         "audio-def456",
			Fonts:         []string{"Arial", "Helvetica", "Courier", "Times"},
			Headless:      false,
			Webdriver:     false,
			UserAgentHash: "ua-789",
			Stun: &edgev1.StunResult{
				Leaked:         true,
				PublicIp:       "203.0.113.7",
				LocalIp:        "192.168.1.5",
				WebrtcLocalIps: []string{"203.0.113.7"},
			},
			Timezone:          "Europe/Berlin",
			Locale:            "en-US",
			ScreenW:           1920,
			ScreenH:           1080,
			WindowW:           1600,
			WindowH:           900,
			RttRatio:          1.1,
			DeviceId:          "device-xyz",
			AppTimingMs:       40,
			TcpRttMs:          20,
			BrowserReportedIp: "203.0.113.7",
		},
	}
}

// newTestServer registers the given scorer under "weighted" and returns a
// gRPC client wired over an in-process bufconn pipe plus the shared store
// (so persistence can be asserted independently of the wire).
func newTestServer(t *testing.T, scorer score.Scorer) (edgev1.EdgeClient, *memory.Repository) {
	t.Helper()

	reg := score.NewRegistry()
	if err := reg.Register(scorer); err != nil {
		t.Fatalf("register scorer: %v", err)
	}
	st := memory.New(time.Now)

	srv := grpc.NewServer()
	edgev1.RegisterEdgeServer(srv, grpcapi.New(grpcapi.Options{
		Registry:      reg,
		Store:         st,
		DefaultScorer: scorer.Name(),
		Region:        "test",
		Version:       "test-1",
	}))

	lis := bufconn.Listen(1 << 20)
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)

	dialer := func(context.Context, string) (net.Conn, error) { return lis.DialContext(context.Background()) }
	conn, err := grpc.NewClient(
		"passthrough:///bufnet",
		grpc.WithContextDialer(dialer),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dial bufnet: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	return edgev1.NewEdgeClient(conn), st
}

// TestScore_PersistsAndRoundTripsThroughConsume drives the primary
// vertical-slice flow: Score a full device payload, confirm it persists,
// then Consume the returned req_id and confirm the stored response comes
// back. A const-allow scorer registered as "weighted" gives a deterministic
// non-insufficient verdict so the persist + consume contract is exercised
// independently of the (separately tested) weighted signal coverage.
func TestScore_PersistsAndRoundTripsThroughConsume(t *testing.T) {
	client, st := newTestServer(t, constAllowNamed("weighted"))
	ctx := context.Background()

	resp, err := client.Score(ctx, fullDevice("tenant_42", ""))
	if err != nil {
		t.Fatalf("Score: %v", err)
	}
	if resp.GetReqId() == "" {
		t.Fatal("Score returned empty req_id; expected a generated one")
	}
	if resp.GetVerdict() == score.VerdictInsufficient {
		t.Fatalf("verdict insufficient_data; expected a real verdict, got %q", resp.GetVerdict())
	}
	if resp.GetVerdict() != score.VerdictAllow {
		t.Fatalf("verdict = %q, want %q", resp.GetVerdict(), score.VerdictAllow)
	}
	// Device-derived signals must round-trip into the Signals payload.
	if got := resp.GetSignals().GetStunLeak(); got != "203.0.113.7" {
		t.Errorf("stun_leak = %q, want leaked public IP", got)
	}
	if got := resp.GetSignals().GetRttRatio(); got != 1.1 {
		t.Errorf("rtt_ratio = %v, want 1.1", got)
	}

	// Persisted under (req_id, tenant 42)?
	if _, err := st.Get(ctx, resp.GetReqId(), 42); err != nil {
		t.Fatalf("expected lookup persisted for tenant 42: %v", err)
	}

	// Consume returns the stored response (first call).
	consumed, err := client.Consume(ctx, &edgev1.ConsumeRequest{ReqId: resp.GetReqId(), ApiKey: "tenant_42"})
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if consumed.GetReqId() != resp.GetReqId() {
		t.Errorf("consume req_id = %q, want %q", consumed.GetReqId(), resp.GetReqId())
	}
	if consumed.GetVerdict() != resp.GetVerdict() {
		t.Errorf("consume verdict = %q, want %q", consumed.GetVerdict(), resp.GetVerdict())
	}

	// Replay is idempotent success (OK, NOT ALREADY_CONSUMED).
	replay, err := client.Consume(ctx, &edgev1.ConsumeRequest{ReqId: resp.GetReqId(), ApiKey: "tenant_42"})
	if err != nil {
		t.Fatalf("Consume replay should be idempotent success, got: %v", err)
	}
	if replay.GetReqId() != resp.GetReqId() {
		t.Errorf("replay req_id = %q, want %q", replay.GetReqId(), resp.GetReqId())
	}
}

// TestConsume_CrossTenant verifies a Consume from a different tenant maps to
// codes.NotFound (never ALREADY_CONSUMED, never leaks existence).
func TestConsume_CrossTenant(t *testing.T) {
	client, _ := newTestServer(t, constAllowNamed("weighted"))
	ctx := context.Background()

	resp, err := client.Score(ctx, fullDevice("tenant_42", ""))
	if err != nil {
		t.Fatalf("Score: %v", err)
	}

	// Tenant 7 tries to consume tenant 42's req_id.
	_, err = client.Consume(ctx, &edgev1.ConsumeRequest{ReqId: resp.GetReqId(), ApiKey: "tenant_7"})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("cross-tenant Consume code = %v, want NotFound", status.Code(err))
	}
}

// TestConsume_UnknownReqID -> NotFound.
func TestConsume_UnknownReqID(t *testing.T) {
	client, _ := newTestServer(t, constAllowNamed("weighted"))
	ctx := context.Background()

	_, err := client.Consume(ctx, &edgev1.ConsumeRequest{ReqId: "test-deadbeef", ApiKey: "tenant_1"})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("unknown req_id Consume code = %v, want NotFound", status.Code(err))
	}
}

// TestScore_GeneratesReqIDWhenEmpty confirms an empty req_id is filled with
// a region-prefixed token.
func TestScore_GeneratesReqIDWhenEmpty(t *testing.T) {
	client, _ := newTestServer(t, constAllowNamed("weighted"))
	resp, err := client.Score(context.Background(), fullDevice("tenant_1", ""))
	if err != nil {
		t.Fatalf("Score: %v", err)
	}
	if len(resp.GetReqId()) < len("test-")+1 || resp.GetReqId()[:5] != "test-" {
		t.Errorf("generated req_id = %q, want region-prefixed (test-...)", resp.GetReqId())
	}
}

// TestScore_PreservesSuppliedReqID confirms a caller-supplied req_id is
// honoured.
func TestScore_PreservesSuppliedReqID(t *testing.T) {
	client, _ := newTestServer(t, constAllowNamed("weighted"))
	resp, err := client.Score(context.Background(), fullDevice("tenant_1", "caller-supplied-id"))
	if err != nil {
		t.Fatalf("Score: %v", err)
	}
	if resp.GetReqId() != "caller-supplied-id" {
		t.Errorf("req_id = %q, want caller-supplied-id", resp.GetReqId())
	}
}

// TestHealth checks the Health RPC contract.
func TestHealth(t *testing.T) {
	client, _ := newTestServer(t, constAllowNamed("weighted"))
	resp, err := client.Health(context.Background(), &edgev1.HealthRequest{})
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if !resp.GetOk() {
		t.Error("Health.ok = false, want true")
	}
	if resp.GetVersion() != "test-1" {
		t.Errorf("Health.version = %q, want test-1", resp.GetVersion())
	}
	if resp.GetUpstreamOk() {
		t.Error("Health.upstream_ok = true, want false (intel-service not wired)")
	}
	if resp.GetCacheSize() != 0 {
		t.Errorf("Health.cache_size = %d, want 0", resp.GetCacheSize())
	}
}

// TestScore_TenantZeroOnUnparseableKey confirms the sidecar trust-boundary
// fallback: an api_key that isn't "tenant_<N>" yields tenant 0.
func TestScore_TenantZeroOnUnparseableKey(t *testing.T) {
	client, st := newTestServer(t, constAllowNamed("weighted"))
	ctx := context.Background()

	resp, err := client.Score(ctx, fullDevice("not-a-tenant-key", ""))
	if err != nil {
		t.Fatalf("Score: %v", err)
	}
	if _, err := st.Get(ctx, resp.GetReqId(), 0); err != nil {
		t.Fatalf("expected lookup persisted for tenant 0: %v", err)
	}
}

// TestWeightedSignalCoverage_FromProtoPayload pins the device-only signal
// coverage for a full proto payload through the PRODUCTION weighted
// registry. After the legacy device-tier port, a full payload fires nine
// device-only signals — well past the minSignalsRequired=6 quorum — and
// yields a real (non-insufficient) verdict. The five remaining stubs
// (ua_match, os_match, headers_order, residential_proxy, ip_reputation)
// stay unavailable: they need raw-UA PII or the intel upstream, both out of
// scope for the device-only tier.
func TestWeightedSignalCoverage_FromProtoPayload(t *testing.T) {
	// Production registry — the real DefaultRegistry the binary ships with.
	scorer := weighted.New()

	// Build the same Signals the servicer builds from a full proto payload.
	headless, webdriver, rtt := false, false, 1.1
	sig := score.Signals{
		IP:                "203.0.113.7",
		BrowserReportedIP: "203.0.113.7",
		JA3:               "e7d705a3286e19ea42f587b344ee6865",
		Headless:          &headless,
		Webdriver:         &webdriver,
		RTTRatio:          &rtt,
		TZ:                "Europe/Berlin",
		Locale:            "en-US",
		ScreenW:           1920,
		ScreenH:           1080,
		WindowW:           1600,
		WindowH:           900,
		WebGLVendor:       "Apple Inc.",
		CanvasHash:        "canvas-abc123",
		AudioHash:         "audio-def456",
		FontsCount:        4,
		AppMs:             40,
		TCPRTTMs:          20,
		STUNLeak: &score.STUNResult{
			Leaked:         true,
			PublicIP:       "203.0.113.7",
			LocalIP:        "192.168.1.5",
			WebRTCLocalIPs: []string{"203.0.113.7"},
		},
	}

	expl := scorer.Explain(sig)
	fired := map[string]bool{}
	for _, c := range expl.Signals {
		if c.Available {
			fired[c.Name] = true
		}
	}
	// Every device-only signal must fire for a full payload.
	want := []string{
		"ip_match", "timings", "screen_resolution", "window_screen_ratio",
		"webdriver", "timezone", "webgl_renderer", "proxy_detection", "stun_webrtc",
	}
	for _, name := range want {
		if !fired[name] {
			t.Errorf("expected signal %q to fire from a full proto payload", name)
		}
	}
	if len(fired) < 6 {
		t.Fatalf("fired signals = %d (%v), want >= 6 (quorum)", len(fired), keys(fired))
	}
	// The deferred stubs must stay unavailable.
	for _, name := range []string{"ua_match", "os_match", "headers_order", "residential_proxy", "ip_reputation"} {
		if fired[name] {
			t.Errorf("stub signal %q fired but should be deferred (notImplemented)", name)
		}
	}

	// The production weighted scorer clears the 6-signal quorum and returns a
	// real, non-insufficient verdict. This clean payload scores ~1.0 → allow.
	res, err := scorer.Score(sig)
	if err != nil {
		t.Fatalf("Score: unexpected error: %v", err)
	}
	if res.Verdict == score.VerdictInsufficient {
		t.Fatalf("verdict = insufficient_data; expected a real verdict (>=6 signals fired)")
	}
	if res.Verdict != score.VerdictAllow {
		t.Fatalf("verdict = %q, want allow (clean full payload scores ~1.0)", res.Verdict)
	}

	// End-to-end through the servicer with the production scorer: the gRPC
	// Score must surface the same real verdict and persist the lookup.
	client, st := newTestServer(t, weighted.New())
	resp, err := client.Score(context.Background(), fullDevice("tenant_5", ""))
	if err != nil {
		t.Fatalf("Score over gRPC: %v", err)
	}
	if resp.GetVerdict() == score.VerdictInsufficient {
		t.Fatalf("gRPC verdict = insufficient_data; expected a real verdict")
	}
	if resp.GetVerdict() != score.VerdictAllow {
		t.Fatalf("gRPC verdict = %q, want allow", resp.GetVerdict())
	}
	if _, err := st.Get(context.Background(), resp.GetReqId(), 5); err != nil {
		t.Fatalf("score must persist: %v", err)
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// constAllowNamed returns a const-allow scorer renamed so it can be
// registered under an arbitrary name (the registry keys on Name()).
func constAllowNamed(name string) score.Scorer {
	return constscorer.New(name, "test", score.Result{
		Score:   1.0,
		Verdict: score.VerdictAllow,
	})
}
