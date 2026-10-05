# Valence

Valence is ACMVIT's in-house competitive programming platform. It runs contests and practice with sandboxed judging, a browser-based editor, live leaderboards and optional proctoring. Nearly every feature is configurable at the global, contest, problem and language level.


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