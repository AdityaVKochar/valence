# Valence

Valence is ACMVIT's in-house competitive programming platform. It runs contests and practice with sandboxed judging, a browser-based editor, live leaderboards and optional proctoring. Nearly every feature is configurable at the global, contest, problem and language level.


## Getting started

### Prerequisites

- [Go](https://go.dev/dl/) 1.25 or newer
- [Node.js](https://nodejs.org/) 22 or newer and [pnpm](https://pnpm.io/installation) 10
- [Docker](https://docs.docker.com/get-docker/), for Postgres
- A C/C++ compiler and Python 3, to judge submissions locally. Other languages work when their compilers are installed.

### 1. Clone and install

```bash
git clone https://github.com/AdityaVKochar/valence.git
cd valence
pnpm install
go mod download
cp .env.example .env
```

`make help` lists every target. The Makefile reads `.env`, so the defaults work as they are.

### 2. Start Postgres and load the sample problems

```bash
docker run -d --name valence-pg -p 5432:5432 \
  -e POSTGRES_USER=valence -e POSTGRES_PASSWORD=valence -e POSTGRES_DB=valence \
  postgres:17

make migrate   # the API also migrates on start in dev
make seed      # imports the six problems in problems/examples
```

### 3. Run the services

Each in its own terminal:

```bash
make run-api                # http://localhost:8080, internal port 8090
make run-worker             # judges submissions; http://localhost:8081/healthz
pnpm --filter web dev       # http://localhost:5173
```

Without GitHub or Google credentials in `.env`, sign in with the development login: open http://localhost:8080/auth/dev?user=alice (add `&role=admin` for an admin).

On a machine without isolate the worker judges with `local-unsafe`, which runs submissions as your user. That is fine for your own test code; see [docs/judge-setup.md](docs/judge-setup.md) for the sandboxed setup.

### 4. Check your changes

```bash
make fmt          # gofmt
make vet
make test         # needs Postgres: set VALENCE_TEST_DATABASE_URL, or have Docker running
make gen-check    # after changing proto/ or db/queries/
pnpm --filter web lint
pnpm --filter web build
```

`make test` includes an end-to-end test that starts the API and a worker and judges every sample solution. `make test-short` skips everything that needs Postgres.

| Service | Port | Override |
|---|---|---|
| api | 8080 | `API_ADDR` |
| api (internal: workers, metrics) | 8090 | `API_INTERNAL_ADDR` |
| judge-worker | 8081 | `WORKER_ADDR` |
| web (Vite) | 5173 | |
| Postgres | 5432 | |

### Docs

- [Problem package format](docs/problem-format.md)
- [How judging works](docs/judging.md)
- [Running a judge worker](docs/judge-setup.md)
- [Sandbox escape tests](judge/sandbox-tests/README.md)
- Load testing: `make smoke` runs [loadtest/smoke.js](loadtest/smoke.js) with [k6](https://grafana.com/docs/k6/latest/set-up/install-k6/)

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
