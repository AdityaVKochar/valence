# Valence

Valence is ACMVIT's in-house competitive programming platform. It runs contests and practice for around 200 concurrent users, with sandboxed judging, a browser-based editor, live leaderboards and optional proctoring. Nearly every feature is configurable at the global, contest, problem and language level.

The project is also meant to teach system design, so the architecture deliberately uses queues, an outbox, several purpose-built datastores, layered caching, sandboxing and realtime fan-out.

> **Status:** early planning. The plan below is subject to change as it improves, and this repository currently contains only the folder layout.

## Planned stack

| Area | Choice |
|---|---|
| Frontend | React + Vite SPA, TanStack Router and Query, Monaco editor |
| Backend | Go services in one monorepo: ConnectRPC, sqlc + pgx, OpenTelemetry |
| Judging | Warm pool of Go judge workers sandboxing each run with isolate (cgroups v2); Judge0 as a switchable fallback |
| Data | PostgreSQL (source of truth), Valkey (cache, leaderboards, rate limits), ClickHouse (telemetry), NATS JetStream (queues, events, live flags), Cloudflare R2 (objects) |
| Edge | Cloudflare CDN, Tunnel, WAF, Turnstile, Access, Workers and Durable Objects |
| Hosting | k3s on Hetzner Cloud, deployed with Helm and GitOps |

## Planned features

- Contests with ICPC, IOI, LeetCode-style and custom scoring, freezes, virtual participation, clarifications and rejudges
- Judging with custom checkers, test groups and a per-contest choice of judge provider
- Editor with three autocomplete levels: off, in-browser, or full server-side language servers
- Code autosave with conflict handling and snapshot history
- Realtime verdicts, announcements and leaderboards
- Contest telemetry (copy, paste, focus, keystroke stats) and a proctor console
- Screen sharing with periodic snapshots and on-demand live view
- Copy traps on problem statements (decoys, LLM canaries, watermarks)
- Layered, versioned configuration with hot reload

## Repository structure

```
valence/
├── apps/
│   └── web/                  React + Vite SPA (arena, admin, proctor console)
├── services/                 Go services (one go.work at the root)
│   ├── api/                  modular monolith: auth, problems, contests, submissions, config
│   ├── judge-worker/         NATS consumer, isolate sandbox, checkers
│   ├── realtime-gateway/     verdict, leaderboard and announcement fan-out
│   ├── lsp-gateway/          language-server sessions over WebSocket
│   └── telemetry-ingest/     browser events into NATS and ClickHouse
├── pkg/                      shared Go packages (config, judge providers, observability)
├── proto/                    protobuf contracts (buf, ConnectRPC)
├── db/
│   ├── migrations/           PostgreSQL schema migrations
│   ├── queries/              sqlc query files
│   └── clickhouse/           ClickHouse schemas
├── edge/                     Cloudflare Workers and Durable Objects
├── judge/
│   ├── toolchains/           digest-pinned language toolchain images
│   └── sandbox-tests/        sandbox escape test suite
├── deploy/
│   ├── infra/                Hetzner, Cloudflare and k3s provisioning
│   ├── helm/                 Helm charts and values
│   └── gitops/               Argo CD / Flux manifests
├── tools/                    operational tools (burst-judge capacity planner, scripts)
├── loadtest/                 k6 scenarios
└── docs/                     design docs and runbooks
```

Empty folders hold a `.gitkeep` file until real code lands in them.

## Roadmap

1. **Foundations:** monorepo, protobuf contracts, k3s, Cloudflare Tunnel, Postgres, Valkey, NATS, CI, observability
2. **MVP judge:** auth, problems, isolate judge, submissions, editor, autosave
3. **Contests:** lifecycle, scoring, leaderboards, realtime updates, configuration, Judge0 toggle
4. **Proctoring:** telemetry pipeline, proctor console, copy trap, screen share
5. **Power features:** full LSP, burst judges, resolver, spectator mode, code replay, plagiarism checks
6. **Authoring v2:** generators, validators, model solutions, signed test bundles
