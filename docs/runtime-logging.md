# Runtime logging and request correlation

[简体中文](runtime-logging.zh-CN.md) | [Documentation index](README.md)

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

## Shared events and local queries

Gateway runtime events are newline-delimited JSON on stdout. Each record has `time`, `level`, `msg`, `instanceId`, `sessionId`, `component`, `operation`, `requestId`, and `traceId`. A process event without request context has empty correlation IDs. HTTP access events add `method`, the route pattern, request class, status, and duration in milliseconds. Raw URL paths, query strings, request bodies, `Authorization`, and `Cookie` are never access-log fields. Attributes named like secrets, credentials, tokens, bodies, URLs, queries, or errors are redacted before output.

Runtime logging has an explicit output lifecycle: writes, flushes, and closes are serialized, and close attempts both cleanup callbacks once even if flush fails. The default stdout and memory buffer are borrowed destinations: close is a no-op that does not wait for writes, flush, sync, or close stdout. Configured runtime errors and graceful shutdown finish existing HTTP and resource cleanup before closing log output. Worker cancellation remains context-based; resource-owning destinations will need their own bounded cleanup and delivery behavior.

Each HTTP response carries `X-Request-ID` and `X-Trace-ID`. A safe client request ID can be reused; an absent or unsafe ID is replaced with a generated ID. The same IDs are stored on audit records written during that request, including management operations. Audit rows remain in PostgreSQL as operation evidence; runtime JSON is not written to a business table. Lifecycle job claim, completion, and failure events use the job ID as `requestId`. Webhook delivery attempts use the delivery ID. Operators can locate these worker events from the corresponding job or delivery record.

`GATEWAY_ACCESS_LOG=limited` is the default: only 5xx responses and requests slower than `GATEWAY_ACCESS_LOG_SLOW_MS` (default `1000`) emit access records. Set `GATEWAY_ACCESS_LOG=full` to log every request. The slow threshold must be a positive integer no greater than 60000 milliseconds. The policy limits log volume; it does not disable audit records.

System Runtime → Runtime logs calls `GET /api/v2/runtime/logs` against the connected Gateway process's memory buffer. `GATEWAY_LOG_BUFFER_LINES` defaults to 1000 and accepts 0–5000; zero disables queries with HTTP 503. Only platform administrators can query. Filters include a time window of at most 24 hours, instance, level, component, request ID, trace ID, and keyword, with at most 100 results per page and a two-second execution timeout. Audit record correlation IDs link to this panel. Responses identify `scope=local`, `instanceId`, and `sessionId`; requesting another instance returns 503. The buffer is lost on process restart. A durable external collector remains necessary for cross-node and historical queries.

The bundled Compose deployment uses Docker's `json-file` logging driver with `GATEWAY_LOG_MAX_SIZE` (default `10m`) and `GATEWAY_LOG_MAX_FILES` (default `5`). These bounds apply per container; recreating a container can remove its local history. For a cluster, collect stdout/stderr from every API, scheduler, and worker pod with the deployment's log collector. Configure the collector's durable storage and retention policy outside Gateway, indexing `instanceId`, `sessionId`, `requestId`, and `traceId`. Kubernetes node log rotation alone is insufficient for queries after a pod is replaced. Keep collector credentials outside the browser and outside log fields. Verify retention by querying one request ID after a pod restart and by querying a worker event from another node.
