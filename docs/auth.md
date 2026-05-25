# Auth

Two boundaries. Different answers for each. JWT API keys solve both.

## The two boundaries

```
   customer's app                          layerid-edge                    intel-service
   ─────────────────                       ───────────────                 ──────────────
            │                                       │                           │
            ├── Layer 1 (downstream) ───────────────┤                           │
            │   App → Edge                          │                           │
            │                                       ├── Layer 2 (upstream) ─────┤
            │                                       │   Edge → Intel            │
```

## API keys ARE signed JWTs

When LayerID's SaaS issues an API key, the customer doesn't receive a random string — they receive a signed JWT presented as `pk_live_<JWT>`. The JWT is Ed25519-signed (small, fast verification, no quantum drama).

Example claims:

```json
{
  "sub": "tenant_abc123",
  "tier": "standard",
  "rate_limit": 100000,
  "features": ["lookup", "consume", "shadow", "custom_weights"],
  "region_pin": "eu-central-1",
  "iat": 1716000000,
  "exp": null,
  "iss": "https://auth.layerid.io",
  "kid": "edge-2026-05"
}
```

The customer sees:

```
pk_live_eyJhbGciOiJFZERTQSIsInR5cCI6IkpXVCIsImtpZCI6ImVkZ2UtMjAyNi0wNSJ9.eyJzdWIiOiJ0ZW5hbnRfYWJjMTIzIi...
```

Anyone with the public key can validate. So **stateless validation everywhere it matters.**

### Two key flavours

| Flavour | Prefix | Purpose | Where used |
|---|---|---|---|
| **Secret key** | `pk_live_…` (or `pk_test_…`) | Authenticates server-side calls to Score/Consume. Carries tier, quota, features. Never embedded in browser. | Customer's backend → edge / SaaS API |
| **Public key** | `pp_live_…` (or `pp_test_…`) | Tenant identifier only. No bearer authority. Embedded safely in browser JS. | Browser SDK → `/v1/init` to start a fingerprint session |

Same JWT format, different `kty` / `scope` claim. The public key is signed-but-low-authority — knowing it lets you start fingerprint sessions for that tenant (counted against their quota), but not call Score/Consume.

## Layer 1: Customer → Edge (downstream)

### SaaS mode

Customer's backend → `api.layerid.io` (which terminates at `fingerprints-migration/backend`, then internally hits `layerid-edge`):

```
   customer backend
        │ "Authorization: Bearer pk_live_eyJ..."
        ▼
   fingerprints-migration/backend  (BFF)
        │ verifies JWT signature with public key
        │ rejects if revoked (Bloom filter + Redis revocation list)
        │ extracts tenant_id, tier, rate_limit, features
        │ enforces rate limit (Redis sliding window)
        │ attaches tenant metadata to internal gRPC call
        ▼
   layerid-edge  (localhost trust)
        │ no auth check — BFF already validated
        │ processes Score / Consume
```

The BFF is the customer-facing trust boundary. Edge trusts the BFF.

### Self-host mode

Customer runs the edge in their VPC. Their backend calls localhost-edge:

```
   customer backend
        │ "Authorization: Bearer pk_live_eyJ..."
        ▼
   layerid-edge  (in customer's VPC)
        │ verifies JWT signature with embedded LayerID public key
        │ extracts tenant_id, tier, features
        │ no remote validation needed (key is self-validating)
        │ processes Score / Consume
        │ ↓ upstream call to LayerID for intel features ↓
```

Critical property: **the edge can validate the customer's API key without phoning home.** All it needs is the LayerID public key, which it gets via the JWKS endpoint at build time + refreshes nightly.

Three deployment sub-modes for self-host:

1. **Standalone**: localhost trust between customer's app and edge. No auth between them. The app's developers trust their own infrastructure.
2. **mTLS**: edge requires client cert. Customer's K8s service mesh handles the cert. Stronger but heavier.
3. **API key**: edge requires `pk_live_xxx` even on localhost calls. Same JWT, validated locally. For multi-team setups where one team runs the edge and others use it.

Pick at edge config (`auth.mode: "trust" | "mtls" | "api_key"`).

## Layer 2: Edge → Intel-service (upstream)

The edge needs to fetch intel features from `intel-service/collector` over the public internet (in self-host) or over the private mesh (in SaaS).

Each edge instance has its own `edge_key` — a JWT issued during deployment, signed by `auth.layerid.io`:

```json
{
  "sub": "edge_instance_eu1_pod42",
  "tenant_id": "tenant_abc123",     // for self-host: which customer's edge is this
  "type": "edge_instance",
  "issued_for": "intel-service",
  "iat": 1716000000,
  "exp": 1716086400,                // 24h TTL; refreshed automatically
  "iss": "https://auth.layerid.io",
  "kid": "edge-instance-2026-05"
}
```

Edge → intel-service flow:

1. Edge starts up. Reads `edge_key` from env / mounted secret.
2. Edge calls `auth.layerid.io/v1/exchange` to get a fresh 24h JWT (the `edge_key` itself is long-lived; the exchanged JWT is short-lived).
3. Edge calls intel-service over **mTLS + JWT in metadata**. Intel-service validates JWT signature locally, checks tier-specific feature access (e.g. is this customer allowed pre-disclosure feed?).
4. Intel-service returns features.
5. Before JWT expiry, edge silently refreshes.

Why both mTLS and JWT:
- **JWT** carries identity / tier claims that intel-service needs for authorization.
- **mTLS** pins transport — prevents MITM, lets intel-service whitelist known edge versions / sources.

Why short-lived (24h) instead of static:
- Revocation: revoke a customer's `edge_key`, their existing JWT expires within 24h. No global revocation list needed.
- Defence in depth: a compromised JWT is bounded.
- Standard pattern: AWS STS, GCP service account keys, OAuth client credentials all work this way.

## JWKS endpoint

`https://auth.layerid.io/.well-known/jwks.json` returns the current public keys:

```json
{
  "keys": [
    {
      "kty": "OKP",
      "crv": "Ed25519",
      "use": "sig",
      "kid": "edge-2026-05",
      "alg": "EdDSA",
      "x": "..."
    },
    {
      "kty": "OKP",
      "crv": "Ed25519",
      "use": "sig",
      "kid": "edge-instance-2026-05",
      "alg": "EdDSA",
      "x": "..."
    }
  ]
}
```

Edge and intel-service both fetch this:
- At build time, bake the current keys into the binary (no network needed for cold start)
- At runtime, refresh hourly with `Cache-Control: max-age=3600`
- On JWT signature failure: refresh JWKS immediately + retry signature check once (handles key rotation gracefully)

This is the same pattern Auth0 / Okta / Cognito use for their machine-to-machine tokens. Customer self-host gets the same JWKS endpoint over HTTPS; no LayerID-side state for them to depend on.

## Key rotation

Rolling a `kid`:

1. Generate new keypair, assign new `kid` (e.g. `edge-2026-08`).
2. Add new public key to JWKS. **Don't remove the old one yet.**
3. Start issuing new JWTs with new `kid`.
4. Wait for max JWT TTL × 2 (so all old JWTs have naturally expired).
5. Remove old public key from JWKS.

During the overlap window, both old and new JWTs validate. Customers see no disruption.

Emergency revocation (a `kid` is compromised):

1. Remove the compromised `kid` from JWKS immediately.
2. All JWTs signed by it fail validation immediately.
3. Customers need to re-issue their API keys (they get new JWTs from the new `kid`).
4. **This is disruptive** — communicate ahead of time, ideally never needed.

Per-customer revocation (a single key compromised, not the whole `kid`):

1. Add the JWT's `jti` (or hash thereof) to a Redis revocation list.
2. Edge and intel-service check the list before accepting any JWT.
3. List entries auto-expire when the JWT would have expired anyway.

The revocation list is small (kilobytes typically). Cached in-memory in edge, refreshed every 30s.

## Customer-facing key UX

Dashboard at `app.layerid.io/keys`:

```
   ┌──────────────────────────────────────────────────────┐
   │ API Keys                                             │
   │                                                      │
   │ pk_live_eyJ...x1Q (created 2026-04-12)              │
   │   tier: standard · features: all · rate: 100k/mo    │
   │   [Rotate] [Revoke]                                  │
   │                                                      │
   │ pk_test_eyJ...mK7 (created 2026-04-12)              │
   │   tier: test · features: all · rate: 10k/mo         │
   │   [Rotate] [Revoke]                                  │
   │                                                      │
   │ pp_live_eyJ...n9P (created 2026-04-12)              │
   │   purpose: browser SDK · tier: standard             │
   │   [Rotate] [Revoke]                                  │
   │                                                      │
   │ [+ Create new key]                                  │
   └──────────────────────────────────────────────────────┘
```

Operations:
- **Create**: signs a new JWT with the customer's current tier/features. Customer copies once; we don't store the secret.
- **Rotate**: issues a new key, marks the old one for deletion in 7 days (gives customer time to update their backends).
- **Revoke**: adds the key's `jti` to the revocation list immediately.

## What about `fingerprints-migration/backend` ↔ `layerid-id`?

The BFF authenticates the **end-user-facing dashboard** via OAuth/OIDC against `layerid-id`. That's separate from API key auth.

```
   customer's developer (human)
        │
        │ browser OAuth flow against layerid-id
        ▼
   fingerprints-migration/frontend (Nuxt)
        │ session cookie scoped to .layerid.io
        ▼
   fingerprints-migration/backend
        │ resolves session → customer account
        │ uses account permissions to gate /api/keys/* operations
        │ (create / rotate / revoke API keys for this customer)
```

So the BFF talks two auth protocols:
- **Bearer JWT** for machine traffic (Score / Consume / customer's backend integrating)
- **OAuth session** for human traffic (dashboard, customer admin)

`layerid-id` issues the OAuth sessions; `auth.layerid.io` issues the API key JWTs. They're separate services with separate signing keys.

## Implementation order (when we resume code)

1. `internal/auth/jwks.go` — fetch + cache JWKS, key resolution
2. `internal/auth/verify.go` — Ed25519 signature verification, claim extraction
3. `internal/auth/revocation.go` — Redis revocation list check
4. `internal/auth/middleware.go` — gRPC interceptor that handles all three checks
5. Stub `auth.layerid.io` issuer for tests (`tests/fake_issuer/`)

`internal/auth/` is small (~500 lines). It's also load-bearing: get the signature verification wrong and the whole product is compromised. Don't roll your own crypto — use `crypto/ed25519` from Go stdlib + `github.com/golang-jwt/jwt/v5` for the JWT plumbing.
