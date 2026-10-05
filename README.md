# Valence

Valence is ACMVIT's in-house competitive programming platform. It runs contests and practice with sandboxed judging, a browser-based editor, live leaderboards and optional proctoring. Nearly every feature is configurable at the global, contest, problem and language level.


## Getting started

### Prerequisites

- [Go](https://go.dev/dl/) 1.25 or newer
- [Node.js](https://nodejs.org/) 22 or newer and [pnpm](https://pnpm.io/installation) 10
- [Docker](https://docs.docker.com/get-docker/), for Postgres
- [goose](https://github.com/pressly/goose), for database migrations: `go install github.com/pressly/goose/v3/cmd/goose@latest`

### 1. Clone and install

```bash
git clone https://github.com/AdityaVKochar/valence.git
cd valence
pnpm install
go mod download
```

### 2. Start Postgres and apply migrations

```bash
docker run -d --name valence-pg -p 5432:5432 \
  -e POSTGRES_USER=valence -e POSTGRES_PASSWORD=valence -e POSTGRES_DB=valence \
  postgres:17

export DATABASE_URL="postgres://valence:valence@localhost:5432/valence?sslmode=disable"
goose -dir db/migrations postgres "$DATABASE_URL" up
```

### 3. Run the services

Each in its own terminal:

```bash
go run ./services/api/cmd/api                    # http://localhost:8080/healthz
go run ./services/judge-worker/cmd/judge-worker  # http://localhost:8081/healthz
pnpm --filter web dev                            # http://localhost:5173
```

### 4. Check your changes

```bash
gofmt -l .        # should print nothing
go vet ./...
go test ./...
pnpm --filter web lint
pnpm --filter web build
```

| Service | Port | Override |
|---|---|---|
| api | 8080 | `-addr` flag or `API_ADDR` |
| judge-worker | 8081 | `-addr` flag or `WORKER_ADDR` |
| web (Vite) | 5173 | |
| Postgres | 5432 | |

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
