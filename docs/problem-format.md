# Problem package format (v0)

A problem is a folder. `valence-admin problem validate <dir>` checks it without touching the database, and `valence-admin problem import <dir>` stores its tests and creates or updates the problem. The [examples](../problems/examples) are complete packages to copy from.

```
aplusb/
  problem.yaml      metadata and limits
  statement.md      the statement, in Markdown
  tests/            01.in, 01.out, 02.in, 02.out, ...
  solutions/        optional reference solutions, named after their verdict
  generator.py      optional: whatever produced the tests
```

## problem.yaml

```yaml
slug: aplusb              # URL name: 2 to 64 of a-z, 0-9 and '-'
title: A + B              # 1 to 200 characters
time_limit_ms: 1000       # 100 to 20000, CPU time per test
memory_limit_mib: 256     # 16 to 2048
checker: tokens           # exact, tokens or float:<epsilon>; default tokens
visibility: public        # public or private; default private
samples: [1, 2]           # tests shown in the statement
```

Unknown keys are an error, so a typo fails validation instead of being ignored.

Languages scale the limits: Python gets twice the time, Java 1.5 times and some extra memory. The factors live in [`judge/languages.yaml`](../judge/languages.yaml).

## Checkers

| Checker | Accepts when |
|---|---|
| `exact` | The output matches byte for byte. CRLF counts as LF, and trailing newlines are ignored. |
| `tokens` | The whitespace-separated tokens match. Use this unless spacing matters. |
| `float:1e-6` | Tokens match, and numeric tokens are within the absolute or relative epsilon. |

Custom checkers are a later phase.

## Tests

Tests are `tests/NN.in` and `tests/NN.out`, numbered from 1 with no gaps (`1.in` and `001.in` both work). Every `.in` needs an `.out`. A package may have up to 500 tests, each file up to 256 MiB. Judging stops at the first failing test.

## Solutions

Files in `solutions/` start with the verdict they should get, followed by anything: `ac.cpp`, `wa_int_overflow.cpp`, `tle.py`. The prefixes are `ac`, `wa`, `tle`, `mle`, `re`, `ole` and `ce`, and the extension picks the language (`.c`, `.cpp`, `.py`, `.java`, `.kt`, `.go`, `.rs`).

The judge tests run every solution of every example and fail if one gets a different verdict, so a solution is also a regression test for the judge.

## Importing

```sh
make seed                                          # every example
go run ./services/api/cmd/valence-admin problem import path/to/problem/
```

Import reads `DATABASE_URL` and `BLOB_DIR` like the API. Test files are stored by content hash, so re-importing an unchanged problem stores nothing new, and workers keep their cached copies.
