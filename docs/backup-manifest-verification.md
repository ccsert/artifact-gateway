# Offline backup manifest verification

[简体中文](backup-manifest-verification.zh-CN.md) | [Documentation index](README.md)

This **Unreleased foundation** validates a private local bundle's bytes and
operator declarations. It is separate from `agctl` and from the existing
[pinned RustFS physical backup drill](recovery-runbook.md).

```sh
gateway preflight backup --input /private/backup/manifest.json --format json
```

The command reads files, writes JSON to stdout, and exits. It does not load
runtime configuration, contact PostgreSQL or S3, enumerate or stop writers,
export a backup, create a complete marker, restore stores, create credentials,
or change permissions, quotas or retention. Provide an immutable,
operator-controlled local bundle: concurrent changes invalidate its evidence.

## Results and consistency boundary

| Exit | Overall `status` | Meaning |
| --- | --- | --- |
| 1 | `invalid` | A supported bundle's artifact, inventory or confirmed writer interval contradicts its manifest. |
| 2 | No usable report | Invalid arguments, unavailable/nonprivate/oversized manifest, malformed or ambiguous JSON, or failed report output. |
| 3 | `unknown` | Byte verification completed, evidence is missing/unsupported, or verification was cancelled. |
| 0 | No backup result | Help only. This foundation never grants overall backup success. |

The report separates `integrity` (`verified`, `invalid`, `unknown`),
`writerEvidence` (`declared`, `invalid`, `unknown`), and `consistency` (**always
`unknown`**). `state: complete` is a producer claim. It does not prove source
enumeration or identity, the writer inventory, continuous fencing, a common
database/object snapshot, schema compatibility, or recoverability. Matching
dump bytes are not a parsed or restored PostgreSQL dump. Ledger checksums are
not independently compared with source migration files.

Even verified bytes and complete declarations return exit 3. Do not use this
result to reopen writes, delete source data, mark a backup consistent, or
authorize restore. A future exporter, trusted stop evidence and isolated
restore acceptance are separate requirements; issue #200 remains partial.

The JSON report contains `schemaVersion: 1`, opaque `backupId`, UTC `checkedAt`,
SHA-256 of the exact manifest (`manifestSha256`), reason codes and verified
object/byte/migration counts. On an unsuccessful report, counts describe only
the verified prefix, not total backup completeness. Keep reports and bundles
in controlled evidence storage. Reports omit file paths, object keys, writer
IDs, raw parser/IO errors, source coordinates and content. Use nonsecret IDs;
even an opaque ID and quantities may be private evidence.

## Manifest version 1

All JSON field names are ASCII; values preserve valid Unicode. Duplicate fields
(including case-alias collisions), unknown v1 fields, invalid UTF-8, isolated UTF-16 surrogate escapes,
trailing JSON, fractional/out-of-range integer quantities and root `null` are
rejected. The manifest is limited to 8 MiB. Missing quantities remain unknown;
only explicit zero represents an empty object set or zero-byte object. A single
ASCII field case variant retains the existing decoder's behavior; two names
mapping to the same field are rejected. The integer version envelope is read
before v1 body rules, so unsupported versions with future fields/body types
remain unknown. Common JSON syntax/ambiguity and size limits still apply.
Explicit negative v1 object totals are invalid, rather than missing evidence.

| Field | Required contract |
| --- | --- |
| `schemaVersion`, `backupId`, `state` | Version 1; opaque identity; `complete` declaration. Unsupported versions/incomplete state return unknown. |
| `startedAt`, `completedAt` | Nonzero RFC3339 timestamps; completion strictly after start and no later than verification. Record UTC. |
| `gateway` | `version`, 40 lowercase hex `revision`, full `imageDigest: sha256:<64 lowercase hex digits>`. These identify declared software, not proven compatibility. |
| `database` | `format: pg-custom-v1` and `file` reference to dump bytes. No connection, dump parsing or restore is performed. |
| `schema` | `format: gateway-migrations-tsv-v1` and `file` reference to the native migration ledger representation below. |
| `objects` | `format: s3-bytes-v1`, an `inventory` file reference, and nonnegative int64 `count` and `bytes` matching the streamed inventory. RustFS physical tar is a different profile and returns unknown. |
| `writers` | `scopeId`, explicit `inventoryDeclaredComplete`, and nonempty `writers` declarations described below. |

`backupId`, software `version`, scope and writer IDs contain 1–128 ASCII
letters/digits or `.`, `_`, `:`, `+`, `-`, starting with a letter/digit. Every
file reference has `path`, explicit nonnegative integer `size`, and
`sha256: sha256:<64 lowercase hex digits>`. All bytes are hashed, including
zero-byte files; size, multipart ETag and object count are insufficient.

The migration ledger is UTF-8 TSV with the exact header `filename\tsha256`.
Each subsequent row contains a native six-digit migration filename and its
64 lowercase hex checksum, separated by one tab. Names are unique and strictly
sorted; at least one migration is required. This example is synthetic:

```text
filename	sha256
000000_synthetic.sql	cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc
```

The inventory is JSONL, one object per line, strictly sorted by unique `key`.
Keys are nonempty, at most 4096 UTF-8 bytes, without NUL, CR or LF. Each
`file.path` begins with `objects/`. This digest is for the synthetic bytes `abc`:

```json
{"key":"synthetic/a","file":{"path":"objects/one.bin","size":3,"sha256":"sha256:ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"}}
```

Ledger and inventory files are each limited to 64 MiB, with lines shorter than
64 KiB. Object files use fixed-size read buffers. Cancellation is checked
between local reads; it cannot guarantee interruption of a blocked filesystem
or hostile concurrent file replacement.

## Writer declarations and filesystem boundary

Each writer has `id`, `stopConfirmed`, `stoppedAt`, and `confirmedThrough`.
IDs must be unique. A confirmed writer must have stopped no later than the
backup start and declare continuous stop through backup completion, no later
than the check. Missing scope/inventory confirmation, missing writers or an
unconfirmed writer leave `writerEvidence: unknown`. Duplicate identities or
contradictory confirmed intervals are invalid.

These are operator assertions. No authenticated process check, distributed
fencing token or external writer discovery is performed. API, Worker and all
other actual writers must be accounted for by the eventual producer, including
writers outside one Compose project.

The manifest's parent directory is the bundle root. On the supported POSIX
baseline, root and files must grant no group/other permissions (typically 0700
and 0600); the command does not chmod them. Manifest and referenced artifacts
must be regular files. References use normalized relative paths of at most
4096 bytes, without absolute paths, `..`, backslash, colon, NUL, CR or LF.
`os.Root` confines resolution through nested directories; final file symlinks
are rejected. This does not prove ownership, stop concurrent mutation, or make
mounted/hostile filesystems a trusted snapshot.

The existing physical drill and destructive isolated restore helper remain
unchanged. This validator does not treat their `SHA256SUMS` as a portable S3
manifest, extract archives, or modify database/object state.
