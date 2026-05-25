package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

// ctxKey is the unexported type used for context keys to avoid collisions.
type ctxKey int

const (
	claimsKey ctxKey = iota
)

// FromContext returns the Claims attached by Middleware. Handlers behind
// the auth middleware can rely on this being non-nil; handlers in front
// of it get (nil, false).
func FromContext(ctx context.Context) (*Claims, bool) {
	c, ok := ctx.Value(claimsKey).(*Claims)
	return c, ok
}

// Middleware verifies the Authorization: Bearer header and stashes the
// claims in the request context. Failed verification returns 401 with a
// JSON error body matching the rest of internal/api/.
func Middleware(opts VerifyOptions) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tok := bearerToken(r)
			if tok == "" {
				writeUnauthorized(w, "missing Authorization: Bearer header")
				return
			}
			claims, err := Verify(tok, opts)
			if err != nil {
				writeUnauthorized(w, classify(err))
				return
			}
			ctx := context.WithValue(r.Context(), claimsKey, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if h == "" {
		return ""
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(h, prefix) {
		return ""
	}
	return strings.TrimSpace(h[len(prefix):])
}

func classify(err error) string {
	switch {
	case errors.Is(err, ErrExpired):
		return "token expired"
	case errors.Is(err, ErrSignature):
		return "invalid signature"
	case errors.Is(err, ErrUnknownKey):
		return "unknown signing key"
	case errors.Is(err, ErrUnsupportedAlg):
		return "unsupported signature algorithm"
	case errors.Is(err, ErrMissingKey):
		return "missing kid claim"
	case errors.Is(err, ErrMalformed):
		return "malformed token"
	default:
		return "invalid token"
	}
}

func writeUnauthorized(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("WWW-Authenticate", `Bearer realm="layerid-edge"`)
	w.WriteHeader(http.StatusUnauthorized)
	// Hand-encoded to avoid pulling encoding/json into this file.
	_, _ = w.Write([]byte(`{"error":` + jsonQuote(msg) + `}`))
}

func jsonQuote(s string) string {
	b := make([]byte, 0, len(s)+2)
	b = append(b, '"')
	for _, r := range s {
		switch r {
		case '"', '\\':
			b = append(b, '\\', byte(r))
		case '\n':
			b = append(b, '\\', 'n')
		case '\r':
			b = append(b, '\\', 'r')
		case '\t':
			b = append(b, '\\', 't')
		default:
			if r < 0x20 {
				continue
			}
			b = append(b, []byte(string(r))...)
		}
	}
	b = append(b, '"')
	return string(b)
}
