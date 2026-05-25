# layerid-edge

**LayerID's decision plane.** The open-source scoring runtime that joins intel-service features with browser device signals, applies the scoring model, and returns `{ score, verdict, signals }`.

**Status: alpha.** Detailed design lives in [`DESIGN.md`](./DESIGN.md). The binary runs end-to-end (HTTP endpoints, in-memory store, JWT auth, 7 of 14 signals implemented). Postgres + JWKS + upstream intel-service gRPC are wired in subsequent phases.

## Quick start (60 seconds)

```bash
# 1. Run the engine in dev mode (no DB, synthetic auth)
LAYERID_EDGE_AUTH_DISABLED=1 go run ./cmd/layerid-edge

# 2. In another terminal, hit it end-to-end
./examples/smoke.sh
```

You'll see init → lookup → consume → consume-idempotent, all green. The default weighted scorer emits a real verdict against a populated signal payload (no `insufficient_data`).

Bare-metal curl:

```bash
curl http://localhost:8080/healthz
# {"ok":true,"version":"0.2.0-scaffold","region":"dev"}

curl http://localhost:8080/v1/scorers
# {"default":"weighted","names":["const-allow","const-deny","const-step-up","weighted"]}

curl -X POST http://localhost:8080/v1/score \
  -H 'Authorization: Bearer dev' \
  -H 'Content-Type: application/json' \
  -d '{"scorer":"const-deny","signals":{}}'
# {"score":0,"verdict":"deny","model":"const-deny@v1",...}
```

## What this is

`layerid-edge` is the scoring engine. It sits between:

- **upstream**: `intel-service/collector` (LayerID's data plane — features about IPs, JA3, TCP)
- **downstream**: customer applications (or LayerID's own `fingerprints-migration/backend` BFF)

```
   browser/device signals ──┐
                            │
                            ▼
                    ┌──────────────┐         ┌────────────────────┐
                    │ layerid-edge │ ─ gRPC ─▶│  intel-service     │
                    │ (this repo)  │ ◀──────  │  features          │
                    └──────┬───────┘         └────────────────────┘
                           │
                           ▼
                  { score, verdict, signals }
                  → customer policy engine decides
```

## Why open source

The data is the moat (intel-service stays closed). The code that scores it shouldn't be. Open-sourcing the edge:

- Lets customers **audit** how their signals are weighted into a score
- Lets customers **self-host** the edge in their own VPC for data residency / sub-5ms latency / failure independence
- Lets us **dogfood** — the same binary scores requests in our SaaS and in customer infrastructure
- Lets contributors add **distro-specific JA3 catalog labels**, signal weights, integration adapters

## Configuration

All knobs are env vars (12-factor):

| Variable | Default | Purpose |
|---|---|---|
| `LAYERID_EDGE_LISTEN` | `:8080` | HTTP bind address |
| `LAYERID_EDGE_DEFAULT_SCORER` | `weighted` | One of: `weighted`, `const-allow`, `const-deny`, `const-step-up` |
| `LAYERID_EDGE_REGION` | `dev` | Prefix attached to every `req_id` for GeoDNS routing |
| `LAYERID_EDGE_AUTH_DISABLED` | _(unset)_ | Set to `1` to inject synthetic `tenant_0` claims. **Dev only.** |

## Scorers

The scoring strategy is pluggable — `internal/score.Scorer` is an interface. Out of the box:

- **`weighted`** — the 14-signal weighted-average model ported from legacy `fingerprints/`. 7 signals implemented today; the rest are stubbed (port pending). See [`docs/scoring-model.md`](./docs/scoring-model.md).
- **`const-allow` / `const-deny` / `const-step-up`** — constant scorers, useful for smoke tests and for short-circuiting traffic in incident response.

Implemented signals:

| Signal | Weight | Notes |
|---|---|---|
| `ip_match` | 0.15 | Peer-IP heuristics (not yet intel-service-backed) |
| `webdriver` | 0.20 | `navigator.webdriver` flag — `true` is a near-certain bot signal |
| `timings` | 0.05 | App-side response time bucket |
| `screen_resolution` | 0.05 | Common-resolution & aspect-ratio plausibility |
| `window_screen_ratio` | 0.05 | Detects window > screen (antidetect) and exact match (headless) |
| `timezone` | 0.10 | IANA shape check (geo-cross-check pending intel-service) |
| `webgl_renderer` | 0.05 | Flags software renderers (SwiftShader, llvmpipe) |

Stubbed (planned): `ua_match`, `os_match`, `headers_order`, `proxy_detection`, `residential_proxy`, `ip_reputation`, `stun_webrtc`.

## Layout

```
.
├── cmd/layerid-edge/                binary entrypoint
├── internal/
│   ├── api/                         HTTP handlers (init, score, lookup, consume)
│   ├── auth/                        Ed25519 JWT verifier + middleware
│   ├── score/                       Scorer interface + Registry
│   │   ├── constscorer/             const-allow / const-deny / const-step-up
│   │   └── weighted/                14-signal weighted-average scorer
│   │       └── signals/             per-signal SignalFunc implementations
│   └── store/                       Lookup persistence (idempotent init / consume)
│       ├── memory/                  in-memory impl + tests
│       └── postgres/                pgx-backed impl (queries done, driver wiring pending)
├── deploy/postgres/migrations/      initial schema (partitioned lookups, quota, audit)
├── docs/                            scoring-model / consume / multi-region / auth
├── examples/smoke.sh                end-to-end smoke test
└── proto/edge.proto                 gRPC service definition (stub)
```

## License

Apache 2.0. Repo lives at [`github.com/layerid/edge`](https://github.com/layerid/edge).
