# Scoring model

The scoring engine is **a pluggable strategy interface**. The 14-signal weighted model from legacy is the **first concrete implementation** — and the one used at launch. But the interface is what's load-bearing: we want to experiment with different scorers without touching the rest of the edge.

> **Source of truth (initial implementation)**: `../../fingerprints/src/modules/fingerprints/domain/scoring.py` (943 lines, `FingerprintScoringService.score()`). Weights and thresholds in this doc were extracted from that file; treat the legacy as authoritative if there's drift.

## The Scorer interface

```go
package score

type Scorer interface {
    // Name + Version identify this scorer in responses / dashboards.
    // Same input + same Name + same Version MUST produce same output.
    Name() string
    Version() string

    // Score produces a verdict from signals. Pure function — no I/O.
    // Returning ErrInsufficientData lets the edge surface
    // verdict: "insufficient_data" without inventing a fake score.
    Score(input Signals) (Result, error)

    // Explain returns per-signal contributions for the dashboard /
    // calibration page. Optional — return nil if the scorer is a
    // black box (ML models, customer hosted scorers).
    Explain(input Signals) *Explanation
}

type Result struct {
    Score   float64       // [0.0, 1.0]
    Verdict string        // "allow" | "step-up" | "deny" | "log" | "insufficient_data"
    Model   string        // mirrors Name() + Version()
}

type Explanation struct {
    Signals []SignalContribution
    Weights map[string]float64
    Total   float64
}
```

The edge instantiates scorers from a registry; the request picks one based on:

1. **Customer override** (per-tenant config in DB): "use scorer X for my traffic"
2. **Tier default** (free/standard/pro have different defaults)
3. **Shadow flag** in the request (forces a specific scorer for evaluation)

## Concrete scorers at launch

| Scorer | Name | What it does | When to use |
|---|---|---|---|
| **WeightedAverage** | `weighted-v1` | The 14-signal model from legacy. Documented below. | Default for all tiers at launch. |
| **WeightedAverageWithOverrides** | `weighted-v1-custom` | Same model, weights from `scoring_weights` table per tenant. | Customers who tune their own weights via dashboard. |
| **Shadow** | `shadow-<a>-vs-<b>` | Wraps two scorers, returns `<a>`'s result, logs `<b>`'s result for comparison. | Calibration during model migrations. |
| **Const** | `const-allow` / `const-deny` / `const-test` | Returns a fixed verdict regardless of input. | Tests; canary endpoints; "panic kill switch" if we ship a broken model. |
| **Rule** | `rule-v1` | Hard-coded boolean rules — e.g. "residential_proxy AND narrow_ja3 AND iot_cctv → deny". | Customers who want rules over scoring; high-precision low-recall traffic. |

Future scorers (not in launch):

- `ml-v1` — trained model. Same interface, opaque internals. Customers can pin a `model_version` for stability.
- `per-vertical-v1` — banking / gaming / e-commerce-specific weight defaults.
- `customer-hosted` — calls a customer's own scorer over gRPC. Edge becomes a transport.

The interface stays the same; the implementations grow.

## Where the 14-signal model lives

```
internal/score/
├── registry.go              maps name → Scorer constructor
├── interface.go             Scorer, Result, Explanation, Signals
├── weighted/
│   ├── weighted_average.go  the WeightedAverage scorer
│   ├── signals/             one file per signal
│   │   ├── ip_match.go
│   │   ├── os_match.go      TTL + IP-ID OS detection
│   │   ├── stun_webrtc.go   9-case decision tree
│   │   └── …
│   └── weights.go           default weight table
├── rule/
│   └── rule_engine.go
├── shadow/
│   └── shadow_scorer.go
└── const/
    └── const_scorer.go
```

Each signal is its own file. Adding a new signal = adding one file + registering it in `weighted/signals/registry.go` + giving it a default weight of 0 (shadow). This keeps the model surgically modifiable.

## The 14 signals

Each signal returns a normalised `[0.0, 1.0]` score where `1.0` = consistent with a legitimate human user, `0.0` = consistent with abuse. The weighted average across **available** signals is the final score.

| # | Signal | Weight | Range | What it measures |
|---|---|---:|---|---|
| 1 | `ip_match` | 0.15 | binary | JS-reported IP matches network-observed IP |
| 2 | `ua_match` | 0.15 | binary | User-Agent matches across JS + headers |
| 3 | `os_match` | 0.20 | 0–1 | TCP TTL + IP-ID consistent with claimed OS (Linux/Windows/macOS) |
| 4 | `headers_order` | 0.10 | binary | HTTP/2 pseudo-header ordering matches claimed browser (Firefox only — Chrome too lenient) |
| 5 | `timings` | 0.05 | 0–1 | Request roundtrip latency reasonable (≤300ms=1.0, ≤600ms=0.5, else 0.0) |
| 6 | `proxy_detection` | 0.25 | 0–1 | `app_timing / tcp_rtt` ratio — ≤2.0 direct, ≤8.0 maybe proxy, >15.0 definitely proxy |
| 7 | `timezone` | 0.10 | 0–1 | JS timezone vs GeoIP-inferred timezone: exact=1.0, same region=0.7, mismatch=0.2 |
| 8 | `screen_resolution` | 0.05 | 0–1 | Common resolution=1.0, valid aspect=0.8, unusual=0.3–0.6 |
| 9 | `window_screen_ratio` | 0.05 | 0–1 | Window/screen ratio between 0.7–0.99 (normal browser chrome)=1.0 |
| 10 | `webgl_renderer` | 0.05 | 0–1 | Renderer string in known-good list=1.0, suspicious=0.2 |
| 11 | `webdriver` | 0.20 | binary | `navigator.webdriver === true` or other automation marker present |
| 12 | `residential_proxy` | 0.15 | 0–1 | WebRTC reports IPs without STUN being run (antidetect signature) |
| 13 | `ip_reputation` | 0.25 | 0–1 | GeoIP feed: proxy −0.7, hosting −0.5, blacklist −0.15 to −0.4, mobile +0.1 |
| 14 | `stun_webrtc` | 0.25 | 0–1 | 9-case decision tree — `antidetect_residential`=0.0, `stun_mismatch`=0.0, `match`=1.0, etc. |

Sum of weights = 2.50. Final score = `Σ(signal × weight) / Σ(weights_of_available_signals)`. Signals that didn't fire (e.g. STUN blocked, no WebGL) are excluded from both numerator and denominator.

## Risk levels (legacy thresholds)

```
score ≥ 0.7   → LOW    risk (verdict: allow)
0.4 ≤ s < 0.7 → MEDIUM risk (verdict: step-up)
score < 0.4   → HIGH   risk (verdict: deny / log)
```

These thresholds are **uncalibrated** in the legacy (no A/B record, picked by feel). The rewrite is the chance to:

1. Keep them as defaults — they're not obviously wrong, and customers have built policies around them.
2. **Expose customer overrides** — per-API-key threshold tuning in the dashboard.
3. **Track outcomes** — when customers send back ground truth via shadow-mode, we calibrate per-tier defaults.

## What changes in v2

**Preserved exactly (ported to Go faithfully):**
- All 14 signals with same weights.
- TTL/IP-ID OS detection heuristic (`scoring.py` lines 200–340 — clever, hard to spoof).
- 9-case STUN/WebRTC decision tree (`scoring.py` lines 480–620 — heavily tuned).
- Weighted-average formula with availability gating.
- Risk thresholds 0.7 / 0.4.

**Improved:**

1. **Pluggable signal sources.** Legacy reads signals from its own Mongo + sniffer. v2 reads from `intel-service` (IP rep, residential pool membership, JA3 breadth) + browser SDK (JS signals) + edge-local enrichment (TCP fingerprint via packet inspection). No single failure path takes scoring down — intel-service down means those signals come back `null` (unavailable), not "score = 0."

2. **Configurable weights per customer.** Stored in Postgres `customer_scoring_weights(tenant_id, signal_name, weight)`. Default weights from this doc; customers can tune in the dashboard. The same scoring engine, two parameter sets.

3. **Shadow-mode evaluation.** New signals ship with `weight = 0` initially; they're computed and returned but don't affect the score. Customers can pull `signals_json` from the dashboard, regress against their ground truth, and promote signals to non-zero weight when calibrated.

4. **Per-signal calibration tracking.** For customers who send back outcomes via `/v1/outcomes`, we compute per-signal precision/recall and surface in the dashboard. The user can see "your `webgl_renderer` signal has 0.31 correlation with your fraud outcomes — consider down-weighting."

5. **Explicit "uncertain" return.** Legacy returns a score even when only 3 of 14 signals are available — which is statistically meaningless. v2 returns `verdict: "insufficient_data"` if fewer than N signals fired, where N is configurable (default 6).

6. **No more `decision: require_2fa()` strings in responses.** Verdict is `allow | step-up | deny | log | insufficient_data`. Customer's policy engine maps these to its UX (CAPTCHA / 2FA / lockout / etc.).

## What stays in legacy until proven otherwise

- The specific TTL ranges for OS detection (`±16` from baseline). These were tuned against real traffic; changing them needs evidence.
- The 9-case STUN decision tree. Same — tuned against real adversaries.
- The proxy-detection RTT ratio thresholds (2.0 / 8.0 / 15.0). Same.

If the port reveals subtle behaviour differences, **the port is wrong**, not the legacy.

## Outcomes-driven calibration (the long game)

Once enough customers send outcomes back via `/v1/outcomes`, the scoring weights become learnable. The trajectory:

1. Phase A (now): hard-coded weights, ported from legacy.
2. Phase B (3-6 months in): per-customer override UI, default weights still hard-coded.
3. Phase C (6-12 months in): default weights per industry vertical (banking / gaming / e-commerce) — bootstrapped from customers who opted into outcome sharing.
4. Phase D (year 1+): online learning. Default weights drift based on aggregate outcome data. Customers can pin to a specific `model_version` for stability.

Phase D requires a `model_version` field in the Score response and a versioning policy. Not for v2 launch — but the design shouldn't preclude it.

## Pluggable signals (within the WeightedAverage scorer)

Adding a new signal to the WeightedAverage model:

1. Add the signal to the proto `Signals` message in `protocol/edge.proto` with a new field number.
2. Implement the computation in `internal/score/weighted/signals/<name>.go` — must satisfy `weighted.SignalFunc` interface returning `(score float64, available bool, contribution *Detail)`.
3. Register in `internal/score/weighted/signals/registry.go` with **default weight 0** (shadow).
4. Customers opt-in via dashboard (sets their per-customer weight > 0).
5. After enough data, promote the default weight from 0 to its calibrated value.

A different scorer (e.g. `rule-v1`) might consume the new signal differently or ignore it entirely — that's the point of the strategy interface.

## Adding a whole new scorer

The faster experiment loop. To try a totally different model:

1. Create `internal/score/<your-strategy>/` with a struct that satisfies `score.Scorer`.
2. Register it in `internal/score/registry.go`:
   ```go
   registry.Register("experimental-v1", func(deps ...) score.Scorer {
       return experimental.New(...)
   })
   ```
3. Enable for one tenant via dashboard → `customer_scorer_override` table.
4. Run in shadow against `weighted-v1` for as long as it takes to be confident.
5. Promote.

The whole flow is config — no recompile, no redeploy of the customer's integration. Tools like this are what makes "play with it" actually fast.

## Customer-hosted scorers (Pro tier and above)

A customer who wants to run their own scoring model can register a gRPC endpoint that satisfies the `Scorer` interface. Edge calls it for that customer's traffic:

```yaml
tenant: customer_abc
scorer:
  name: customer-hosted-v1
  endpoint: scoring.customer.internal:50051
  timeout_ms: 50
  fallback: weighted-v1     # if customer endpoint times out
```

This is the ultimate flexibility — customer plugs their own model into our edge while keeping all the surrounding plumbing (cache, consume, audit, dashboard). They write 100 lines of Go/Python/whatever, satisfy the proto contract, ship.

## Testing parity with legacy

A `tests/parity/` subdir runs both the legacy Python scoring (via gRPC call to a legacy adapter) and the new Go scoring against the same input, and asserts the score is within `±0.005`. This stays green until phase 4 cutover, at which point the legacy is retired and the parity tests become regression tests against captured golden inputs.
