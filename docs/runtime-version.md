# Runtime version and node build identity

[简体中文](runtime-version.zh-CN.md) | [Documentation index](README.md)

The administrator sidebar shows the build version and revision of the **connected node**. Its link opens Operations → System diagnostics. `GET /api/v2/diagnostics` reports the process that answered this request, including its session ID. A load balancer may connect a later request to another process.

`GET /api/v2/runtime/nodes` lists node sessions within the retention window. Heartbeats update each session's version and revision. The inventory also shows its roles, start time, last heartbeat, and status. `currentSessionId` identifies the API session that answered the inventory request; the matching row is marked as the connected node.

During a rolling upgrade, online or stale nodes with different reported versions or revisions produce a `mixed_build` health issue with the affected session IDs. Older nodes may lack build fields; a missing identity produces `build_identity_unknown` and the Console displays “Version unknown.” Offline nodes do not participate in build consistency checks.

`releaseSource: not_configured` means that no desired-version or Release source is connected. The running version, cluster versions, and latest available Release are distinct facts; the Gateway does not claim to know the latest version without a source. The Console labels a `dev` build “Development build.”

Apply database migration `000135_runtime_node_build_identity.sql` before deploying the new process. Its fields are nullable so older processes can continue to write heartbeats during a rolling upgrade. Rolling back the process does not require dropping the new fields.
