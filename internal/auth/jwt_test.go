package auth_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/layerid/edge/internal/auth"
)

const testKid = "test-key-1"

// mintToken signs a JWT with the given claims using the supplied private
// key. Mirrors what auth.layerid.io's issuer will do; lives here as a
// test helper so we don't accidentally depend on it from production code.
func mintToken(t *testing.T, priv ed25519.PrivateKey, claims map[string]any) string {
	t.Helper()
	hdr := map[string]any{"alg": "EdDSA", "typ": "JWT", "kid": testKid}
	enc := func(v any) string {
		b, _ := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(b)
	}
	signingInput := enc(hdr) + "." + enc(claims)
	sig := ed25519.Sign(priv, []byte(signingInput))
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func testOpts(t *testing.T, pub ed25519.PublicKey, now time.Time) auth.VerifyOptions {
	t.Helper()
	return auth.VerifyOptions{
		Keys:      auth.StaticKeys{testKid: pub},
		Clock:     func() time.Time { return now },
		LeewaySec: 2,
	}
}

func TestVerifyGoodToken(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	tok := mintToken(t, priv, map[string]any{
		"sub":  "tenant_abc",
		"tier": "standard",
		"iat":  time.Now().Unix(),
	})
	claims, err := auth.Verify(tok, testOpts(t, pub, time.Now()))
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if claims.Subject != "tenant_abc" {
		t.Errorf("subject: got %q, want tenant_abc", claims.Subject)
	}
	if claims.Tier != "standard" {
		t.Errorf("tier: got %q, want standard", claims.Tier)
	}
	if claims.KeyID != testKid {
		t.Errorf("kid: got %q, want %q", claims.KeyID, testKid)
	}
}

func TestVerifyBadSignature(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	tok := mintToken(t, priv, map[string]any{"sub": "tenant_abc", "iat": time.Now().Unix()})
	// Sign with one key, verify with a different one.
	otherPub, _, _ := ed25519.GenerateKey(rand.Reader)
	_ = pub
	_, err := auth.Verify(tok, testOpts(t, otherPub, time.Now()))
	if !errors.Is(err, auth.ErrSignature) {
		t.Errorf("got %v, want ErrSignature", err)
	}
}

func TestVerifyExpired(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	tok := mintToken(t, priv, map[string]any{
		"sub": "tenant_abc",
		"iat": time.Now().Add(-2 * time.Hour).Unix(),
		"exp": time.Now().Add(-1 * time.Hour).Unix(),
	})
	_, err := auth.Verify(tok, testOpts(t, pub, time.Now()))
	if !errors.Is(err, auth.ErrExpired) {
		t.Errorf("got %v, want ErrExpired", err)
	}
}

func TestVerifyUnknownKid(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	tok := mintToken(t, priv, map[string]any{"sub": "x", "iat": time.Now().Unix()})
	opts := auth.VerifyOptions{
		Keys:  auth.StaticKeys{}, // empty — kid won't resolve
		Clock: time.Now,
	}
	_, err := auth.Verify(tok, opts)
	if !errors.Is(err, auth.ErrUnknownKey) {
		t.Errorf("got %v, want ErrUnknownKey", err)
	}
}

func TestVerifyMalformed(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	cases := []string{
		"not.a.jwt.too.many.parts",
		"only-two.parts",
		"!@#$.bad.base64!@#",
	}
	for _, c := range cases {
		_, err := auth.Verify(c, testOpts(t, pub, time.Now()))
		if !errors.Is(err, auth.ErrMalformed) {
			t.Errorf("%q: got %v, want ErrMalformed", c, err)
		}
	}
}

func TestVerifyRejectsNoneAlg(t *testing.T) {
	// The "alg":"none" attack — server accepts unsigned token if it
	// doesn't pin the algorithm. Our parser must reject anything other
	// than EdDSA explicitly.
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	hdr := `{"alg":"none","typ":"JWT","kid":"` + testKid + `"}`
	payload := `{"sub":"attacker","iat":` + intStr(time.Now().Unix()) + `}`
	encHdr := base64.RawURLEncoding.EncodeToString([]byte(hdr))
	encPayload := base64.RawURLEncoding.EncodeToString([]byte(payload))
	tok := encHdr + "." + encPayload + "." // empty signature
	_, err := auth.Verify(tok, testOpts(t, pub, time.Now()))
	if !errors.Is(err, auth.ErrUnsupportedAlg) {
		t.Errorf("got %v, want ErrUnsupportedAlg (the none-alg attack must be rejected)", err)
	}
}

func TestStripsLayerIDPrefix(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	tok := mintToken(t, priv, map[string]any{"sub": "tenant_abc", "iat": time.Now().Unix()})
	withPrefix := "pk_live_" + tok
	if _, err := auth.Verify(withPrefix, testOpts(t, pub, time.Now())); err != nil {
		t.Errorf("prefix not stripped: %v", err)
	}
	if !strings.HasPrefix(withPrefix, "pk_live_") {
		t.Fatal("prefix wasn't actually present in the test input")
	}
}

func intStr(n int64) string {
	// tiny helper to avoid pulling strconv just for one call
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
