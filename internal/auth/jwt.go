// Package auth implements the JWT-API-key validation pipeline from docs/auth.md.
//
// Stdlib-only — Ed25519 signature verification via crypto/ed25519, JWT
// parsing via base64 + JSON. No external dependencies, no third-party
// JWT library. The format is JWT spec compliant; any client SDK can
// generate keys we accept (Stripe-style).
//
// JWKS support (fetching the public key over HTTPS, key rotation) lands
// in a sibling file; this file is the pure verifier.
package auth

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Claims is the subset of JWT claims we use. Unknown claims are
// preserved in Extra for forward compatibility — never reject a token
// because of an unknown claim.
type Claims struct {
	Subject   string          `json:"sub"`
	Tier      string          `json:"tier"`
	RateLimit int             `json:"rate_limit"`
	Features  []string        `json:"features"`
	RegionPin string          `json:"region_pin,omitempty"`
	IssuedAt  int64           `json:"iat"`
	ExpiresAt int64           `json:"exp,omitempty"`
	Issuer    string          `json:"iss"`
	KeyID     string          `json:"kid,omitempty"`
	JTI       string          `json:"jti,omitempty"`
	Extra     map[string]any  `json:"-"`
}

// header is the JWT header. Only EdDSA is accepted — the algorithm
// confusion class of bug starts with accepting "alg":"none" or "alg":"HS256"
// when you expected RSA/Ed25519, so we lock it down at parse time.
type header struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
	Kid string `json:"kid,omitempty"`
}

// Sentinel errors.
var (
	ErrMalformed       = errors.New("auth: malformed JWT")
	ErrUnsupportedAlg  = errors.New("auth: unsupported signature algorithm (expected EdDSA)")
	ErrSignature       = errors.New("auth: signature verification failed")
	ErrExpired         = errors.New("auth: token expired")
	ErrUnknownKey      = errors.New("auth: unknown key id (kid)")
	ErrMissingKey      = errors.New("auth: token has no kid claim")
)

// KeyResolver maps a JWT's kid → public key. JWKS fetch implementations
// satisfy this interface; for tests a static map works fine.
type KeyResolver interface {
	Resolve(kid string) (ed25519.PublicKey, bool)
}

// StaticKeys is a simple in-memory KeyResolver. Useful for tests and for
// edge deployments that embed the public key set at build time.
type StaticKeys map[string]ed25519.PublicKey

func (s StaticKeys) Resolve(kid string) (ed25519.PublicKey, bool) {
	k, ok := s[kid]
	return k, ok
}

// VerifyOptions controls verification. Leave clock nil to use time.Now;
// inject a fake in tests for deterministic expiry checks.
type VerifyOptions struct {
	Keys      KeyResolver
	Clock     func() time.Time
	LeewaySec int64 // small clock-skew allowance
}

// Verify parses and verifies a JWT API key. Returns the validated claims
// or a sentinel error. Strips the "pk_live_" / "pk_test_" prefix if
// present (LayerID API key cosmetic prefix).
func Verify(token string, opts VerifyOptions) (*Claims, error) {
	if opts.Clock == nil {
		opts.Clock = time.Now
	}

	// Strip the LayerID prefix if present. Pure JWT inputs work too.
	if i := strings.Index(token, "_eyJ"); i > 0 {
		token = token[i+1:]
	}

	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, ErrMalformed
	}

	hdrBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("%w: header decode: %v", ErrMalformed, err)
	}
	var hdr header
	if err := json.Unmarshal(hdrBytes, &hdr); err != nil {
		return nil, fmt.Errorf("%w: header parse: %v", ErrMalformed, err)
	}
	if hdr.Alg != "EdDSA" {
		return nil, ErrUnsupportedAlg
	}
	if hdr.Kid == "" {
		return nil, ErrMissingKey
	}

	pubKey, ok := opts.Keys.Resolve(hdr.Kid)
	if !ok {
		return nil, ErrUnknownKey
	}

	signingInput := parts[0] + "." + parts[1]
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, fmt.Errorf("%w: signature decode: %v", ErrMalformed, err)
	}
	if !ed25519.Verify(pubKey, []byte(signingInput), sig) {
		return nil, ErrSignature
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("%w: payload decode: %v", ErrMalformed, err)
	}
	var claims Claims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("%w: payload parse: %v", ErrMalformed, err)
	}
	claims.KeyID = hdr.Kid

	if claims.ExpiresAt != 0 {
		now := opts.Clock().Unix()
		if now > claims.ExpiresAt+opts.LeewaySec {
			return nil, ErrExpired
		}
	}

	return &claims, nil
}
