// Package api wires the scoring engine into HTTP endpoints. JSON wire
// format for now; gRPC stubs will be added when protoc tooling lands.
// The handlers are intentionally thin — all real work happens in
// internal/score/ and internal/store/.
package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/layerid/edge/internal/auth"
	"github.com/layerid/edge/internal/score"
	"github.com/layerid/edge/internal/store"
)

// Server is the HTTP surface.
type Server struct {
	reg            *score.Registry
	defaultScorer  string
	store          store.Repository
	region         string
	healthVersion  string
	authMiddleware func(http.Handler) http.Handler
}

// Options configures the server. AuthMiddleware can be nil — useful for
// local dev / tests. In production it should be set to the JWT verifier
// from internal/auth/.
type Options struct {
	Registry       *score.Registry
	Store          store.Repository
	DefaultScorer  string
	Region         string
	Version        string
	AuthMiddleware func(http.Handler) http.Handler
}

func New(o Options) *Server {
	mw := o.AuthMiddleware
	if mw == nil {
		// Passthrough: useful for tests / local dev when auth isn't wired.
		mw = func(next http.Handler) http.Handler { return next }
	}
	return &Server{
		reg:            o.Registry,
		defaultScorer:  o.DefaultScorer,
		store:          o.Store,
		region:         o.Region,
		healthVersion:  o.Version,
		authMiddleware: mw,
	}
}

// Routes returns the http.ServeMux with all endpoints wired.
//
// /healthz and /v1/scorers are unauthenticated; /v1/* (init, score,
// consume) require a valid Bearer token via the auth middleware.
func (s *Server) Routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /v1/scorers", s.handleListScorers)

	authedMux := http.NewServeMux()
	authedMux.HandleFunc("POST /v1/init", s.handleInit)
	authedMux.HandleFunc("POST /v1/score", s.handleScore)
	authedMux.HandleFunc("GET /v1/lookup/{req_id}", s.handleLookup)
	authedMux.HandleFunc("POST /v1/consume/{req_id}", s.handleConsume)
	mux.Handle("/v1/init", s.authMiddleware(authedMux))
	mux.Handle("/v1/score", s.authMiddleware(authedMux))
	mux.Handle("/v1/lookup/{req_id}", s.authMiddleware(authedMux))
	mux.Handle("/v1/consume/{req_id}", s.authMiddleware(authedMux))
	return mux
}

// --- Handlers ----------------------------------------------------------

type healthResp struct {
	OK      bool   `json:"ok"`
	Version string `json:"version"`
	Region  string `json:"region,omitempty"`
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, healthResp{OK: true, Version: s.healthVersion, Region: s.region})
}

type listScorersResp struct {
	Default string   `json:"default"`
	Names   []string `json:"names"`
}

func (s *Server) handleListScorers(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, listScorersResp{
		Default: s.defaultScorer,
		Names:   s.reg.Names(),
	})
}

// --- /v1/init -----------------------------------------------------------

type initReq struct {
	Signals score.Signals `json:"signals"`
}

type initResp struct {
	ReqID       string  `json:"req_id"`
	Score       float64 `json:"score"`
	Verdict     string  `json:"verdict"`
	Model       string  `json:"model"`
	Insufficient bool   `json:"insufficient_data,omitempty"`
}

// handleInit assigns a fresh req_id, scores the supplied signals
// immediately, and stores everything. The client then has a stable
// identifier to call /v1/score or /v1/consume against later.
func (s *Server) handleInit(w http.ResponseWriter, r *http.Request) {
	claims, ok := auth.FromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "no claims in context")
		return
	}
	tenantID, err := parseTenantSub(claims.Subject)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}

	var body initReq
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	scorer, err := s.reg.Get(s.defaultScorer)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	result, scoreErr := scorer.Score(body.Signals)
	insufficient := errors.Is(scoreErr, score.ErrInsufficientData)
	if scoreErr != nil && !insufficient {
		writeError(w, http.StatusInternalServerError, "scorer error: "+scoreErr.Error())
		return
	}

	reqID := s.region + "-" + newToken()
	lookup := store.Lookup{
		ReqID:      reqID,
		TenantID:   tenantID,
		Signals:    body.Signals,
		Score:      result.Score,
		Verdict:    result.Verdict,
		Model:      result.Model,
		EdgeRegion: s.region,
		CreatedAt:  time.Now().UTC(),
	}
	if _, err := s.store.Init(r.Context(), lookup); err != nil {
		writeError(w, http.StatusInternalServerError, "store init: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, initResp{
		ReqID:        reqID,
		Score:        result.Score,
		Verdict:      result.Verdict,
		Model:        result.Model,
		Insufficient: insufficient,
	})
}

// --- /v1/score (ad-hoc, no persistence) ---------------------------------

type scoreReq struct {
	Scorer  string        `json:"scorer,omitempty"` // override; empty = default
	Signals score.Signals `json:"signals"`
}

type scoreResp struct {
	Score   float64            `json:"score"`
	Verdict string             `json:"verdict"`
	Model   string             `json:"model"`
	Explain *score.Explanation `json:"explain,omitempty"`
}

func (s *Server) handleScore(w http.ResponseWriter, r *http.Request) {
	var req scoreReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	scorerName := req.Scorer
	if scorerName == "" {
		scorerName = s.defaultScorer
	}
	scorer, err := s.reg.Get(scorerName)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	result, scoreErr := scorer.Score(req.Signals)
	if scoreErr != nil && !errors.Is(scoreErr, score.ErrInsufficientData) {
		writeError(w, http.StatusInternalServerError, "scorer error: "+scoreErr.Error())
		return
	}
	writeJSON(w, http.StatusOK, scoreResp{
		Score:   result.Score,
		Verdict: result.Verdict,
		Model:   result.Model,
		Explain: scorer.Explain(req.Signals),
	})
}

// --- /v1/lookup/{req_id} (read-only) -----------------------------------

type lookupResp struct {
	ReqID      string    `json:"req_id"`
	Score      float64   `json:"score"`
	Verdict    string    `json:"verdict"`
	Model      string    `json:"model"`
	CreatedAt  time.Time `json:"created_at"`
	ConsumedAt *time.Time `json:"consumed_at,omitempty"`
}

func (s *Server) handleLookup(w http.ResponseWriter, r *http.Request) {
	claims, ok := auth.FromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "no claims in context")
		return
	}
	tenantID, err := parseTenantSub(claims.Subject)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	reqID := r.PathValue("req_id")
	l, err := s.store.Get(r.Context(), reqID, tenantID)
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrCrossTenant) {
		writeError(w, http.StatusNotFound, "lookup not found")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "store get: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, lookupResp{
		ReqID:      l.ReqID,
		Score:      l.Score,
		Verdict:    l.Verdict,
		Model:      l.Model,
		CreatedAt:  l.CreatedAt,
		ConsumedAt: l.ConsumedAt,
	})
}

// --- /v1/consume/{req_id} -----------------------------------------------

type consumeResp struct {
	ReqID    string    `json:"req_id"`
	Score    float64   `json:"score"`
	Verdict  string    `json:"verdict"`
	Model    string    `json:"model"`
	Replayed bool      `json:"replayed"`
	ConsumedAt time.Time `json:"consumed_at"`
}

func (s *Server) handleConsume(w http.ResponseWriter, r *http.Request) {
	claims, ok := auth.FromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "no claims in context")
		return
	}
	tenantID, err := parseTenantSub(claims.Subject)
	if err != nil {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	reqID := r.PathValue("req_id")

	result, err := s.store.Consume(r.Context(), reqID, tenantID, r.RemoteAddr)
	switch {
	case errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrCrossTenant):
		writeError(w, http.StatusNotFound, "lookup not found")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "store consume: "+err.Error())
		return
	}

	consumedAt := time.Time{}
	if result.Lookup.ConsumedAt != nil {
		consumedAt = *result.Lookup.ConsumedAt
	}
	writeJSON(w, http.StatusOK, consumeResp{
		ReqID:      result.Lookup.ReqID,
		Score:      result.Lookup.Score,
		Verdict:    result.Lookup.Verdict,
		Model:      result.Lookup.Model,
		Replayed:   result.Replayed,
		ConsumedAt: consumedAt,
	})
}

// --- Helpers -----------------------------------------------------------

// parseTenantSub extracts the numeric tenant id from a JWT subject like
// "tenant_42". Returns an error if the format doesn't match.
func parseTenantSub(sub string) (int64, error) {
	const prefix = "tenant_"
	if len(sub) <= len(prefix) || sub[:len(prefix)] != prefix {
		return 0, errors.New("subject must look like tenant_<id>")
	}
	var n int64
	for _, c := range sub[len(prefix):] {
		if c < '0' || c > '9' {
			return 0, errors.New("subject tenant id must be numeric")
		}
		n = n*10 + int64(c-'0')
	}
	return n, nil
}

// newToken returns 16 hex chars of randomness — short enough for URL
// readability, long enough that req_id collisions are impossible.
func newToken() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

type errorResp struct {
	Error string `json:"error"`
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorResp{Error: msg})
}
