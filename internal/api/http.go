// Package api wires the scoring engine into HTTP endpoints. JSON wire
// format for now; gRPC stubs will be added when protoc tooling lands.
// The handlers are intentionally thin — all real work happens in
// internal/score/.
package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/layerid/edge/internal/score"
)

// Server is the HTTP surface. Constructed with a Registry and a default
// scorer name; per-request scorer selection (via tenant config) is layered
// on top in the BFF.
type Server struct {
	reg            *score.Registry
	defaultScorer  string
	healthVersion  string
}

func New(reg *score.Registry, defaultScorer, version string) *Server {
	return &Server{
		reg:           reg,
		defaultScorer: defaultScorer,
		healthVersion: version,
	}
}

// Routes returns the http.ServeMux with all endpoints wired.
func (s *Server) Routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /v1/scorers", s.handleListScorers)
	mux.HandleFunc("POST /v1/score", s.handleScore)
	return mux
}

// --- Handlers ----------------------------------------------------------

type healthResp struct {
	OK      bool   `json:"ok"`
	Version string `json:"version"`
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, healthResp{OK: true, Version: s.healthVersion})
}

type listScorersResp struct {
	Default string   `json:"default"`
	Names   []string `json:"names"`
}

func (s *Server) handleListScorers(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, listScorersResp{
		Default: s.defaultScorer,
		Names:   s.reg.Names(),
	})
}

type scoreReq struct {
	Scorer  string         `json:"scorer,omitempty"` // override; empty = default
	Signals score.Signals  `json:"signals"`
}

type scoreResp struct {
	Score   float64             `json:"score"`
	Verdict string              `json:"verdict"`
	Model   string              `json:"model"`
	Explain *score.Explanation  `json:"explain,omitempty"`
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

	result, err := scorer.Score(req.Signals)
	if err != nil && !errors.Is(err, score.ErrInsufficientData) {
		writeError(w, http.StatusInternalServerError, "scorer error: "+err.Error())
		return
	}
	// ErrInsufficientData is expected — the Result still carries
	// verdict:"insufficient_data", just send it back normally.

	writeJSON(w, http.StatusOK, scoreResp{
		Score:   result.Score,
		Verdict: result.Verdict,
		Model:   result.Model,
		Explain: scorer.Explain(req.Signals),
	})
}

// --- Helpers -----------------------------------------------------------

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
