# syntax=docker/dockerfile:1

# Judge worker image: isolate, every toolchain in judge/languages.yaml and the judge-worker binary.
# isolate needs cgroups v2, so run it privileged with a private cgroup namespace:
#
#   docker build -f judge/Dockerfile -t valence-judge .
#   docker run --privileged --cgroupns=private -e DATABASE_URL=... valence-judge
#
# The test target runs the isolate-backed judge tests and the sandbox escape suite:
#
#   docker build -f judge/Dockerfile --target test -t valence-judge-test .
#   docker run --rm --privileged --cgroupns=private valence-judge-test

ARG GO_VERSION=1.25
ARG UBUNTU_VERSION=24.04

FROM golang:${GO_VERSION}-bookworm AS go

FROM go AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/judge-worker ./services/judge-worker/cmd/judge-worker

FROM ubuntu:${UBUNTU_VERSION} AS isolate
ARG ISOLATE_VERSION=v2.0
RUN apt-get update \
 && apt-get install -y --no-install-recommends build-essential ca-certificates git libcap-dev \
 && rm -rf /var/lib/apt/lists/*
RUN git clone --depth 1 --branch "${ISOLATE_VERSION}" https://github.com/ioi/isolate /isolate \
 && make -C /isolate isolate

FROM ubuntu:${UBUNTU_VERSION} AS runtime
ARG KOTLIN_VERSION=2.0.21
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      ca-certificates curl unzip libcap2 \
      gcc g++ libc6-dev python3 pypy3 openjdk-21-jdk-headless rustc \
 && rm -rf /var/lib/apt/lists/*
RUN curl -fsSL -o /tmp/kotlin.zip \
      "https://github.com/JetBrains/kotlin/releases/download/v${KOTLIN_VERSION}/kotlin-compiler-${KOTLIN_VERSION}.zip" \
 && unzip -q /tmp/kotlin.zip -d /usr/local/lib \
 && rm /tmp/kotlin.zip \
 && ln -s /usr/local/lib/kotlinc/bin/kotlinc /usr/local/bin/kotlinc
# Submissions in Go use the same toolchain the worker is built with. It lives under /usr, which
# isolate mounts into every box.
COPY --from=go /usr/local/go /usr/local/go
RUN ln -s /usr/local/go/bin/go /usr/local/bin/go
COPY --from=isolate /isolate/isolate /usr/local/bin/isolate
COPY --from=isolate /isolate/default.cf /usr/local/etc/isolate
RUN chmod 4755 /usr/local/bin/isolate
COPY judge/entrypoint.sh /usr/local/bin/judge-entrypoint
COPY --from=build /out/judge-worker /usr/local/bin/judge-worker
ENV VALENCE_ENV=prod \
    JUDGE_PROVIDER=local-isolate \
    JUDGE_CACHE_DIR=/var/cache/valence/tests \
    JUDGE_WORK_DIR=/var/lib/valence/judge \
    WORKER_ADDR=:8081
EXPOSE 8081
ENTRYPOINT ["judge-entrypoint"]
CMD ["judge-worker"]

FROM runtime AS test
COPY --from=build /go/pkg/mod /root/go/pkg/mod
COPY --from=build /src /src
WORKDIR /src
ENV VALENCE_ENV=test VALENCE_TEST_ISOLATE=1 GOTOOLCHAIN=local
# -p 1: the packages share isolate box 0, so they must not run at the same time.
CMD ["go", "test", "-p", "1", "-count=1", "-v", \
     "./services/judge-worker/internal/sandbox/", \
     "./services/judge-worker/internal/engine/", \
     "./services/judge-worker/internal/escape/"]
