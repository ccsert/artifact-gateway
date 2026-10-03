# Operational signal sources, HTTP windows and capacity boundaries

[简体中文](operational-signals.zh-CN.md) · [Documentation index](README.md)

Capacity evidence needs a source, unit, observation time and scope. Repository
usage, a filesystem visible to a Gateway process, and an object-store or NAS
physical pool describe different resources. None supplies the others' free space.

## Existing signals

| Signal | Source and unit | Time, reset and interpretation | Access and scope |
| --- | --- | --- | --- |
| Repository usage and quota | Management capacity API; format-specific PostgreSQL metadata; bytes and object count | Evaluated when queried. `quotaBytes = 0` disables the logical quota. References and format accounting determine `usedBytes`; it is not a physical object-store inventory. | Existing Repository administration boundary; a Group is a view and does not own an additional pool. |
| HTTP count and latency | `/metrics`; fixed request classes and status classes; requests and seconds | Process counters reset on restart. Scrapes have their own timestamps; counters alone do not define an error-rate window or a minimum sample policy. No requests is not evidence of a healthy dependency. | Existing metrics exposure. Worker/Scheduler-only processes do not serve package or management APIs. |
| Runtime, database and queue signals | `/metrics` and administrator diagnostics; Go memory bytes, database pool statistics and queue counts | Process gauges and counters or database observations. These are not filesystem bytes or task completion evidence. | Existing endpoint boundaries; changing deployment access is a separate action. |
| Runtime nodes | Administrator runtime inventory and diagnostics; instance, startup session, roles and heartbeat | Normal shutdown marks its session offline. Heartbeat age distinguishes stale/offline records; old sessions are not the current process. | The inventory reports observed roles. Without an external expected-instance plan, it cannot prove that every intended deployment instance exists. |
| Offline capacity preflight | Explicit versioned plan and inventory evidence; logical and physical byte budgets | The report evaluates supplied evidence, not a continuous physical-capacity sampler. | Local operator workflow; see [migration capacity preflight](migration-capacity-preflight.md). |

The source implementations are [Repository capacity](../internal/repository/postgres_capacity.go),
[HTTP/runtime metrics](../internal/app/request_metrics.go),
[queue/counter metrics](../internal/app/repository_metrics.go),
[diagnostics](../internal/app/diagnostics_api.go) and
[capacity preflight](../internal/preflight/capacity.go).
Do not estimate request totals from the bounded runtime log buffer, sum logical
Repository usage into a backend pool, or treat a pending/interrupted operation as
a successful backup or migration. Those operations need their supported result
contracts and freshness checks.

## Opt-in local-capacity diagnostics

Set optional `GATEWAY_LOCAL_CAPACITY_MOUNTS` to a JSON object containing only
`temporary`, `logs` and `backups`, each mapped to an explicit absolute directory
in this process's namespace. For example, with synthetic paths:

```sh
GATEWAY_LOCAL_CAPACITY_MOUNTS='{"temporary":"/explicit/temp","logs":"/explicit/logs","backups":"/explicit/backup"}'
```

The default is empty; no system temporary, log or backup directory is inferred.
Startup validates syntax only, not existence or capacity. Input is limited to
16 KiB, each path to 4096 bytes and the selection to three aliases. Duplicate or
unknown keys, non-string/empty paths, relative/unclean paths, NUL and trailing
JSON are rejected with a fixed error that does not repeat input. An unavailable
directory does not prevent startup: its observation is unknown.

One observer lives for the process lifetime. API and standalone processes expose
its snapshot only through administrator `GET /api/v2/diagnostics`, including the
existing forced-password-change gate. Worker/Scheduler-only processes do not
gain a management endpoint. `localCapacity` is additive and optional for rolling
upgrades; the Console shows an older node's missing field as unknown. Diagnostics
use `Cache-Control: no-store`. No capacity metric, cluster-wide polling,
deployment mount, credential, quota or alert is added.

The native provider opens the selected directory without following a final
symlink and queries filesystem metadata on that directory descriptor. Linux and
Darwin require directory read permission, never raise privilege or change
permissions. Ancestor symlinks follow normal OS path resolution, so the
administrator must select a trusted path. It reads
neither directory entries nor file contents. Linux supports ext-family, XFS,
Btrfs, tmpfs and overlayfs; Darwin supports local APFS and HFS. Known network
filesystems return `unknown`; unsupported types/platforms and failed reads also
return `unknown`. A supported visible filesystem can itself be backed by a VM,
an overlay or a shared storage pool: support does not establish backend locality.

Successful measurements use the kernel's total block count and blocks available
to an unprivileged process, converted to bytes using the native allocation unit.
The Linux provider prefers `f_frsize` and falls back to `f_bsize`; Darwin uses
`f_bsize`. Zero available bytes is a valid full filesystem. Missing identity,
invalid geometry and values exceeding signed 64-bit bytes are unknown, with no
numeric capacity fields. The kernel interface is documented in
[statfs(2)](https://man7.org/linux/man-pages/man2/statfs.2.html).

| Observer state | Meaning | Capacity fields |
| --- | --- | --- |
| `available` | Valid sample no older than one minute in this observer's mount namespace | Total and available bytes, actual sample time |
| `not_configured` | No explicit path for this alias | Absent |
| `unknown` | Fixed reason such as remote/unsupported filesystem, read failure, invalid measurement, overflow, timeout or cancellation | Absent |
| `stale` | An expired successful sample exists and the current refresh cannot finish | Old sample time only; bytes absent |

The Console displays fixed aliases, process mount scope, source/unit, snapshot
check time, actual sample time, status and a fixed reason. Unconfigured/failed
capacity is unknown with absent bytes; stale samples show the old time only.
Zero available bytes appears only for a valid full filesystem. Normal refresh
failure retains the old snapshot with a warning; lost authorization clears it.

The snapshot's check time does not refresh the underlying sample time. Cached
results are reused for at most fifteen seconds before a refresh is attempted.
One snapshot has a total wait budget of at most one second, independent of the
number of configured aliases. A blocked syscall may remain outstanding: the
observer permits only one query per alias, at most three per process, and concurrent
requests share those queries. Cancellation does not create replacements for
blocked work. A failed alias does not erase a different alias's valid result.

Private kernel identity from the same descriptor can positively associate
aliases on the same visible filesystem. Only fixed aliases appear in
`SharedWith`; paths, device numbers, filesystem IDs and raw errors do not appear
in observations. This association is confined to the observer process's mount
namespace. Different or unknown identities do not prove independent physical
pools, and there is no aggregate pool total to sum.

For example, temporary and log directories on one filesystem share its reported
free space. A container overlay's capacity is the view exposed to that container,
not proof of the host's free disk, an S3 bucket's capacity or a NAS pool's capacity.
Remote backend physical capacity remains unknown until a supported source and
its deployment boundary have been chosen. No credentials or remote adapters are
introduced by this observer. It does not promise to cancel a kernel syscall or
wait for it during shutdown; process exit releases its OS resources.

## Current-process HTTP 5xx observation

Administrator diagnostics and the Console report optional `httpErrorRate` for
the responding API/standalone process. The observation reuses
`artifact_gateway_http_requests_total`: the ten fixed business classes
`management`, `oci`, `maven`, `raw`, `conan`, `npm`, `pypi`, `go`, `cargo` and
`other`. All recorded 1xx–5xx statuses contribute to requests; only 5xx contributes
to errors. Health and metrics probe classes and the `other` status bucket are
excluded. This is recorded handler HTTP status, not confirmation that a client
received a complete artifact. It does not add labels, change metrics access,
aggregate nodes, infer dependency health, or trigger alerts.

One in-memory observer samples the same process counters every 15 seconds and
stops with the API runtime context. It retains at most 22 samples; diagnostics
reads never sample or advance the sample timestamp. No remote query, credential,
filesystem operation or monitoring database is involved. Worker/Scheduler-only
roles do not gain this management endpoint. Administrator and forced-password
gates and `Cache-Control: no-store` remain in effect. Older nodes without the
optional field display unknown.

The target window is 300 seconds. The latest boundary at or before 300 seconds
before the last sample is used, without interpolation; sampling alignment can
produce an actual 300–330-second span. `windowStart`, `windowEnd` and
`coverageSeconds` disclose that span. Startup/reset observations can report
safe counts for a partial span while the ratio remains absent. A single baseline
has no counts, rather than a made-up zero. Cumulative traffic before observation
is excluded. The ratio becomes available only with a complete window, a last
sample no older than 30 seconds, and at least 20 requests. Twenty requests is a
display minimum, not a statistical confidence guarantee or a production policy.

| Data state/reason | Counts | Ratio |
| --- | --- | --- |
| `available` | Safe actual-window requests and 5xx errors | Errors / requests, including a valid zero |
| `unknown`: `no_traffic` / `low_sample` | Complete-window zero / 1–19 requests | Absent |
| `unknown`: `warming_up`, `session_changed`, `counter_reset`, `sampling_gap`, `clock_invalid` after a new baseline | Safe partial-window counts when a second sample exists | Absent until a complete window |
| `stale`: `sample_stale` | Last safe counts with their old sample/boundaries | Absent |
| `unknown`: `source_unavailable`, future snapshot clock, `count_overflow` | Absent | Absent |

An individual counter decrease resets the baseline even if other classes' growth
hides it in the aggregate. Session changes, backwards sample clocks and gaps
over 30 seconds also restart the window; no difference crosses these boundaries.
Missing identity is unknown. Differences over JavaScript's exact integer limit
`2^53 - 1` are omitted. Raw paths, Request IDs, error text and unbounded labels
are not part of this signal. The node/session identity binds it to this response,
not to all processes behind a load balancer.

The Console shows current node/session, counts, sample/check times and actual
window. It advances the server-reported sample age while displayed and hides the
ratio when it expires, preserving explicitly old counts. Browser UTC age also
guards delayed responses; a future browser sample clock is unknown. A normal refresh failure
keeps the old snapshot with a warning; lost authorization clears it. A positive
ratio below 0.01% displays `<0.01%`, not zero.

## Verification boundary

Observer tests use synthetic providers for shared identities, full filesystems,
remote/unsupported sources, invalid units, overflow, redaction, concurrent blocked
queries, cancellation, failed refreshes and stale samples. Native tests query
only test-owned temporary directories, reject unreadable directories,
files/final symlinks and verify a
test-owned sentinel's bytes, mode, size and modification time remain unchanged:

```sh
go test -race -count=1 ./internal/localcapacity
```

HTTP tests cover administrator/password gates, unknown byte omission, repeated
timeout resource bounds and the generated OpenAPI response. Console tests use
synthetic responses for desktop/mobile geometry, localization, unknown/stale,
repeat refresh, refresh failure and permission loss. Linux native evidence uses
owned disposable mounts; induced timeout uses a blocking synthetic provider,
not an actual stuck kernel syscall.

HTTP-window tests use controlled clocks for minimum counts, reset/session/gap,
freshness, actual boundaries, safe integers and concurrency. Real loopback HTTP
requests pass through the existing instrumentation to prove 5xx/4xx counting and
probe exclusion; actual administrator diagnostics responses are validated against
generated OpenAPI. Console tests cover bilingual desktop/mobile geometry,
data states, browser expiry and refresh/authorization behavior.

These tests do not validate a production mount, NAS/S3 physical capacity,
quota enforcement, alert policies, expected deployment instances, or
backup/migration completion. Those
remain separate acceptance work under #210. No alert, quota, deployment or
notification setting is changed by this slice. #210 remains open for the broader
signals; the capacity and HTTP-window slices close only their respective children.
