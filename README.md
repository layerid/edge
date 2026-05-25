# layerid-edge

**LayerID's decision plane.** The open-source scoring runtime that joins intel-service features with browser device signals, applies the scoring model, and returns `{ score, verdict, signals }`.

**Status: scaffold only.** Detailed design lives in [`DESIGN.md`](./DESIGN.md). Implementation has not started.

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

## Plan (will be revisited)

See [`DESIGN.md`](./DESIGN.md) for the full architecture and migration plan. Summary:

1. Scaffold (this commit)
2. Port scoring from legacy `../fingerprints/` into `internal/score/`
3. Wire `../fingerprints-migration/backend/` to delegate Score calls to embedded edge
4. Retire legacy `../fingerprints/` after one-week shadow comparison
5. Publish public Helm chart + binary release for customer self-host

## What's here right now

```
.
├── README.md              this file
├── DESIGN.md              full architecture + 5-phase migration plan
├── CLAUDE.md              agent guide for when work resumes
├── proto/                 gRPC service definition (stub)
├── cmd/layerid-edge/      binary entrypoint (empty)
├── internal/
│   ├── score/             scoring model — empty (port target from fingerprints/)
│   ├── consume/           race-safe one-time req_id consume — empty
│   ├── cache/             local intel cache (LRU + TTL) — empty
│   ├── upstream/          gRPC client to intel-service — empty
│   └── shadow/            shadow-mode plumbing — empty
├── deploy/{docker,helm,binary}/   distribution artefacts — empty
├── docs/                  scoring-model.md, signal-reference.md, self-host.md — empty
└── examples/              integration examples — empty
```

## License

Apache 2.0. Will live at `github.com/layerid/edge` once the repo is created.
