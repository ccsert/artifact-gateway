# Performance baseline

[简体中文](performance-baseline.zh-CN.md) · [Project README](../README.md)

This document is a reproducible engineering snapshot, not a production SLA or
a public-release claim. It answers three narrow questions about the lightweight
core: how large the Go deliverable is, how much memory a quiet stack retains,
and how the same stack behaves under local concurrent reads.

## Result summary

At commit `7881539a` on 2026-08-21:

- the stripped static Gateway binary was **28.88 MiB for Linux/arm64** and
  **31.83 MiB for Linux/amd64**;
- the distroless runtime image, including Gateway and its healthcheck, was
  **36.06 MiB** of uncompressed image content;
- a healthy, settled Gateway retained **53.59 MiB on average**, while the full
  Gateway + PostgreSQL + RustFS core stack averaged **382.08 MiB**;
- at 128 concurrent clients, authenticated PostgreSQL-backed metadata reads
  reached **32,562 requests/s at p95 9 ms**, with a **104.40 MiB** observed
  Gateway memory peak;
- at 128 concurrent clients, authenticated 64 KiB Raw reads through PostgreSQL
  and RustFS reached **2,964 requests/s / 186.08 MiB/s at p95 53 ms**, with a
  **103.50 MiB** observed Gateway memory peak;
- all 397,000 measured requests completed with zero ApacheBench failures, and
  Gateway remained live and ready with no `panic` or `fatal` log entries.

These numbers support a specific advantage: Artifact Gateway keeps its own Go
process small and uses PostgreSQL as its only coordination and database
dependency. Artifact bytes still require an S3-compatible byte plane; the
tested local stack uses RustFS. Redis, Kafka, Elasticsearch, and an external
message queue are not part of this core baseline. See
[PostgreSQL capabilities](postgresql-capabilities.en.md) for the locking,
queueing, notification, search, and observability primitives behind that
boundary.

## Test environment

| Item | Value |
| --- | --- |
| Date | 2026-08-21 |
| Source | commit `7881539a` |
| Host | macOS 26.5.2 (25F84), Apple Silicon arm64, Mac15,9, 128 GiB |
| Go | 1.26.6 |
| Docker | Docker Desktop 29.7.2, Linux arm64 VM |
| Docker VM allocation | 12 vCPU, 46.96 GiB |
| Container limits | No explicit CPU or memory limits |
| Topology | Gateway `standalone`, PostgreSQL 16 Alpine, RustFS |
| Network path | One host-side load generator over loopback HTTP; no TLS or ingress |

The baseline includes the three continuously resident core services. The
one-shot migration and RustFS initialization containers had exited before
sampling. Optional scanner and APT signer profiles were not enabled.

## Deliverable size

The binaries use the production Dockerfile flags:
`CGO_ENABLED=0`, `-trimpath`, and `-ldflags='-s -w'`. The runtime image uses the
distroless static non-root base.

| Deliverable | Architecture | Raw size | gzip snapshot |
| --- | --- | ---: | ---: |
| Gateway static binary | Linux/arm64 | 28.88 MiB | 9.32 MiB |
| Gateway static binary | Linux/amd64 | 31.83 MiB | 10.36 MiB |
| Healthcheck static binary | Linux/arm64 | 5.19 MiB | 2.10 MiB |
| Healthcheck static binary | Linux/amd64 | 5.57 MiB | 2.33 MiB |
| Runtime image (Gateway + healthcheck + base) | Linux/arm64 | 36.06 MiB | 13.50 MiB |

The image raw size is Docker's uncompressed content size. The 13.50 MiB value
is a local `docker save | gzip -1` transport snapshot and must not be read as an
exact registry layer size.

## Quiet memory footprint

After the core stack reported healthy for about two minutes, it settled for an
additional 15 seconds. Ten `docker stats --no-stream` samples were then taken
one second apart.

| Service | Minimum | Average | Maximum |
| --- | ---: | ---: | ---: |
| Gateway | 53.34 MiB | 53.59 MiB | 53.93 MiB |
| PostgreSQL | 122.00 MiB | 122.47 MiB | 123.00 MiB |
| RustFS | 205.40 MiB | 206.02 MiB | 206.70 MiB |
| Core stack, sum of averages | — | **382.08 MiB** | — |

This is a settled local baseline, not a minimum-memory guarantee. Database and
object-store caches, repository count, connection pools, observability, and
optional workers can change the steady state.

## Concurrent-read workloads

The test created one real Raw Hosted repository and stored a random 64 KiB
object. ApacheBench used HTTP/1.1 keep-alive and an ephemeral administrator
Bearer token. Both paths were warmed once before measurement:

- `metadata`: `GET /api/v2/repositories`, exercising authentication and the
  PostgreSQL control plane;
- `raw-64k`: `GET /raw/perf-raw/performance/payload-64k.bin`, exercising
  authentication, PostgreSQL metadata, and the RustFS byte plane.

| Workload | Concurrency | Requests | Requests/s | p50 | p95 | p99 | Transfer | Failed |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| Metadata | 1 | 5,000 | 3,479.12 | 0 ms | 0 ms | 1 ms | 0.97 MiB/s | 0 |
| Metadata | 32 | 30,000 | 12,798.80 | 1 ms | 12 ms | 16 ms | 3.58 MiB/s | 0 |
| Metadata | 128 | 300,000 | **32,562.20** | 3 ms | **9 ms** | 16 ms | 9.10 MiB/s | 0 |
| Raw 64 KiB | 1 | 2,000 | 766.33 | 1 ms | 2 ms | 2 ms | 48.10 MiB/s | 0 |
| Raw 64 KiB | 32 | 20,000 | 2,903.22 | 10 ms | 22 ms | 31 ms | 182.24 MiB/s | 0 |
| Raw 64 KiB | 128 | 40,000 | **2,964.35** | 42 ms | **53 ms** | 66 ms | **186.08 MiB/s** | 0 |

The metadata result benefits from local loopback, a small response, and warm
PostgreSQL caches. It is useful for comparison on the same machine, not as an
internet-facing capacity promise.

## Memory under 128-way concurrency

Docker resource samples were taken about once per second while the two
high-concurrency cases ran. Peaks between samples may therefore be missed.

| Workload | Gateway peak | PostgreSQL peak | RustFS peak | Sum of service peaks |
| --- | ---: | ---: | ---: | ---: |
| Metadata, c=128 | 104.40 MiB | 163.80 MiB | 219.00 MiB | **487.20 MiB** |
| Raw 64 KiB, c=128 | 103.50 MiB | 132.00 MiB | 231.70 MiB | **467.20 MiB** |

The Gateway stayed close to 104 MiB at the observed peak—roughly twice its
quiet average—while serving 128 concurrent clients. Docker CPU values exceeded
100% because the workload used multiple cores. Twenty-five seconds after the
load, caches remained warm: Gateway averaged 83.24 MiB and the full stack
averaged 443.21 MiB, so this run does not claim immediate return to cold idle.

## Reproduce the baseline

Prerequisites are Docker with Compose v2, Go 1.26.6, ApacheBench (`ab`), curl,
jq, OpenSSL, Python 3, gzip, and standard POSIX utilities.

```sh
make performance-baseline
```

The command allocates free loopback ports, creates an isolated Compose project
and fresh volumes, builds the image and both Linux architectures, runs the six
workloads, writes CSV evidence to a printed temporary directory, and removes
the isolated containers, volumes, and ephemeral secrets on exit. It does not
reuse or modify the normal `.env` or development volumes.

For a shorter script check or a retained output location:

```sh
GATEWAY_PERF_OUTPUT_DIR=./performance-results \
GATEWAY_PERF_METADATA_LOW_REQUESTS=128 \
GATEWAY_PERF_METADATA_MID_REQUESTS=256 \
GATEWAY_PERF_METADATA_HIGH_REQUESTS=512 \
GATEWAY_PERF_RAW_LOW_REQUESTS=128 \
GATEWAY_PERF_RAW_MID_REQUESTS=256 \
GATEWAY_PERF_RAW_HIGH_REQUESTS=512 \
GATEWAY_PERF_IDLE_SAMPLES=2 \
make performance-baseline
```

Changing request counts makes the output a script validation run, not a
reproduction of the published snapshot.

## Limits and next gates

This first report deliberately does not claim production sizing. It uses one
arm64 laptop, one Docker VM, one Gateway replica, warm reads, a single load
generator, and no TLS, ingress, network latency, writes, cache misses, scanner,
signer, or sustained soak.

Before treating performance as a release gate, repeat it on a controlled Linux
amd64 runner with hard resource limits; add TLS/ingress and remote storage;
measure mixed reads and writes, ranges and larger objects; run a 30–60 minute
soak; and compare one-node with split-role and multi-Gateway deployments.
