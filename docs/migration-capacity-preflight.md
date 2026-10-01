# Migration capacity preflight

[简体中文](migration-capacity-preflight.zh-CN.md) | [Documentation index](README.md)

The Gateway binary provides an offline budget command for a fixed, normalized
migration inventory. This addition is under `Unreleased`; it is not part of the
already published v0.5.0 binaries. It uses the existing operations preflight
entry point, independently of the planned `agctl` CLI.

```sh
gateway preflight capacity --input plan.json --format json
```

The command reads one regular JSON file (maximum 8 MiB), writes a JSON report to
stdout, and exits. It does not load Gateway runtime configuration, contact the
source or target, start services, reserve capacity, set quotas, upload artifacts,
delete data, or apply retention rules. Acquisition and verification of evidence
are separate operator steps. No new management HTTP route is introduced.

## Outcomes and evidence

| Exit | `status` | Meaning |
| --- | --- | --- |
| 0 | `sufficient` | All relevant quantities are known and fit this evidence snapshot. |
| 1 | `insufficient` | At least one known independent constraint is exceeded. |
| 2 | No report | Invalid arguments, unreadable/oversized input, malformed or ambiguous JSON, unknown fields, or failed report output. |
| 3 | `unknown` | Evidence is incomplete, inconsistent, expired, unsupported, or cancelled. |

An independently known failure takes precedence over another dimension's
unknown result. Missing numbers serialize as `null`, not zero. All byte values
are nonnegative signed 64-bit **integer bytes**, not decimal GiB values; decimal,
out-of-range JSON numbers, duplicate JSON fields (including case aliases) and
trailing JSON values are rejected. Field names are ASCII; values may be UTF-8.
Negative values or arithmetic overflow leave the affected total unknown.

`knownMinimumProjectedBytes` and `knownMinimumFreeBytes` retain proven
nonnegative contributions. They are lower bounds, not substitutes for missing
totals: the complete total stays `null`, and a bound that fits cannot produce
`sufficient`. A bound already exceeding the known quota or available bytes still
produces `insufficient`. If that bound itself exceeds int64, it is `null` but
exceeds every representable finite limit. Verified, already-counted logical
references cannot sum to more than the capacity snapshot's `usedBytes`; such
contradictory repository evidence returns unknown.

The report includes `schemaVersion: 1`, UTC `checkedAt`, `inventoryId`, the
SHA-256 of the exact input file (`inputSha256`), sorted per-repository results,
and a separate storage result with the peak components. Object keys, digests,
file paths, parser excerpts and dependency credentials are not echoed. Keep
the input file, its checksum and report in the controlled migration evidence
directory; use opaque, nonsecret IDs. `inventoryId`, repository IDs, target ID
and pool ID use 1–128 ASCII letters/digits or `.`, `_`, `:`, `-`, starting with a
letter/digit. Logical/object keys are nonempty, at most 4096 bytes, without NUL,
CR or LF. Expected content uses a full `sha256:<64 hex digits>` digest.

`snapshot.targetId` must equal the planned `targetId`. All observations must
refer to that target, the planned inventory and one storage pool, and be
collected within the operator's stated evidence interval. `observedAt` must be
nonzero and no later than the check; `validUntil` must be later than the check
and observation. The operator explicitly chooses this freshness window; the
tool does not invent a validity period or authenticate the observations.

`sufficient` is a snapshot calculation, not a future-space guarantee or a
reservation. Concurrent writes, source drift and changed quotas can invalidate
it immediately. Recollect evidence before the first import write and after
target changes; existing runtime quota enforcement still decides actual writes.

## Version 1 normalized input

Start from the [synthetic example](examples/migration-capacity-plan.json).
Its fixed timestamps deliberately expire; refresh them and replace all
observations with verified values before evaluating a real batch. Missing or
unavailable evidence must remain absent/`null`/`unknown`, rather than being filled
with zero or an assumed `absent` state.

| Field | Contract |
| --- | --- |
| `schemaVersion` | Exactly 1; other versions return unknown. |
| `inventoryId`, `complete` | Fixed batch identity and explicit complete enumeration. An incomplete/unnamed batch returns unknown. This command does not enumerate Nexus or infer unlisted objects. |
| `references` | Planned repository logical references with `repositoryId`, `logicalKey`, target `objectKey`, full expected `digest`, and `size`. |
| `snapshot.repositories` | `repositoryId`, `usedBytes`, `quotaBytes` from the target capacity API. `quotaBytes: 0` explicitly means no quota, matching that API; missing quota is unknown. |
| `snapshot.objects` | Physical observations by target `key`. `state: absent` needs a verified absence; `verified` requires matching full-byte `digest` and `size`; unknown/missing observations cannot reduce growth. |
| `snapshot.references` | Repository membership observations by `repositoryId` and logical `key`, with the same states. Physical existence alone does not prove repository membership. |
| `snapshot.freeBytes` | Known available bytes in this one physical pool; it is not logical repository capacity or an S3 bucket-size measurement. |
| `peak` | Matching `storagePoolId` and explicit `downloadBytes`, `uploadBytes`, `backupBytes`, `restoreBytes`, `headroomBytes`. Each may be explicitly zero when not needed; omitted values are unknown. |

Read the existing capacity API with the repository-read authority already
required by that API. For example, save only the fields needed by the plan:

```sh
curl --fail --silent --show-error \
  -H "Authorization: Bearer $GATEWAY_READ_TOKEN" \
  "$GATEWAY_URL/api/v2/repositories/$REPOSITORY_ID/capacity" \
  | jq '{repositoryId, usedBytes, quotaBytes}'
```

Record capture time and target identity yourself. A permission error, a failed
GET or an unavailable physical-space measurement is missing evidence, not zero.
The offline command makes no authenticated requests of its own. `verified`
means prior full-byte SHA-256 verification, not just HEAD, size, HTTP 2xx, S3
multipart ETag or a manually asserted digest copied from the inventory.

## Logical growth and physical peak

Logical growth is summed once per `(repositoryId, logicalKey)`, according to
the existing format's capacity contract. Duplicate tag-derived references to
the same logical OCI blob count once within a repository; the same blob newly
referenced by another repository still consumes that repository's logical
quota. An already verified repository reference contributes zero new logical
bytes. Proxy cache use, aliases and other formats need their own correct
normalized inventory; unknown mappings cannot be used to claim a complete plan.

Physical growth is summed once per **planned target object key in this pool**.
Only a matching verified physical object contributes zero new physical bytes.
The same digest at different physical keys is counted separately: OCI blob
keys can be shared, while native OCI manifest keys include repository and image
name, and Maven publication keys must not be assumed globally deduplicated.
Conflicting repeated logical or physical identities return unknown. Duplicate
snapshot observations for a relevant identity are ambiguous and return unknown.

The storage requirement is:

```text
unique new physical bytes
  + simultaneous download/spool bytes + simultaneous upload bytes
  + additional backup copy bytes + isolated restore copy bytes + headroom bytes
```

The temporary budgets include the chosen parallelism. Existing used storage is
already excluded from available `freeBytes`, so it is not added again. These
components conservatively coexist in one pool. If object storage, spool and
backups are on different filesystems, aggregate one proven common pool or
report unknown; version 1 does not model multiple pools or discover S3 physical
free space. A missing/unverified object's bytes are not treated as zero.

This slice supports normalized evidence only. Source collectors, importers,
live target rechecks, transfer reservations, general `agctl` commands, quota
changes and cleanup are separate work. It does not redefine `objectCount` as a
component count or change the existing capacity and authorization contracts.
