# Runtime logging and request correlation

[简体中文](runtime-logging.zh-CN.md) | [Follow-on product design proposal](runtime-log-product-design.md) | [Documentation index](README.md)

## Optional file output

Stdout remains enabled. Set `GATEWAY_LOG_FILE_DIRECTORY` to an absolute, writable deployment directory to additionally save the exact same redacted NDJSON. It is unset by default; no files are created in that mode. An explicitly enabled file output that cannot initialize fails startup before business resources open.

| Variable | Default | Accepted values |
| --- | --- | --- |
| `GATEWAY_LOG_FILE_MAX_BYTES` | `104857600` (100 MiB) | 1–1073741824 bytes |
| `GATEWAY_LOG_FILE_MAX_BACKUPS` | `10` | 0–100 sealed segments |
| `GATEWAY_LOG_FILE_MAX_AGE` | `168h` (7 days) | positive Go duration or integer seconds, at most 365 days |

These configurable defaults are conservative starting values, not production capacity estimates. Each output creates a private `gateway-*` session directory (0700) containing `active.ndjson` and numbered sealed segments (0600). Rotation happens before a complete event would exceed the byte limit; an event larger than the limit is rejected instead of fragmented. Each accepted write calls Sync; rotation syncs and closes the old file before rename/reopen, and normal shutdown flushes/closes after runtime resource cleanup. A failed partial write is rolled back; rollback failure stops further file writes to that segment. Sync reduces the loss window but does not promise zero loss on power failure or SIGKILL, or directory-entry durability after a filesystem crash.

Backup and age limits apply **only to sealed segments created by this output session**. Cleanup runs on writes and explicit flush/close, leaves the active file intact, refuses replaced files, and never scans/adopts/deletes previous sessions or user files. Previous sessions remain for separate retention/history work: the configured parent directory therefore has no global size/age cap across restarts. File persistence does not add Console history, cross-session cursors, or cluster search; the existing administrator-only `scope=local` API remains memory-backed.

File writes and Sync are synchronous and can delay a logging caller; there is no queue or disk I/O deadline. Both the existing stdout/buffer destination and file are attempted even when either fails. Runtime file errors preserve the default destination, return an error to the handler, and report a redacted failure/count via independent stderr at most once per minute; close errors are also reported separately. Failed records are not persistently retried. Gateway does not change its running exit code for these optional-output failures. Deployments own volume permissions, capacity monitoring and old-session retention; the read-only nonroot Kubernetes image needs an explicitly provisioned writable mount, and `/tmp` is not durable storage. This change provisions no volumes or credentials and deploys nothing.

## Optional OTLP Logs output

Set `GATEWAY_OTLP_LOGS_ENDPOINT` to additionally export the same redacted events as standard **OTLP/HTTP protobuf Logs**. It is unset by default, independent of the trace setting `GATEWAY_OTLP_HTTP_ENDPOINT`. Files and OTLP may be enabled separately or together; stdout stays enabled. No receiver, log database, volume or credentials are provisioned.

| Variable | Default | Accepted values |
| --- | --- | --- |
| `GATEWAY_OTLP_LOGS_ENDPOINT` | unset (disabled) | HTTP(S) URL without user credentials, query or fragment; an empty/root path becomes `/v1/logs`, a custom path is preserved |
| `GATEWAY_OTLP_LOGS_HEADERS` | empty | JSON object of string header values; each value at most 4096 bytes, with transport/content headers reserved |
| `GATEWAY_OTLP_LOGS_QUEUE_SIZE` | `256` | 1–1024 unconfirmed records, including queued and in-flight records |
| `GATEWAY_OTLP_LOGS_TIMEOUT` | `3s` | Go duration or integer seconds, 1 ms–30 s per export including retries |
| `GATEWAY_OTLP_LOGS_SHUTDOWN_TIMEOUT` | `5s` | Go duration or integer seconds, 1 ms–10 s for each flush and shutdown attempt |

These are configurable starting values, not measured deployment capacity. The private Logs provider uses the official exporter and batches at most 64 records, normally every second. It neither changes the global tracing/logging providers nor inherits `OTEL_EXPORTER_OTLP*` exporter configuration. Shared `OTEL_RESOURCE_ATTRIBUTES` metadata uses the same sensitive-name redaction policy while preserving safe attributes; malformed syntax or encoding fails initialization with a safe error before SDK diagnostics. Fixed Gateway service/instance/session identity takes priority. HTTPS verifies certificates using the private HTTP client's system trust; private CA deployments must provide that trust through the process's system certificate configuration. There is no insecure TLS mode or redirect following. Header credentials stay in the request headers, outside log fields and fallback diagnostics.

Timestamp, severity, body and a valid trace ID use native OTLP fields; ordinary correlation fields and nested groups remain attributes. An absent/invalid trace ID remains an attribute without fabricating a span ID. Signed int64 values remain exact; wider integers or numbers outside float64 are preserved as text. OTLP admission accepts one complete NDJSON object at most 64 KiB with at most 32 nested attribute levels; it rejects oversized or invalid events instead of truncating them. These limits do not change stdout, file or local-buffer admission.

A successful write acknowledges admission, **not delivery or durable receiver storage**. Admission does not wait for network delivery: capacity exhaustion rejects a new event with safe counters while retaining admitted events. Direct writes during explicit protocol cleanup are also rejected promptly, preventing SDK cleanup locks from blocking admission or shutdown; the runtime output already serializes writes and cleanup. The official exporter retries transient failures within the export timeout, respecting `Retry-After` seconds or HTTP dates; a requested delay beyond that budget prevents retry. Permanent errors, invalid responses and partial success are visible as failed batches; a partial batch is conservatively counted entirely as unconfirmed and is not retried. A nonempty success response must use protobuf, while an empty serialized success is accepted. Failed batches release capacity but are not persistently requeued, and later success does not erase earlier failure evidence. An expired shutdown budget cancels active requests and conservatively settles all remaining reservations as unconfirmed, including records the SDK discards without exporting. SIGKILL and receiver outages can also leave records unconfirmed. There is no durable retry journal or zero-loss promise.

All configured destinations are attempted even if another fails. OTLP diagnostics use independent redacted stderr at most once per minute and include export failures, unconfirmed, rejected, capacity-rejected, pending and cleanup-failure counts; raw receiver errors, bodies and credentials are excluded. Initialization failure stops startup before business resources open; a runtime delivery or cleanup failure preserves the runtime exit code. Shutdown happens after existing business cleanup, first flushing then shutting down the Logs provider and releasing its private idle HTTP connections. Each protocol cleanup has its own configured budget (default up to 5 s + 5 s); this is not a total process deadline, and synchronous stdout/file I/O remains separately unbounded.

OTLP is an export protocol, not a log-query API. Receiver storage, retention and search require deployment configuration and a separately designed Gateway query adapter. `GET /api/v2/runtime/logs` still queries only the current process's memory under its existing administrator/password-change gates. Synthetic receiver tests and CI do not replace multi-node, restart and outage deployment acceptance.

## Shared events and local queries

The private session directory is exclusively managed by its output instance; administrators and same-UID/root tools must not concurrently replace its paths. Identity checks reject replacements observed before rotation or retention, but are not filesystem transactions against concurrent privileged path mutation.

Gateway runtime events are newline-delimited JSON on stdout. Each record has `time`, `level`, `msg`, `instanceId`, `sessionId`, `component`, `operation`, `requestId`, and `traceId`. A process event without request context has empty correlation IDs. HTTP access events add `method`, the server-registered route template (or `unmatched`), request class, status, and duration in milliseconds. The template is captured at router dispatch, independent of a handler changing `r.Pattern`; compatibility aliases retain the outer `/repository/` template. Their request class reuses the resolved Maven/Raw/npm/PyPI/Go classification already used by metrics, including when metrics are not configured. An unresolved repository does not infer its class from its name. The shared class mapping also supports Cargo; Cargo retains its native `/cargo/` root with no Nexus alias. Raw URL paths, resolved parameter values, query strings, request bodies, `Authorization`, and `Cookie` are never access-log fields. Attributes named like secrets, credentials, tokens, bodies, URLs, queries, or errors are redacted before output.

Runtime logging has an explicit output lifecycle: writes, flushes, and closes are serialized, and close attempts both cleanup callbacks once even if flush fails. The default stdout and memory buffer are borrowed destinations: close is a no-op that does not wait for writes, flush, sync, or close stdout. Configured runtime errors and graceful shutdown finish existing HTTP and resource cleanup before closing log output. Worker cancellation remains context-based; resource-owning destinations will need their own bounded cleanup and delivery behavior.

Each HTTP response carries `X-Request-ID` and `X-Trace-ID`. A safe client request ID can be reused; an absent or unsafe ID is replaced with a generated ID. The same IDs are stored on audit records written during that request, including management operations. Audit rows remain in PostgreSQL as operation evidence; runtime JSON is not written to a business table. Lifecycle job claim, completion, and failure events use the job ID as `requestId`. Webhook delivery attempts use the delivery ID. Operators can locate these worker events from the corresponding job or delivery record.

`GATEWAY_ACCESS_LOG=limited` is the default: only 5xx responses and requests slower than `GATEWAY_ACCESS_LOG_SLOW_MS` (default `1000`) emit access records. Set `GATEWAY_ACCESS_LOG=full` to log every request. The slow threshold must be a positive integer no greater than 60000 milliseconds. The policy limits log volume; it does not disable audit records.

System Runtime → Runtime logs calls `GET /api/v2/runtime/logs` against the connected Gateway process's memory buffer. `GATEWAY_LOG_BUFFER_LINES` defaults to 1000 and accepts 0–5000; zero disables queries with HTTP 503. Only platform administrators can query. Filters include a time window of at most 24 hours, instance, level, component, request ID, trace ID, and keyword, with at most 100 results per page and a two-second execution timeout. Audit record correlation IDs link to this panel. Responses identify `scope=local`, `instanceId`, and `sessionId`; requesting another instance returns 503. The buffer is lost on process restart. File persistence and protocol export do not change this endpoint. File history and cross-node queries require separately designed readers or search adapters; see [output and history design](runtime-log-product-design.md).

Console keeps keyword, time range, level, exact component and Request ID visible; instance and Trace ID are under More filters. Recent 15-minute, one-hour and 24-hour ranges use a rolling duration. `windowSeconds` accepts 1–86400 seconds and is mutually exclusive with `from` and `to`; omitted bounds still default to one hour. Both rolling bounds are calculated from the same locked snapshot on every query and follow read, so pause/resume or a long-open page cannot lengthen the span. The cursor binds the duration, and changing the time range or any filter resets cursors and pauses follow before a new snapshot.

Custom bounds are fixed snapshots. Console normalizes the picker's visible second precision, validates order, the 24-hour span and the five-minute future limit at submission, then serializes UTC ISO dates. Choose a recent range to follow new events; fixed snapshots do not pretend to include events after their end. Client validation supplements the same server limits. Invalid API windows still return `invalid_time_window` with no records or cursor; Console shows recovery guidance and preserves the diagnostic code. Copy/export remains limited to already loaded results.

### Safe diagnostic projection

The local API projects only the redacted core fields and a bounded top-level
allowlist: HTTP `status` (100–599), `durationMs` (0–86400000), standard HTTP
`method`, the known `requestClass`, `route` (a template registered by this server,
at most 256 ASCII bytes, or the fixed `unmatched` value), an opaque ASCII `jobId` (at most 128 bytes),
and `attempt` (0–1000000). Missing, incorrectly typed, out-of-range, redacted or
nested diagnostic values are omitted without dropping the event. This does not
open raw attributes, request paths/URLs, headers, bodies or error payloads.
Unregistered or malformed top-level route values are excluded from keyword
matching. Console rows, copy and NDJSON retain the same safe template. Trace IDs
remain correlation values; no collector-backed trace navigation is introduced. The same
sensitive-name and ancestor-group redaction runs before stdout, buffer search
and this projection.

Every successful page includes `source`: the INFO collection floor, actual
limited/full access mode and slow threshold, buffer capacity, 16 KiB line
admission bound, and up to 100 exact component names observed in the retained
current instance/session. `componentsTruncated` identifies a capped component
list. Names are observations, not supported wildcard filters or cross-node
worker aggregation. An empty filtered page does not establish why an event was
not emitted. Selecting DEBUG does not enable DEBUG collection. Disabled buffers
return `log_buffer_unavailable` (503); missing runtime identity returns
`log_source_identity_unavailable` (503). Administrator and forced-password-change
gates apply before source availability or cursor validation. Responses use
`Cache-Control: no-store`.

### Session-bound incremental queries

The default query and `beforeSequence` continue to return descending sequences
for history. An initial/history page supplies `afterCursor` at the locked
snapshot tail, for following events arriving after that snapshot. A follow
request sends `afterCursor`, receives ascending sequences, and must continue
the returned cursor while `hasMore` is true. A full page advances only through
positions actually scanned; it never jumps to the latest sequence while
matching unread pages remain. A short page also consumes nonmatching scanned
positions. Newly appended records after the snapshot remain for the next read.
The omitted time bounds roll over the last hour at each snapshot; explicit
bounds remain fixed and must obey the 24-hour query limit. Ordering and that
limit are checked again at the locked snapshot; if waiting for a writer makes
the rolling bounds invalid, the query returns `400 invalid_time_window`
without entries or a cursor.
The OpenAPI Problem code enum includes the log query's validation, disabled
buffer, source identity, remote scope, session change, timeout and forced
password-change errors; clients can distinguish these declared responses.

The opaque cursor is signed with an ephemeral buffer key and bound to the
instance, session and filter specification. Preserve the same filters between
follow reads; changing the page limit is allowed. Filters changing or malformed/
modified cursors return `invalid_cursor` (400). `beforeSequence` and
`afterCursor` cannot be combined. A cursor from a different instance or restarted
session returns `log_cursor_scope_changed` (409), requiring a fresh query; an
explicit request for another instance still returns 503. Do not reuse an older
history page's tail as the active follow cursor.

`retention.earliestSequence` and `latestSequence` describe the whole memory ring,
independently of query filters (both zero when empty). `retention.gap` is true
only when unread sequence positions were overwritten before a forward read;
it does not claim those events matched the filters. Time/level/component gaps
between returned records are not loss. Reset/restart is separately reported by
the cursor scope error. The two-second query budget also bounds waiting for a
contended writer. These cursors do not add files, durable history, other nodes,
stdout readers, worker aggregators or a zero-loss delivery promise.

The bundled Compose deployment uses Docker's `json-file` logging driver with `GATEWAY_LOG_MAX_SIZE` (default `10m`) and `GATEWAY_LOG_MAX_FILES` (default `5`). These bounds apply per container; recreating a container can remove its local history. For a cluster, collect stdout/stderr from every API, scheduler, and worker pod with the deployment's log collector. Configure the collector's durable storage and retention policy outside Gateway, preserving searchable `instanceId`, `sessionId`, `requestId`, and `traceId` fields. Do not use these high-cardinality fields directly as index labels in label-indexed backends such as Loki. Kubernetes node log rotation alone is insufficient for queries after a pod is replaced. Keep collector credentials outside the browser and outside log fields. Verify retention by querying one request ID after a pod restart and by querying a worker event from another node.

### Console reading, follow and bounded export

System Runtime → Runtime logs presents literal, monospace rows with time, textual
severity, process/session, exact component/operation, message and allowed
request/job diagnostics. Dark/light palettes preserve severity text. Wrap lines
can be disabled for horizontal viewing inside the message field. ANSI, control
and bidirectional characters are escaped as visible text; HTML is not executed.
Each displayed field is capped at 4096 characters, with an ellipsis when shortened.

Search applies the time, instance, severity, exact component and correlation
filters together; Clear filters also removes audit-link request/trace filters.
The component suggestions are the API's observed names, not wildcard groups:
`worker` does not query every worker. The source line reports the actual INFO
floor, limited/full policy, slow threshold, ring capacity and retained sequences.
A DEBUG filter does not enable DEBUG collection; an empty result cannot explain
why an event was not emitted. Separate processes and file/OTLP history remain
outside this view.

Follow new logs starts from the snapshot's incremental cursor. Load older logs
keeps that live anchor separate. Reads are sequential, normally every three
seconds; unread pages drain no faster than once per second, with at most 100
records per read and a five-second browser request budget. Pause retains the
consumed cursor, and hidden tabs suspend polling. Resume continues after that
cursor; new matching events may already have expired from the server ring, which
is reported as a server retention warning. Reading above the bottom preserves
scroll position and counts newly fetched unique records as unread. Resume at
bottom scrolls to the latest loaded row and continues following. The unread
count does not estimate events that have not been fetched.

Changing filters replaces the snapshot and cursors. A session/instance change
clears previous records and requires a new snapshot; an ordinary failed refresh
retains the loaded rows with an error. Authentication/password-change failures
clear protected rows. Restart never mixes records from the prior session.

The client retains only the latest contiguous tail of at most 300 unique rows
and 1,048,576 UTF-8 bytes of projected NDJSON, including line delimiters. Client
capacity trimming has its own notice and does not claim server loss. Older
history loading stops at the client capacity boundary. Single-row copy uses the
safe JSON projection. Copy loaded and Download NDJSON export only the current
filters' loaded records, capped by those same limits; they do not fetch all
history. Unknown response attributes are excluded. Copy selection preserves the
selected visible text up to 1 MiB of UTF-8; larger selections are explicitly
rejected with a warning rather than silently truncated. Display shortening and
local trimming do not reconstruct records beyond these limits.
Replacement snapshots, filter/session changes and authorization clearing also
invalidate the text selection. Copy checks the current native selection stays
inside the log stream, so a stale selection cannot export previous records.
