# How judging works

```
browser ──SubmitSolution──▶ api ──insert──▶ Postgres (submissions, attempts, jobs)
                                                     ▲            │
                                    ReportProgress,  │            │ lease (SKIP LOCKED)
                                    ReportResult     │            ▼
                                     (internal port) api ◀──── judge-worker ──▶ isolate box
```

1. **Submit.** `SubmitSolution` checks the user's rate limits (`SUBMIT_COOLDOWN`, `MAX_ACTIVE_SUBMISSIONS`) and code size (`MAX_CODE_KIB`), then inserts the submission, attempt 1 and a `judge` job in one transaction.
2. **Lease.** A worker leases the job with `FOR UPDATE SKIP LOCKED` and a one-minute lease that it renews every 20 seconds. Postgres `LISTEN/NOTIFY` wakes idle workers, so there is no polling delay.
3. **Fetch.** The worker asks the API for the job (`GetJob` on the internal port, authenticated with `INTERNAL_TOKEN`), then fetches test files it does not have from `/internal/blobs/{sha256}` into its local cache. The cache is content-addressed and evicts least recently used files past `JUDGE_CACHE_MAX_MIB`.
4. **Compile.** The source is compiled in a box with the language's compile limits. A non-zero exit is CE, with the compiler output (up to 64 KiB) saved for the user.
5. **Run.** Each test runs in a fresh box with the problem's limits scaled by the language. A test that fails stops judging, and its verdict is the submission's verdict:

   | Verdict | When |
   |---|---|
   | MLE | Peak memory went over the limit, or the cgroup killed the program for memory |
   | TLE | CPU time went over the limit, or wall time over twice the limit plus a second |
   | OLE | Output reached 64 MiB |
   | RE | Non-zero exit or a signal |
   | WA | The checker rejected the output |
   | AC | Every test passed |

6. **Report.** The worker streams progress (compiling, running test N) and then the result. `ReportResult` is idempotent: replaying it for a finalized attempt does nothing, so a retried report can never double-count.

## Failures and retries

| What happened | What happens next |
|---|---|
| Worker crashed or lost its network | The lease runs out and another worker picks the job up |
| Infrastructure error (fetch failed, box would not start) | The job goes back to the queue with backoff (2s, 4s, 8s...) |
| Third failed delivery | The submission is finalized as IE (internal error) |
| Worker is shutting down | Jobs in flight get 25 seconds; unfinished ones go straight back to the queue |
| Unknown language or a missing toolchain | Finalized as IE at once; retrying would not help |

Each judging of a submission is an attempt with its own status, verdict and test results, and the submission shows the latest one. Phase 1 only creates attempt 1; rejudges will add more.

## Sandboxes

The worker runs submissions through a `Sandbox` interface with two providers:

- **`local-isolate`** uses [isolate](https://github.com/ioi/isolate) with cgroups v2: separate PID, network, mount and IPC namespaces, a read-only view of `/usr`, `/bin` and `/lib`, and cgroup limits on memory and processes. Production runs this. See [judge-setup.md](judge-setup.md).
- **`local-unsafe`** runs the program as your own user with rlimits, a memory watcher and a process group it can kill. It exists so you can judge on macOS or a Linux machine without cgroups v2. It is not a sandbox: the worker only starts it with `VALENCE_ENV=dev` and logs a warning.

`JUDGE_PROVIDER=auto` (the default) uses isolate when it works and falls back to local-unsafe in dev.

## Adding a language

Add an entry to [`judge/languages.yaml`](../judge/languages.yaml) and install the toolchain in [`judge/Dockerfile`](../judge/Dockerfile). Then add an `ac` solution for it under `problems/examples/aplusb/solutions/` (and the extension to `solutionExt` in `pkg/problempkg`) so the judge tests cover it. Workers only offer languages whose compiler they can find.

## Metrics

Both services serve Prometheus metrics: the API on its internal port at `/metrics` (RPC counts and latencies, queue depth), the worker on `WORKER_ADDR` at `/metrics` (busy slots, verdicts, judge duration, infrastructure errors).
