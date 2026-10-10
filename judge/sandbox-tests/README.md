# Sandbox escape tests

Each directory here is one attack on the judge sandbox. The suite compiles and runs every case through the real judge engine with the isolate provider, then checks two things:

1. The verdict is one of the case's allowed `verdicts`. Cases that can tell whether they got out print `SAFE` or `ESCAPED`; the expected output is `SAFE`, so an escape shows up as WA and fails the suite.
2. The host is unaffected: every path in `host_absent` is missing afterwards, and the host can still start processes and create files.

Run it in the judge test image (Linux or Docker Desktop):

```sh
docker build -f judge/Dockerfile --target test -t valence-judge-test .
docker run --rm --privileged --cgroupns=private valence-judge-test
```

or with `make sandbox-test` as root on a Linux machine that has isolate. CI runs it on every pull request.

Without isolate (`go test ./...`) the suite only checks that every case is well formed. Never run the cases with `local-unsafe`: they are real fork and memory bombs.

## Adding a case

Create a directory with one source file (`main.c`, `main.cpp` or `main.py`; the extension picks the language) and a `case.yaml`:

```yaml
proves: One sentence on what the case shows the sandbox prevents.
verdicts: [AC, RE]      # verdicts that count as contained
host_absent:            # optional: paths the case tries to create on the host
  - /tmp/valence-escape-example
```

The program gets an empty stdin, 2 seconds of CPU time and 256 MiB of memory. A case that cannot tell whether it escaped should print nothing and rely on its verdict list and the host checks.
