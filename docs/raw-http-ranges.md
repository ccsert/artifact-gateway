# Raw HTTP ranges

[简体中文](raw-http-ranges.zh-CN.md) · [Protocol matrix](protocol-compatibility.md)

`GET /raw/<repository>/<path>` supports single ranges and bounded
`multipart/byteranges` for Hosted, Proxy and Group reads. Repository and member
authorization, quarantine enforcement, and verified cache admission run before
content is returned. This extends the native HTTP read contract; it adds no
management operation, Console action, schema migration, or credential type.

```sh
curl --header 'Range: bytes=0-1023,1048576-1049599,-1024' \
  --header 'If-Range: "sha256-<current SHA-256>"' \
  --user '<identity>:<credential>' \
  https://gateway.example/raw/downloads/releases/app.bin
```

The request has one `Range` field, at most 8192 bytes and 16 intervals before
normalization. Suffix and open-ended ranges are supported. Duplicate, overlapping
and adjacent intervals coalesce in ascending offset order. Valid intervals beyond
the object are omitted if another is satisfiable; malformed syntax, integer
overflow, a zero suffix, multiple field lines, no satisfiable intervals and
exceeded bounds return `416` with `Content-Range: bytes */<size>`.

Two or more resulting intervals produce `206 multipart/byteranges`. Their combined
payload must be at most 64 MiB; framing is bounded below 32 KiB. The response has
an exact total `Content-Length`, per-part `Content-Type` and `Content-Range`, and
no top-level `Content-Range`. A single resulting interval keeps the ordinary
single-range response and is not subject to the multipart payload limit.

`ETag` and `Digest` describe the complete immutable object. `If-Range` enables
partial content only when it is exactly the current strong ETag. A weak tag, date
or different validator causes a full `200` response. `HEAD` ignores Range and
returns full-object metadata without opening an object byte stream. A matching
`If-None-Match` still returns `304` before range processing.

Each interval is streamed sequentially through the existing bounded object read
port. The selected object key, size and digest are captured once; a concurrent
path update never changes later parts. Interrupted or unavailable intervals stop
the response without a closing delimiter or the advertised complete length.
Clients must treat such responses as incomplete and retry with a fresh validator.
No fallback switches to another path reference or Group member after bytes start.

A cold Proxy GET fetches and validates the complete canonical object before
applying ranges locally. It never forwards the client's Range or validators and
does not combine partial upstream objects. The existing staging-size, quota,
admission and cancellation limits still apply. A verified warm cache can serve
the same ranges while its upstream is stopped. HEAD on a cold Proxy remains a
metadata request to the selected upstream.

Validation lives in `internal/protocol/raw/multipart_test.go`, the public Gateway
Raw tests, PostgreSQL/RustFS cross-instance integration, and
`make native-raw-e2e`. The native gate records the exact curl executable version
and digest, verifies a 32 MiB fixture and every multipart segment, exercises
conditional writes and resumable completion, and uses only its owned local
target. These are isolated client and storage checks; deployment acceptance is
tracked separately.
