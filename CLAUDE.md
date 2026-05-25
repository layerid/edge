# CLAUDE.md — `layerid-edge`

Agent guide for the (currently scaffolded) decision-plane runtime.

**Read [`DESIGN.md`](./DESIGN.md) first.** This file assumes you've read it.

## State right now

Scaffolded. Tree exists, README + DESIGN + proto stub committed. **No Go code yet.** The next session resuming work here is starting at Phase 2 of the migration plan: porting scoring logic from `../fingerprints/` into `internal/score/`.

## What you should never do here

1. **Don't add intel observation logic.** That's `../intel-service/`'s job. This repo *consumes* intel, never produces it.
2. **Don't add customer auth / billing / dashboard code.** That's `../fingerprints-migration/backend/`'s job. This repo is invoked by the BFF, it's not the BFF.
3. **Don't add a `Block(ip)` endpoint or any kind of binding-verdict RPC.** This is a scoring engine, not a policy enforcer. The workspace `CLAUDE.md` "signals not verdicts" framing is load-bearing — don't drift from it.
4. **Don't put closed-source secrets here.** This repo is intended to be open-sourced at Apache 2.0. Anything that needs to stay proprietary (model weights for vertical-specific catalogs, pre-disclosure feeds) lives in `intel-service/` or in a separate closed-source overlay.

## What you should always do

1. **Port from `../fingerprints/` first, refactor later.** During phase 2, faithfulness beats elegance. The existing scoring model has been calibrated against real customer outcomes; preserving its behaviour is more valuable than improving it in the same change.
2. **Test parity with `../fingerprints/`.** Every behavioural test against the legacy scoring path must pass against this one before phase 4 cutover. Add a `tests/parity/` subdir that runs both and compares.
3. **Stay agnostic of upstream.** The `internal/upstream/` package should treat `intel-service` as one possible source. The interface should support an offline mode where intel is loaded from a local snapshot. (Self-host customers will care.)
4. **Treat the proto as a public contract.** Additive changes only. `reserved` field numbers for removals. Mirror the discipline in `../intel-service/proto/honeypot.proto`.

## Coordinating changes with neighbours

- **Bumping `proto/edge.proto`**: customers integrate against this. Stagger releases — proto change → server release → SDK release → deprecation announcement → field removal one minor version later.
- **Bumping `internal/score/` model behaviour**: this changes score values for the same input. Customers calibrating against historical scores will notice. Either ship behind a `model_version` flag (customer opts in to new model) or announce a window and run both in shadow mode.
- **Adding a new signal**: needs an `intel-service` feature first (the upstream observation), THEN an edge port that exposes it. Default weight should be 0 (shadow-evaluated) until calibrated.

## Useful pointers when you resume

- Legacy scoring entry point: `../fingerprints/<wherever>/scoring/` — find it via `grep -rn "def score" ../fingerprints/`.
- Race-safe `consume` semantics: `../fingerprints/<wherever>/consume.py` — port the Redis-Lua atomic check-and-set faithfully.
- Browser device signals schema: `../layerid-fingerprint-js/src/types/` — the shape the SDK posts.
- Intel feature schema: `../intel-service/proto/honeypot.proto` `IPIntel` message.
- Workspace-level CLAUDE.md "Product positioning" section: that's the philosophy this implements.
