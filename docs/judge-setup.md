# Running a judge worker

## On your laptop

`make run-worker` starts a worker with the defaults from `.env.example`. With `JUDGE_PROVIDER=auto` it uses isolate if your machine has it and falls back to `local-unsafe` otherwise, which needs only the compilers on your `PATH`. A worker offers the languages whose compilers it finds and logs them at startup:

```
Judge Worker starting provider=local-unsafe slots=7 languages="[cpp17 c17 python3 go]"
```

`local-unsafe` runs submissions as you, so only judge code you trust with it.

## With isolate, in Docker

The worker image in [`judge/Dockerfile`](../judge/Dockerfile) has isolate and every toolchain in `judge/languages.yaml`. isolate needs cgroups v2, which every current Linux distribution and Docker Desktop provide. The container must be privileged with its own cgroup namespace; its entrypoint then sets up a cgroup subtree for isolate:

```sh
docker build -f judge/Dockerfile -t valence-judge .
docker run --privileged --cgroupns=private \
  -e DATABASE_URL=postgres://... \
  -e API_INTERNAL_URL=http://api:8090 \
  -e INTERNAL_TOKEN=... \
  valence-judge
```

The image sets `VALENCE_ENV=prod` and `JUDGE_PROVIDER=local-isolate`, so the worker refuses to start without a working isolate rather than falling back to running code unsandboxed.

## Settings

| Variable | Default | Meaning |
|---|---|---|
| `DATABASE_URL` | required | The queue lives in Postgres |
| `API_INTERNAL_URL` | `http://localhost:8090` | The API's internal port, for jobs, test files and results |
| `INTERNAL_TOKEN` | dev default | Shared secret with the API; required (32+ characters) in prod |
| `WORKER_SLOTS` | CPUs - 1 | Submissions judged at once. Give each slot a dedicated core for stable timings |
| `JUDGE_PROVIDER` | `auto` | `auto`, `local-isolate` or `local-unsafe` |
| `ISOLATE_BOX_BASE` | `0` | First isolate box ID; slots use the IDs after it. Give workers that share a machine different ranges |
| `JUDGE_CACHE_DIR`, `JUDGE_CACHE_MAX_MIB` | `.data/judge-cache`, 2048 | Local copy of test files |
| `JUDGE_WORK_DIR` | `.data/judge-work` | Scratch space for compiling |
| `WORKER_ADDR` | `:8081` | `/healthz`, `/readyz` and `/metrics` |

## Testing the sandbox

The judge tests and the [sandbox escape suite](../judge/sandbox-tests) run against isolate inside the test image. CI runs them on every pull request; to run them yourself:

```sh
docker build -f judge/Dockerfile --target test -t valence-judge-test .
docker run --rm --privileged --cgroupns=private valence-judge-test
```

On a Linux machine with isolate installed you can also run `make sandbox-test` as root.
