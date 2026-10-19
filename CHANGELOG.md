# Changelog

[简体中文](CHANGELOG.zh-CN.md) | [Documentation index](docs/README.md)

All user-visible changes to Artifact Gateway are recorded in this file.

Artifact Gateway follows semantic versioning. Pre-1.0 releases are usable
distributions whose contracts can still evolve. Changes are collected under
`Unreleased`; a release moves them to a dated version heading without rewriting
their meaning.

## Unreleased

- Official Conan 2.21.0 uploads now publish complete recipe/package revisions through the existing durable sessions (#312). The canonical remote root is `/conan/<repository>`; the former version-before-name layout returns 404. Manifest, package identity and every asset are verified before atomic visibility, correlated audit and scan scheduling. Interrupted uploads resume after PostgreSQL/object-store restart; scoped tokens use independent `GATEWAY_CONAN_TOKEN_SECRET` and revalidate machine credentials. Group/Proxy writes are rejected. See [publication limits and verification](docs/conan-native-publication.md).

- PyPI supports file yank/unyank and atomic version operations with strong revisions, repository write grants and transactional success audit (#311). HTML/JSON Simple, Proxy revalidation, Group authority, lifecycle and backup preserve the declaration without changing archive bytes. Console operators can choose files, enter reasons and restore; pinned pip 26.2 tests range skips and exact-pin warnings. See [PyPI file yanking](docs/pypi-yank.md).

- npm Hosted supports native deprecate/undeprecate and single-version or whole-package unpublish (#309, #310). Package revisions fence concurrent metadata and publication, mutations commit with actor/version audits, and Group tombstones prevent lower-priority members from reviving withdrawn identities. Existing management restore verifies the retained object and durable collection fence; a partial S3 reclaim cannot restore missing bytes. Tarballs and published identities remain immutable. Console shows warnings, permits Hosted writers to edit them and administrators to unpublish; see [npm version governance](docs/npm-version-governance.md). Official npm 11.6.2 acceptance verifies all four scoped/unscoped tarballs before and after recovery.

- `agctl publish raw --resumable` completes native offset-checked chunk uploads and resumes a returned `--upload-id` (#132). Lost PATCH responses are settled through status before replay, and a lost completion requires exact digest/size read-back; uncertain results retain recovery identity and exit 3. Raw batches share one deadline. Real Raw/Maven publishing also verifies target identity, active Hosted format and advertised publish capability before writing.

- Raw Hosted, Proxy and Group support bounded multipart byte ranges (#314): up to 16 intervals, coalesced before streaming, and at most 64 MiB of multipart payload. Every interval reads the captured immutable object, even when its path moves; HEAD ignores Range, and If-Range requires the current strong ETag. Cold Proxy reads verify the complete canonical object before applying ranges. See [Raw HTTP ranges](docs/raw-http-ranges.md).

- `agctl email delivery wait` now fails with exit 1 and `delivery_failed` when the delivery is `dead` (#406). Its failure envelope retains the complete delivery record; accepted still exits 0, and timeout still exits 8 without replaying or canceling the queued work.

- Maven / OCI Proxy rejects responses received after the complete request deadline or after cancellation, even when the runtime has not delivered the deadline notification yet (#403). Rejected response bodies are closed and their transport resources released, and their upstream trace records a safe error; timely bodies remain readable until the caller closes them.

- Artifact management handlers can be constructed with independent format query and publishing capabilities (#396). Authorized operations needing an unconfigured capability return `501/not_supported`, now explicit in OpenAPI; missing search or Group browse dependencies no longer panic or appear as empty inventories. Account and repository permission checks retain precedence, and unavailable global session metadata stays hidden from ordinary callers.

- `agctl repo connect` now emits the native `/repository/maven/<repository>/` endpoint for Maven Hosted reads and publication (#130). Generated deployment snippets are exercised by a real POM PUT and byte read-back; Proxy guides retain `/maven/<repository>/`.

- Console administrators can bind explicit Nexus Hosted mappings to real native targets and control durable migration batches with operator capacity evidence, CAS and idempotent requests (#383). Scan, source verification, copy, target read-back and visible identities remain separate; full hashes, endpoint adjustments and incomplete inventory appear in observation reports. Cold-client acceptance, a final catch-up after source freeze and production cutover stay unverified until separately established. See [Console Nexus migration](docs/nexus-migration-console.md).

- Normalize equivalent percent-encoded Nexus asset paths after matching decoded paths against safe metadata (#381). Maven release names containing `+` now accept `%2B` and `%2b`; origin, path-traversal and secret-bearing URL restrictions remain enforced.

- Add administrator-only Nexus 3 source configuration with encrypted credentials, protected read-only discovery, release/MIXED/SNAPSHOT classification and explicit native repository mapping preview (#381); see [Nexus source discovery](docs/nexus-source-discovery.md). No byte migration or target provisioning is performed.

- Raw memory fixtures now share public storage-contract tests with PostgreSQL (#278, [ADR 0010](docs/adr/0010-memory-store-contracts.md)). Their default read/reclaim batches are bounded at 100, collected bytes restore as not found, and contended locks honor cancellation. Memory remains an explicit test fixture; it does not prove PostgreSQL transaction or recovery behavior.

- Gateway HTTP composition now has a single validated `NewGatewayHandler(GatewayOptions)` entry point (#279). Missing metadata or per-format object storage fails initialization; native handlers and caches no longer create implicit memory stores. OCI and Raw storage are independently injected through the format-independent `ObjectStore` port, and unit tests and native-client fixtures explicitly share `internal/apptest` infrastructure. Internal `NewGatewayHandlerWith*`, `OCIObjectStore`, and per-format `Dependencies.Native*ObjectStore` interfaces are removed.

- Remove Nexus-style `/repository/<name>/...` aliases and version-only Go ZIP publication (#385). Old aliases return 404. Configure Maven Hosted at `/repository/maven/<repository>/`, Maven Proxy/Group at `/maven/<repository>/`, npm at `/npm/<repository>/`, PyPI upload at `/pypi/<repository>/legacy/` and Simple reads at `/pypi/<repository>/simple/`, Raw at `/raw/<repository>/`, and `GOPROXY` at `/go/<repository>`. Go Hosted publication requires `PUT /go/<repository>/<escaped-module>/@v/<version>.zip`. OCI keeps `/v2/`. Regenerate lockfiles or cached metadata containing old URLs. Nexus read-only discovery and historical migration evidence remain available; production URL cutover is a separate operation. Remove the historical API/path comparison gate and its CI/local wiring. Current source/bundle contracts and real native clients remain the acceptance boundary; historical management APIs and URLs are not frozen.

- Record the native management architecture and retirement of Nexus compatibility requirements in [ADR 0009](docs/adr/0009-native-architecture.md); enforce protocol dependency and aggregate-store boundaries through `make lint` (#277).

- Backup release verification continues to accept original binaries built under the previous module path. The module and command paths and retained build-info linker flags must belong to the same exact approved namespace; artifact digest, platform, approved archive, version, revision and complete migration-ledger checks remain required.

- The Go module path is now `github.com/ccsert/artifact-gateway`, the source repository (#347, [ADR 0008](docs/adr/0008-go-module-path.md)). Starting with the next release tag, `go install github.com/ccsert/artifact-gateway/cmd/agctl@<tag>` installs the client; `v0.9.0` and older tags declare the previous path, which never resolved, and cannot be installed this way. A module-mode install reports the tag as its version and the revision as `unknown`, because the Go toolchain records no VCS data for `pkg@version` builds. Code that imports Gateway packages must switch to the new path. `make release-version-check` now also fails when `go.mod` differs from the GitHub repository it runs in.

- npm Hosted repositories manage dist-tags after publication (#308). `npm dist-tag ls/add/rm` work against `GET /npm/<repository>/-/package/<package>/dist-tags` and `PUT`/`DELETE …/dist-tags/<tag>`, for scoped and unscoped packages. A tag can only name a visible, committed version that is neither tombstoned nor blocked by the quarantine read policy (404, or 409 for a quarantined target); tag names that parse as npm SemVer ranges are refused (including `x.x`, `x.1`, and `v*`), while ordinary digit-prefixed tags such as `2026-release` and `1-canary` remain valid, and `latest` can be moved but not removed (400). Each change requires Repository write permission and commits in one store transaction together with an `npm.dist-tag.set` or `npm.dist-tag.delete` audit record whose evidence holds the tag, the new and the previous version; repeating the current state succeeds and is audited as unchanged. On PostgreSQL a move takes the version row before the package row, so a concurrent tombstone either refuses the tag or removes it with the version. Packuments and Group merges reflect a change on the next read, and tarball bytes and version identity never change. Proxy repositories list their cached upstream tags, Groups list the tags of the first member that defines each one, and both refuse tag writes after checking Repository write permission (403 for readers, 405 for writers). Unauthenticated `DELETE` requests to npm routes now answer 401 instead of 405. Console shows every dist-tag that currently targets the selected version; it keeps tag editing in the native npm client. The native-client gate verifies the SHA-512-pinned official npm 11.6.2 archive, publishes scoped and unscoped packages to PostgreSQL/RustFS, moves tags, recreates the Gateway instance, installs tags with empty caches and compares complete tarballs, sizes and SHA-256 identities. Desktop and narrow light/dark browser checks cover long tags without page overflow.

- Raw conditional-write lists preserve commas inside quoted entity tags and accept RFC 9110 opaque tag bytes, including obs-text. A wildcard must be the sole field value; repeated or mixed wildcards return 400 without changing the path reference.

- Raw Hosted writes can be conditional (#313). `PUT /raw/{repository}/{path}` and resumable completion (`PUT ?uploadId=…&complete=1`) honor `If-None-Match: *` (create only when the path has no current asset), `If-Match: *`, and `If-Match`/`If-None-Match` entity-tag lists, accepting both the `"sha256-<hex>"` tag reads return and the `"sha256:<hex>"` tag writes return. The Gateway re-checks the condition in the same lock (memory) or transaction (PostgreSQL) that swaps the path reference, so of two writers holding the same validator exactly one succeeds; the other gets 412, the reference is unchanged, the current validator is not echoed, and a rejected resumable upload stays open. Malformed precondition lists and `If-Unmodified-Since`, which cannot be evaluated atomically, answer 400 instead of writing unconditionally. Requests without these headers keep the unconditional replacement behavior. Resumable completion now records the content object before writing its bytes, so a rejected or failed commit leaves them reclaimable.

- `agctl` preserves earlier uncertain email-test attempts when a retry is refused, verifies delivery identity/version/scenario before confirming a test or wait, and reports quota-alert event history as the newest 50-event window. Email wait introspection explicitly reports `cancelSupported: false`.

- `agctl` manages quota alerts and email notifications (#336, M2). `quota-alert list|show|create|update|delete|events` need an explicit policy for every threshold and duration (no default), start rules disabled, and send unchanged fields — including the enabled state, which the endpoint otherwise clears — back on every conditional update; enabling needs `--yes`. `email capability`, `email target list|show|create|update`, `email preview`, `email test`, and `email delivery list|show|wait|replay` keep recipients out of every output (they come only from stdin or a private file), queue tests under a reusable idempotency key with `--yes`, wait for relay acceptance within `--timeout` without pretending to cancel, and treat an initial `email_disabled`, `encryption_key_unavailable`, or `queue_full` refusal as definite only when no earlier attempt may have queued the test. `make email-compose-check` now drives these commands against owned TLS SMTP and asserts exactly one extra accepted message.

- Group reads follow Group writes (#359). An administrator of every repository a Group references can now read it — `GET /api/v2/groups/{id}`, `/members`, `/resolution`, and `/capacity` — and finds it in `GET /api/v2/groups`, which pages through the Groups such a caller may read; each request checks at most 1,000 Groups, so a page can be short or empty while `nextPageToken` continues the scan. Any other non-platform caller gets 404, as if the Group did not exist, so a Group it only partly administers stays hidden; it previously got 403. The contract for `PUT /api/v2/groups/{id}/members` now documents the response the server already returned, the whole updated Group, instead of a member list. `agctl` reports the widened read permission, and `group members set` still accepts a bare member list from an older server.
- `agctl` verifies Maven session responses against the requested repository and declared files before uploading, and confirms a lost commit only from a complete read-back of the requested session. Malformed or mismatched answers retain the idempotency key or session read-back path as an unknown outcome. Upload retries whose source file becomes unreadable also retain the earlier unknown result, and partial-upload recovery instructions preserve the original destination and file arguments.

- `agctl` publishes artifacts and signs in to private files (#132, #129, #140). `publish raw` uploads a file or directory tree to a Raw hosted repository with locally computed SHA-256 digests the Gateway verifies, skips objects whose repository copy already has the same digest, runs a bounded number of streaming uploads, reports every object with its canonical coordinate, and exits 3 when any object is not confirmed; re-running is safe. `publish maven` publishes one Maven coordinate through a publish session under an idempotency key (resumable with the same key, a committed session is never reused), with `publish create|upload|commit|status` for the separate steps; a lost commit answer is settled by reading the session back. `auth login --secret-output FILE` writes the session token to a new private file instead of a profile. Request paths are escaped once, so Raw names with spaces keep their canonical coordinates. The agent skill `.agents/skills/agctl/SKILL.md` documents the machine contract and CI and administration recipes, and a unit test fails when it names a command, flag, or argument the client lacks. The command map guard now checks native protocol rows against their own paths.

- Console section Retry waits until refetched query data reaches its components before restoring the section, preventing a stale answer from throwing again and briefly flashing the fallback.

- `agctl` completes Conan's four management views and confirmed package-revision tombstones (#131). Reference and recipe-revision views use server cursors; package IDs and package revisions report their actual unpaged scope. Deletion needs explicit confirmation, supports a read-only plan, and verifies the exact package's visible revisions after HTTP 204; uncertain acknowledgements or read-back retain an unknown outcome. Isolated CLI E2E covers literal identities, page budgets, authorization, audit and complete object bytes.

- The management API contract now documents the Conan publish session routes: `POST /repositories/{repositoryId}/conan-publish-sessions` (`CreateConanPublishSession` body: `kind` `recipe` or `package`, `reference`, `recipeRevision`, `packageId`, `packageRevision`, `objects`; 201 `PublishSession`), `GET /conan-publish-sessions/{sessionId}`, `PUT /conan-publish-sessions/{sessionId}/objects/{objectName}` (204) and `POST /conan-publish-sessions/{sessionId}:commit` (200 `ConanPublishCommit` with `state` and `revision`). The Console generated client gains their types. The routes now pass through the generated management router, so a malformed session ID answers 400 instead of 404 and the create body shares the 1 MiB management JSON limit; object uploads stay unbounded by that limit.

- Conan publish session responses (`POST /api/v2/repositories/{id}/conan-publish-sessions` and `GET /api/v2/conan-publish-sessions/{id}`) now use the same `PublishSession` shape as Maven: `id`, `repositoryId`, `coordinate`, `state`, `expiresAt` and `objects` (each with `name`, `digest`, `size`). `coordinate` is `reference#recipeRevision`, with `/packageId#packageRevision` appended for package sessions. They previously leaked capitalized store field names (`ID`, `RepositoryID`, `Publisher`, `Kind`, ...) and exposed the publisher. Clients that read the capitalized keys must switch to the contract names.

- Maven publish session responses (`POST /api/v2/repositories/{id}/publish-sessions` and `GET /api/v2/publish-sessions/{id}`) now match the `PublishSession` contract: keys are `id`, `repositoryId`, `coordinate`, `state`, `expiresAt` and `objects` (each with `name`, `digest`, `size`), and the undeclared `Publisher` and `PomObject` fields are no longer exposed. They previously leaked capitalized store field names, which left the Console Maven publish wizard without a session ID. Clients that read the capitalized keys must switch to the contract names; `agctl` is unaffected.

- `agctl` rejects password files accessible to other users before sending requests, verifies the requested identity in credential/API key/session revocation responses, and rejects incomplete service-account lists instead of inferring a false 404. OIDC identity subjects use the API's 512-character limit, including Unicode characters.

- `agctl` manages machine and human identities (#133, #335). `service-account list|show|create|update` manage CI principals (`show` reads the list, since no single-account endpoint exists, and answers 404 only after reading all of it); `credential issue` and `api-key create` write one-time secrets only to a new private file named by `--secret-output` (mode 0600, checked before the request, never overwriting an existing file or link, never printed, never retried), and report `secret_not_delivered` with the ID to revoke when the secret cannot be saved after issuing; `credential revoke` and `api-key revoke` stop a secret at once. `user list|show|create|update|delete|reset-password` read passwords only from bounded stdin or a file and keep them out of output, plans and errors; `user sessions list|revoke` end one or all sessions; `user identities list|link|unlink` manage linked OIDC identities. Conditional writes use the version read just before and are never retried. `make cli-e2e` runs the CI playbook — a service account credential delivered to a 0600 file, used, rotated (the old secret answers 401), and cut off with its grant — together with API key, user, session, and password checks, and fails if any token or password reaches agctl output.

- CLI follow streams preserve unread data at their bounds: runtime logs cap each page to the remaining event budget, expose `--after-cursor` for resuming, and identify their local process even in an empty stream. An audit poll that exceeds ten pages ends with `poll_budget` and exit 3 before advancing past unread history; its inclusive lookback resume time may replay records. Incomplete grant/template and anonymous-policy write responses report an unknown outcome with read-back guidance.

- `agctl` manages authorization and streams logs and audits (#134, #135). `grant list|upsert|delete` work one grant row at a time (upsert is retried because it is keyed to one row; delete is confirmed and never retried), `template apply` replaces a repository's grants only with `--yes`, conditional on the grant set version, and its `--dry-run` lists every grant added, removed, changed and kept; `role list|show` and `template list|show` read the catalogues; `access check` explains read, write, admin, intelligence and anonymous read for the caller or a simulated actor and exits 6 with `--require` when a permission is missing; `access anonymous show|set` reads and conditionally sets the platform switch, enabling only with `--yes`. `runtime logs` reads the answering process's memory buffer, and `runtime logs --follow` and `audit list --follow` stream NDJSON bounded by `--duration` or `--max-events`, ending with exactly one end event that names its reason and where to resume; a restarted log session ends the stream with `scope_changed`, and missed entries are reported as gaps. `agctl commands --json` gains a `follow` capability. `make cli-e2e` checks a grant through `access check` before and after removal, a template apply with a stale version, the grant writes in the audit log and its stream, and a bounded log stream.

- CLI inspection preserves its observation limits: lifecycle lists report their bounded windows without inferring completeness after permission filtering; unhealthy or unreachable scanners fail ordinary `status`; human diagnostics retains each mount's state, bytes and sample time. PyPI connection guides match `.netrc` credentials by hostname even when the server URL has an explicit port.

- `agctl` gains the repository content, audit and runtime commands (#130, #131, #135). `artifact search` searches one repository or every readable one (`--global`, reporting how many repositories it searched); `artifact browse`, `repo browse` and `group browse` walk the server's browse tree; `artifact identities`, `artifact usage`, `artifact intelligence show`, `artifact quarantine show`, `oci images|manifests` and `maven coordinates` read the format views; `artifact list|show|delete` cover Maven hosted artifacts, with `show` reporting intelligence and quarantine as recorded, none, not permitted or unavailable rather than healthy. `repo capabilities` lists a hosted format's operations and `repo connect` prints client configuration for every format with the credential as a `${ARTIFACT_GATEWAY_TOKEN}` placeholder. `audit list` pages the audit log (or reads a `--bounded` window) with table, JSON or `--csv` output; `status` exits non-zero when the Gateway is unhealthy (`--strict`: whenever it is not healthy) and reports unreadable checks as unknown; `diagnostics`, `runtime nodes`, `overview show` and `lifecycle-job list|show` read the runtime. Server documents pass through JSON output unchanged. Pagination metadata now names bounded and offset windows, and the command-map guard checks every implemented row against the endpoints its command declares. `make cli-e2e` covers all of it against a real Gateway, including a refused credential whose request id is found in the audit log.

- `agctl group members set` rejects empty or incomplete Group read-back responses after a confirmed member replacement, retaining the applied member list and recovery hint without repeating the write.

- `agctl group list|show|create|update|delete`, `group members list|set` and `group resolution` manage Groups from the command line (#130). Members are repository IDs in resolution order; `update` and `members set` are conditional on the Group version and a 412 never overwrites; creation reuses its idempotency key across retries; `delete` needs `--yes`, keeps the member repositories and is never retried. Writes whose response is unreadable or incomplete report an unknown outcome with the read-back path instead of success. `make cli-e2e` now also runs the Group lifecycle against a real Gateway. The member replacement endpoint returns the updated Group although the contract documents a member list; the CLI accepts both.

- Repository writes through `agctl` retain recovery information when a successful HTTP response is unreadable, empty, or incomplete: creation reports its original idempotency key for safe replay, and updates report the repository to read back before retrying.

- `agctl repo list|show|create|update|delete` manage repositories from the command line (#130). Listing reads one page by default and `--all` follows cursors within a page and time budget, exiting 3 with the pages read when it stops early. `create` sends an idempotency key that retries reuse and reports it when the outcome is unknown; `update` is conditional on the repository version and a 412 never overwrites; `delete` needs `--yes`, treats 202 as accepted rather than finished, and `--wait` reads the repository back until it is `deleted` within `--timeout`, exiting 8 with the last state on timeout. `--dry-run` prints a client plan without submitting the write. `make cli-e2e` runs the lifecycle against a real isolated Gateway and runs in CI; offline golden envelopes cover the JSON output (#127).

- Query-backed editor reads preserve their opening repository catalogue, pagination cursor, selection and draft across panel refreshes, and cancelled explicit reads cannot reapply an old cached success. Access rechecks hide previous decisions while pending or failed, including a 403 that keeps the signed-in identity. Cancelling a replication plan invalidates its progress as well as the list. The read inventory follows SDK aliases and namespace calls and reports helpers shared by queries and ordinary actions; its remaining non-query scope still requires issue #284's acceptance decision.

- The last management reads outside the shared query layer move onto it: the quota rule editor's extra repository pages, reloading an email target after a version conflict, a replication plan's progress and an access evaluation. A superseded evaluation can no longer replace a newer one, a failed plan read offers Retry, and the editor reads are cancelled when the editor closes. `scripts/read-inventory.mjs` lists every Gateway read and where it runs, and a unit test fails when a read runs outside a query without a listed reason (#284).

- npm, PyPI, Go and Cargo package details read their protocol documents through the shared query layer: a superseded read is cancelled and can never replace the current package, a gateway failure is retried once while a refusal is final and never ends the session, refresh failures retain previously loaded content with Retry, Cargo download errors stay with their original package, and switching the interface language no longer re-reads the package. A unit test now fails when a component that runs effects calls `fetch` outside the documented exceptions (#284).

- Quota alerts and email notifications read through the shared query layer while keeping their stricter rules: a password-change or permission denial still blocks the workspace until an explicit refresh succeeds and never ends the session, a local write cancels the read in flight so an older answer cannot replace it, and returning to the page re-reads at once, replacing even an initial read in flight. Each refresh owns a generation, so a post-write refresh cannot make an older cancelled refresh report success. Their snapshots now also drop with the signed-in identity like every other cached read (#284).

- Service account details link to the account's repository grants and recent audit events (`/access?principal=…`, `/audits?actor=…`), so a credential rotation can be verified without copying the principal between pages; both pages open filtered to the linked principal. The visual baseline adds phone-width captures of sign-in, public browse and repository detail, and waits for the overview plot to settle so the command palette capture is stable.

- Group member editing waits for the complete member list and its write version, reports read failures with Retry, and cancels closed reads. Group dialogs finish their close animation before unmounting; cancelling deletion restores focus to the row menu. Enter opens row-menu dialogs without accidentally activating their close button.

- Console review close-out (#317, #319): group rows use one Edit members button plus a menu (resolution order, capacity, settings, delete) that returns focus to the row when a dialog closes, and the meaningless resource-version column is gone; accounts, users, API keys and credentials read "Enabled" instead of the repositories' "Active"; labels below the 12px floor are raised, with unit tests guarding the type floor and AA contrast of both Gateway themes. The design review is archived under `.impeccable/critique/`.

- Console user filters follow same-page navigation and browser history without an old search overwriting the URL, and start on the first page after a filter change. Date picker months and weekdays follow the Console language, and English scheduled task labels and row actions fit their desktop table columns.

- Console display text now lives entirely in per-feature message dictionaries: the inline `text("中文", "English")` calls are gone, and unit tests fail on a missing or empty translation, mismatched placeholders, a key defined twice or used nowhere, a glossary synonym (`src/messages/glossary.ts`), or Chinese text written outside the dictionaries. That check translated usage snippet labels, untagged OCI versions, reader errors, audit CSV headers and identity labels that were Chinese-only (#276).

- Console pages are declared once in `src/app/routes.tsx`, which drives the router, sidebar, command palette and access redirects. New shared primitives (`ListPage`, `ListStates`, `FilterCard`, `useUrlFilters`, `FormModal`, `ConfirmDanger`, `DetailDrawer`) carry the list states, URL-kept filters and in-flight locking; API keys, groups and users use them, and their filters now survive a reload (#274). Page-only styles load with their page instead of the global stylesheet (#273).

- Console visual and interaction refinements (#317): the repository list shows quota usage as a bar, an upstream column and a row menu instead of an always-visible delete icon; scheduled tasks fit at 1440px with labelled actions; audit actors and service account principals are no longer cut short; an empty dependency check list reads as neutral; overview cards share one height; table headings and Chinese labels are no longer uppercased or letter-spaced and format identifiers are lowercase; the sign-in page uses descriptive placeholders and drops internal API naming.

- CLI credentials stay bound to distinct IPv6 authorities and profile targets when a server override is used; profile/login output strips URL secrets, and concurrent profile writers serialize the full configuration transaction to avoid lost updates.

- The `agctl` client gains its first command surface: `agctl auth login|logout|whoami|change-password` and `agctl config profile add|list|use|remove`. Login exchanges a local username and password from bounded stdin for a session token and stores it only in an explicitly selected 0600 profile, refusing a profile bound to a different server; `mustChangePassword` accounts are guided to `change-password` instead of being changed automatically, and no password, token, or issued secret is ever printed. Logout asks the server to end the session, verifies whether the credential still authenticates, and forgets a matching stored token; a machine credential or static token is reported as still authenticating instead of as revoked. A write command that cannot plan without side effects refuses `--dry-run` with a usage error before any change. Changing a profile's server drops its stored token unless a new one is provided, so a credential is never sent to a different deployment. Profiles are written atomically at mode 0600 with lax files refused.

- The CLI command map (`docs/cli-command-map.md`, with a Chinese companion) classifies every management-API operation by owning issue, stage, permission, and acceptance code without claiming unimplemented coverage. `make cli-contract-check` (also part of `make ci-local`) fails when the OpenAPI contract publishes an operation the map does not classify, and its script test proves the drift detection with an injected operation.

- The `agctl` client is now distributable: the release builder emits version-stamped `artifact-gateway-agctl_<version>_<os>_<arch>` archives covered by the existing checksums, `make cli-build` builds linux/darwin amd64+arm64 binaries with the VERSION and revision injected, `Dockerfile.agctl` with `make cli-docker-build` produces a distroless client image carrying CA certificates, and the install documentation covers archives, the module install path, and the container image. The module install path is documented with its real caveat: it becomes resolvable only once the declared module path matches the source repository.

## 0.9.0 - 2026-10-13

- Upgrading from v0.8.1 adds migration `000145_artifact_contains_search_indexes.sql`: eight GIN search indexes; the previous 144 migration files are unchanged. Ordinary index creation can block writes, so plan the migration window and index disk space, retain a consistent PostgreSQL/object-store recovery point and follow the [release upgrade notes](.github/release-notes/v0.9.0.md). This code release does not declare any target's artifact migration or production rollout complete.

- CLI delivery remains limited to the already merged `agctl` skeleton and local source build (`version`, `info`, `commands`). The distribution/auth/profile batch in PR #346 is excluded; this release does not ship separate agctl archives or a published agctl image, and the remote `go install` channel remains unresolved in #347. Other CLI work and incomplete issues remain tracked independently.

- Maven V2 Groups authorize member reads with the same canonical Maven resource as single repositories, so coordinate-prefix grants cover permitted artifacts, checksum sidecars and metadata. Hosted artifact metadata matches the exact groupId/artifactId, excluding child-group and adjacent-artifact versions while retaining Group fallback when the requested metadata is absent. Authorization still precedes member payload/cache access (#348).

- Public browse reads OCI tags, manifest details and Conan revisions through the shared query layer: each version page is cached and cancellable, a failed later page keeps the earlier ones and Retry reads only that page, and leaving a repository drops its pages. Every Console source file is now at most 400 lines, including the user details drawer, the access evaluator and the theme resolver. Behaviour and layout are unchanged.

- Added the `agctl` client skeleton (`cmd/agctl`), built with `make cli-build`: command registry and global flags, configuration precedence (`--server`/`--token`/`--profile`, then `AGCTL_SERVER`/`AGCTL_TOKEN`/`AGCTL_PROFILE`, then `~/.config/agctl/config.yaml` at mode 0600), the documented exit-code contract, the versioned JSON envelope with structured errors and request ids, a retrying management HTTP client, and the `version`, `info` (health, readiness, identity, formats), and `commands` introspection commands. The client never prints credentials and never prompts.

- The Console shell (overview, sign-in, navigation and command palette) reads through the shared query layer and is split into focused files; Layout, Login, Dashboard and the preferences provider are now each at most 400 lines. Each overview source keeps its own banner and Retry, and an expired session now ends cleanly from the overview. Single sign-on discovery falls back to local and token sign-in when it is unavailable. The command palette reads every repository page once per session and follows catalog changes. Shell messages move into feature dictionaries without changing their text. A 5xx overview groups read is retried once like other pages. Behaviour and layout are otherwise unchanged.

- Console settings retain loaded forms and drafts after failed refreshes. Late OIDC saves cannot repopulate a previous session's cache, and audit retention edits reset when the saved policy version changes. Failed audit refreshes keep the displayed page; successful refreshes read and replace only the first page. Audit group filters retain the original 200-group window across cursor pages.

- Audit log, audit retention, site settings and authentication settings read through the shared query layer and are split into focused files; every settings, audit and quota Console file is now at most 400 lines. Audit pages go forward by cursor and back from cache, a page that fails to load keeps the current one on screen, and Refresh reads the newest records with one request. Audit cleanup jobs poll while one is running. The site settings shell and page share one read, so a saved brand or installed theme shows everywhere at once and the brand no longer falls back to defaults while signing in. The authentication form starts afresh on a newly saved version but keeps edits through refreshed reads of the same version. A 5xx audit refresh is retried once like other pages. Behaviour and layout are otherwise unchanged.

- Groups, global search and artifact detail read through the shared query layer and are split into focused files; the Groups, Search, artifact row detail, OCI image detail and public Conan package table files are now each at most 400 lines. Reopening a group's resolution check or retrying an identical search starts a fresh request instead of joining a stale one. Artifact scan status polls only while a scan is queued or running, quarantine and scan drafts reset when another artifact is opened, and an OCI deep link still wins over an earlier version choice. Repository capacity, download statistics and the artifact picker also use the query layer. Behaviour and layout are otherwise unchanged.

- Repository security and quarantine-read editors synchronize their drafts with refreshed policy versions before saving. Failed artifact searches and first-page refreshes retain the last successful rows in the same repository, with Retry; retained rows never supply a cursor for a new search or survive an identity cache reset.

- Repository detail tabs (artifacts, retention, APT operations, distribution, security, scanning, settings, lifecycle jobs and tombstones) and the repository list read through the shared query layer and are split into focused sections; every repository Console file is now at most 400 lines. Artifact lists keep the current search when an older answer arrives late, polled job lists stop when the tab is closed, and saved policies stay on screen with their confirmation. Behaviour and layout are otherwise unchanged.

- Main artifact search supports explicit case-insensitive literal Contains matching while preserving omitted-parameter behaviour and exact SHA-256 lookup. Console search defaults to Contains with a Prefix option; public results support cursor continuation. PostgreSQL reuses and extends trigram indexes, bounds Contains requests to five seconds, and preserves permissions and artifact identities.

- Unknown artifact formats now show the unified unavailable state in repository detail, public browse, and search detail. Format-specific task entrypoints are resolved from the registry for each repository type.

- The Console describes each artifact format once, in a format registry, instead of repeating format checks across pages. Repository detail no longer downloads every format's detail panels and the Maven publish wizard up front, which shrinks its bundle by about 71 kB; public browse and global search load only the formats they show. Behaviour and layout are unchanged.

- Operations and System status read through the shared query layer and are split into focused sections. Opening the job history tab directly (a link or a reload) now loads it instead of waiting indefinitely; job history polls only while a job is active and the tab is visible. Webhook delivery filters can no longer show a slower answer for a previous filter, and schedule and webhook editors report a failed save only inside the dialog. Schedule and webhook writes and the job history link are covered end to end against a live Gateway. Malformed job projections stay local and Retry re-reads the history; empty-body HTTP 401/403 diagnostic refusals hide cached snapshots and reach shared authorization handling.

- Console access and identity splits keep section Retry tied to its read sources, retain cached grants after refresh failures, and use typed feature message dictionaries without changing display text. Malformed credential pages stay local with Retry, refresh failures retain credential rows, and interpolated names preserve literal dollar characters and placeholder-like text.

- The public browse page is split into focused sections (catalog, repository bar, artifact results, OCI version panel, usage), with its page messages in typed feature dictionaries. Catalog and artifact searches use the shared query layer; anonymous refusals stay on the page, never end a session, and are not retried. Refresh failures keep previously loaded content with a warning and Retry. Results and usage calculations stay inside their section boundaries, and section Retry awaits the refreshed source data. OCI/Conan incremental loaders remain a separate query migration.

- Console theme changes handle rejected native capture and update promises in the application. Skipped or invalid browser captures keep the accepted palette without uncaught page errors.

- Overview Retry keeps the failed section contained until every requested data source finishes. A faster statistics response no longer renders an older damaged groups answer again; navigation still invalidates a previous section's Retry.

- Access control, API keys, service accounts, users and repository grants read through the shared query layer, so a grant saved on a repository, in Access Control or in the user drawer, and a saved role or template, shows everywhere without a manual refresh. A repository administrator's grants tab no longer requests the platform-wide user and key directory it cannot read. Access Control is split into separate grants, evaluator and policy sections, each contained by its own error boundary. Editor dialogs open from a clean draft each time. Grant, credential, API key, role, template and user-drawer writes are covered end to end against a live Gateway.

- Maven Hosted accepts unchanged ISO-8859-1 POMs and resolves an exact `${project.parent.version}` identity from the POM's literal parent version in standard PUT and strict coordinate commit. Original bytes and release checksum sidecars are preserved; malformed XML, unsupported encodings, unresolved identities and coordinate mismatches still fail validation. Real Maven cold-cache parent and dependency-tree consumption is part of the protocol gate.

- Console markup names semantic colour roles (`text-fg-tertiary`, `border-line`, `bg-surface-translucent`, ...) instead of Zinc palette steps; the `!important` compatibility layer that remapped 35 palette classes is removed and a unit test rejects new raw palette utilities. Dividers, the pending-approval card and the public-browse summary grid now follow the light theme instead of staying dark.

- Recent activity on the overview reads as events (operation, object, actor, time, outcome) through the same labels as the audit log, which now also shows outcomes as status text and localizes the actor column. Previously the overview printed raw operation and outcome codes.

- Repository detail groups its thirteen tasks into Artifacts, Governance, Distribution and Settings, with the group's tasks as a secondary bar. Existing `?tab=` deep links keep working; the Artifacts and Settings tasks are labelled Browse and General to stay distinct from their groups.

- Console shell v2: the top bar is replaced by a navigation rail that carries a ⌘K / Ctrl+K command palette (pages, repositories, artifact search, theme, language, sign-out), the connected node, theme and language controls, and an account menu that names the signed-in actor and authentication method. Token management and sign-out move into that menu.

- Console design language v2 foundation: the built-in Gateway Dark and Light themes move to neutral hairline layering with a solid foreground primary action, reserving signal cyan for links, focus and current location. Geist and Geist Mono are self-hosted with the Console bundle. Formats and states render as a swatch or dot beside readable text, and Hosted/Proxy as neutral text. Extension Theme Packages and the package schema are unchanged.

- Console changes are gated by a fully mocked visual baseline of the main surfaces in Gateway Dark and Light, rendered in the pinned Playwright container (`make console-visual`). Fixtures are type-checked against the generated client, end-to-end specs join the TypeScript build, and Console coverage floors rise to the current baseline with two stale per-file thresholds restored.

- Console reads repository data through a shared query layer: cancelled superseded requests, de-duplicated reads, precise invalidation after create/update/delete, one retry for network or server failures only, and a cache cleared whenever the signed-in principal changes. Failed detail refreshes retain previous data with a warning and Retry; 401 ends an expired session and prevents old cookie probes from restoring it while 403 stays a permission error. Overview calculations and repository tasks fail inside their own section boundaries, and Retry awaits all data sources used by the section while ignoring completions from a previous section. A successful deletion retry clears its earlier failure.

## 0.8.1 - 2026-10-10

- OCI upload and object-publication locks use the bounded artifact-lock pool instead of holding metadata-pool connections during object I/O. Upload completion carries one lock session into nested object locking, including with a one-connection lock pool. Cancelled acquisitions and failed unlocks discard their physical session; unlock cleanup has a bounded timeout. Existing advisory keys, schema and object bytes are unchanged.

## 0.8.0 - 2026-10-08

- Build Gateway and Go sidecars with Go 1.26.9 to address the standard-library vulnerabilities detected by release dependency audit, and update `golang.org/x/net` to v0.60.0 with its required module closure; retain strict audit with no new exception.

- Update `golang.org/x/text` to v0.42.0 to address GO-2026-6629 in the existing PostgreSQL connection dependency path.

- Native OCI Proxy repositories can opt into anonymous upstream Bearer exchange with the V2 `ociBearer` issuer/audience configuration. PATCH omission preserves it and explicit null clears it; Hosted repositories, other formats and legacy Group member settings reject the field. Migration 000144 persists configuration only; tokens remain ephemeral. Issuer trust does not grant egress permission.

- Maven/OCI Proxy downloads now reapply the selected egress policy at each approved HTTPS redirect, with exact origin/port checks, five-hop bounds, DNS pinning on locally resolved paths, credential isolation, one cancellation/timeout budget and safe outbound telemetry. Existing HTTP-only or unapproved redirect chains fail closed.

- Console theme choices preserve the latest accepted selection across skipped or failed visual transitions and document navigation. Browser layout checks measure dialogs after their opening motion completes, with unchanged geometry thresholds and failure traces.

- Overview storage and repository statistics render independently of Groups and recent audits, with source-specific retries, cancellation of obsolete requests and an early chart-module download.

- Repository capacity aggregation joins Maven path prefixes with visible coordinates and counts each matched asset once, preserving logical bytes and object counts across overlapping paths and SNAPSHOT builds.

- Repository usage supports server pagination, literal address search and distinct matching/whole-repository totals; Console tables share Ant Design pagination while cursor lists retain load-more semantics.

## 0.7.0 - 2026-10-06

- Added explicit, audited SNAPSHOT publication takeover after historical acceptance. Frozen target bytes/current selectors stay fixed through takeover; receipt-bound ordinary Maven deployments advance current only when complete, retain immutable history and source classifier choices, reject sealed re-import and stale retention jobs, and use a mandatory real Maven retry/resolution gate.

- Maven SNAPSHOT history import now supports official `maven-archetype` packaging as JARs while still rejecting missing main JARs and unknown packaging.

- Updated the Console's locked source-map-js dependency to 1.2.2 for GHSA-68fv-2mgg-jv7q; dependency audit remains strict with no new exception.

- Added a controlled offline Maven SNAPSHOT historical importer with strict frozen-byte/POM/metadata admission, original timestamp/build/classifier identities and source-selected current resolution, bound capacity preflight, durable per-GAV atomic checkpoints, safe replay/GC recovery and searchable audit. Imported GAVs reject ordinary publication by default; anomalies require explicit whole-GAV exclusion, and retention must be disabled. See [the operator runbook](docs/maven-snapshot-import.md).

## 0.6.1 - 2026-10-05

- Native backup export/restore can verify original trimpath binaries through an explicitly approved, full-digest-pinned release archive. Module/platform, complete binary bytes, archive-bound version/revision and the full migration ledger remain mandatory; specs without an archive retain strict injected-identity checks. Published v0.5.0/v0.6.0 regression and isolated PG/RustFS gates are wired to CI, and future Linux packages pass the actual backup identity check. Existing release assets are unchanged.

## 0.6.0 - 2026-10-05

- Console email target and quota rule editors now show field-specific validation with linked hints and keyboard focus on the first invalid field. Email read, save and preview failures describe the actual operation without exposing recipient data or suggesting a synthetic test retry. Default Gateway primary buttons retain readable text through normal, hover and active states.

- Removed the optional Ant Design CLI development helpers and their vulnerable `braces` chain. The October 17 audit exception is retired; API generation, theme/style builds, ESLint, type checks, and coverage gates continue through the existing repository tools.

- Added an opt-in Compose email overlay with a read-only operator configuration directory shared by API, scheduler and email worker. The checked-in relay example remains disabled. An isolated real Compose/TLS gate checks role wiring, local-only API readiness, startup failures, mismatched keys, actual HTML/plain MIME, permanent SMTP rejection and quota suppression without backfill.

- Added nonempty encrypted email/quota recovery acceptance to both isolated binary/OCI transfer profiles, including cancellation, concurrent leases and ambiguous outcomes. The upgrade gate now pins formal v0.5.0 and proves forward migrations plus pre-upgrade snapshot rollback/rollforward; keys remain an independent operational prerequisite. Broader deployment/recovery acceptance remains open.

- Console System now manages write-only email targets, sandboxed bilingual three-state template previews, explicitly confirmed synthetic tests, and readable delivery results. Targets default disabled; saving/previewing sends no mail. Whole-workspace permission gates, safe CAS drafts, cancellation and same-key uncertain test retries are covered. Real mailbox clients and production SMTP remain separate.

- Console System now provides administrator repository quota rule creation/editing, explicit enable/disable/delete confirmation and safe state/event/delivery evidence. New rules stay disabled; permission denials and version conflicts preserve write gates/drafts, stale data retains severity, and SMTP acceptance remains distinct from inbox delivery. Manual replay and production configuration remain separate.

- Added default-off repository logical quota rules with explicit sustained thresholds and recovery hysteresis, PostgreSQL state/events and atomic delivery through the existing TLS mail worker. Administrator CAS APIs expose safe evidence and delivery state; restart/multiple evaluators deduplicate, unknown data cannot recover alerts, and bilingual HTML/plain mail retains immutable evidence. Local mounts, 5xx, Console configuration and real-recipient deployment remain separate.

- Added default-off administrator synthetic email tests with encrypted recipient targets, PostgreSQL idempotency/fenced delivery and retry/replay status, verified TLS SMTP and bilingual responsive HTML/plain-text templates. SMTP acceptance is explicit; capacity evaluation and real-recipient deployment remain separate.

- Raw Proxy access logs now retain finite failure codes and phases through stdout, local administrator queries and Console copy/export, with final-response precedence and diagnostic keyword redaction.

- Runtime log queries, Console rows and copy/export now retain only server-registered route templates, without request paths or parameter values. Nexus-compatible repository access logs reuse the resolved Maven/Raw/npm/PyPI/Go request class already used by metrics instead of falling into `other`.

- Runtime log filters now provide bounded rolling recent ranges and validated custom snapshots at visible second precision, with aligned primary filters and recoverable localized errors. Rolling durations bind incremental cursors and are calculated from one locked snapshot, preserving the 24-hour and five-minute future limits across follow/pause/resume.

- Added current-process business HTTP 5xx observation to administrator diagnostics and Console, with request/error counts, actual five-minute sample boundaries and node/session identity. Ratios require at least 20 requests and fresh complete data; warmup, resets, low traffic and stale samples remain explicit. Health/metrics probes are excluded; no health judgment or alert is introduced.

- Added opt-in explicit local mount capacity to administrator diagnostics and Console, with process-namespace scope, actual sample times, shared alias identity, safe unknown/stale states and bounded queries. Defaults query no directories; no S3/NAS physical-capacity inference, capacity metric, quota, alert or deployment change is introduced.

- Extended the isolated offline backup transfer fixture to public export/restore for binary and a digest-pinned OCI Gateway build from the checkout, including unreferenced S3 bytes, actual image/build identity, wrong-identity rejection and Raw Group/grant/audit readback. A disposable daemon-loopback registry avoids external Gateway package credentials or permissions. This is scoped software-profile acceptance; broader #200/#201 recovery matrices remain open.

- Console runtime logs now use literal monospace rows, source-aware filters, bounded incremental follow/pause and unread/gap notices, with safe selection/loaded copy and finite NDJSON export. The view stays limited to the connected process memory and caps client rows, bytes, display fields and polling.

- Runtime log queries now project bounded safe diagnostic fields and report source/retention metadata; signed session/filter cursors support ascending incremental reads without skipping unread pages, while preserving descending history and administrator gates.

- Added explicit private-spec offline S3 byte export and restore to fresh local Docker targets, with binary/OCI software identity, exact migration and metadata checks, full-byte readback, optional Raw/OCI grant probes and owned failure cleanup. Real synthetic PG/S3 recovery is separate from preflight and the pinned RustFS physical drill; consistency still depends on the operator's complete stopped-writer scope.
- Update OpenTelemetry to the minimum patched trace/log versions required by dependency audit; preserve structured log and explicit exporter endpoint behavior.

- Added read-only `gateway preflight backup --input manifest.json` for versioned private local bundles, full-byte SHA-256 verification and declared writer intervals. Integrity and writer declarations remain separate from snapshot consistency, which always stays unknown; no export, stop, restore or overall backup success is inferred.

- Added offline `gateway preflight capacity --input plan.json` for fixed migration evidence. It separates repository logical quota growth from deduplicated physical object growth and temporary/backup/restore/headroom peaks, preserves missing quantities as unknown, and emits versioned JSON and distinct exit codes without contacting or modifying the target.

## 0.5.0 - 2026-10-01

- Access control keeps the public-access count and confirmed switch with one concise read-only boundary notice; the repeated three-layer explanation and decorative styling are removed. Permission evaluation retains one collapsed explanation; repository grants keep their effective decision details without repeating the decision-order footnote.

- System diagnostics shows the Console bundle's own build version beside the connected Gateway version and revision. Known differing versions warn about image tags or a cached bundle; development without injected Console metadata stays `dev` and does not infer a mismatch.

- Runtime logging can optionally export the same redacted structured events through OTLP/HTTP protobuf Logs alongside stdout and optional files. Logs configuration is separate from traces; bounded admission, finite retries, safe failure counters and bounded protocol cleanup make incomplete delivery visible. This does not add a durable retry journal, receiver storage or historical/cluster log queries.

- Runtime logging can optionally mirror its redacted NDJSON to private session files with configurable complete-event size rotation, backup count and age retention. Default stdout and local query behavior remain unchanged; retention touches only the current output's sealed files, leaving previous sessions and user files for separate retention/history work.

- Runtime log queries discard an oversized line through its newline even when writes split the line, preventing a valid-looking tail from becoming a separate event. Structured loggers preserve `component`, `operation`, `requestId`, and `traceId` bound with `With`; explicit record fields keep priority and group scopes remain independent.

- Cargo Hosted, Proxy, and Group now appear in the public format catalog, repository creation and capabilities APIs, OpenAPI contract, and Console. The Console provides sparse registry configuration and Hosted publish/yank guidance; public browsing and global search link to crate versions. Proxy creation requires an HTTPS upstream and allowed download hosts. Official Cargo 1.96.0, PostgreSQL/RustFS, backup/restore, and upgrade gates define the admission baseline; existing deployments require a separate release and rollout.

- Runtime log output now has explicit, serialized flush/close callbacks and idempotent cleanup. Gateway runtime errors return through resource cleanup before process exit, keeping logging available until cleanup finishes. The default stdout and local buffer remain borrowed destinations; this does not add file persistence or a protocol exporter.

- Runtime log redaction now applies the existing sensitive-name policy to ancestor groups as well as leaf attributes. Values nested under groups such as `credentials` or `Authorization` are masked before output and process-local keyword indexing; safe sibling groups retain their diagnostic fields.

## 0.4.2 - 2026-09-28

- Every dialog that starts a write now stays put until that write settles. The create forms for repositories, API keys, service accounts, credentials, groups, scheduled tasks, and webhook subscriptions, plus the Raw upload dialog, no longer dismiss through Escape, the mask, or the close button while their request is in flight, so a half-applied change cannot be abandoned under a pending response and the same record cannot be submitted twice by reopening the form. Each of those save paths also releases its busy state in a `finally`, so a request that fails at the network level leaves the dialog dismissible again instead of locking the form open.

- Creating a service account, issuing a credential, and linking an OIDC identity now report a failed save inside the dialog that started it. Those three were the last write dialogs that printed their failure to a banner behind the mask, which read as "the button did nothing"; the page banners stay reserved for loading the account, credential, and identity lists.

- Every console write path now releases its busy state in a `finally`. A request that throws instead of answering — rather than returning the client's `{error}` result — can no longer leave a confirmation dialog locked open or a row button spinning.

## 0.4.1 - 2026-09-27

- Direct Maven Hosted uploads now accept assets up to 128 MiB, allowing larger JARs to be imported while retaining a bounded temporary spool.

- Repository grants can now be written one row at a time. `POST /repositories/{repositoryId}/grants` creates or replaces the single grant identified by its principal and resource prefix, and `DELETE` on the same path removes exactly that row, answering `404` when it is already gone. Neither operation takes an `If-Match` precondition, because the write is confined to one grant row and cannot clobber a concurrent change to another principal — the failure mode that made the whole-list `PUT` risky as grant lists grow. Both operations bump the grant-set version and are audited as `repository.grants.upsert` and `repository.grants.delete`; the whole-list replace stays for authorization templates and bulk edits.

- The console's grant editors now work one row at a time instead of replacing the whole grant set behind the scenes. The repository's access tab lists its grants as a table with per-row edit and remove actions, and both it and the per-user repository access panel write through the single-grant endpoints, so a concurrent change to another principal can no longer be silently clobbered and the `412` version-conflict retry loop is gone from both surfaces. An edit that moves an entry to a different resource prefix is an upsert of the new row followed by a delete of the old one, so the edit never forks one grant into two.

- The repository detail tabs no longer double their spacing: the gap between two tab labels was the 12px gutter plus 10px of padding on each side, and the first tab sat 10px inset from the content edge. Spacing now comes from a single 24px gutter, so labels sit exactly 24px apart and the first tab is flush with the summary above and the surface below.

- The audit log's operation column now reads as a localized label instead of the raw machine code: `repository.grants.upsert` shows as "Upsert repository grant" (写入仓库授权), plain traffic codes as "Read (GET)" (读取（GET）), and so on for the full set of codes the gateway writes. The raw code stays on the cell's tooltip and is still what the operation filter and the CSV export carry, and a code the console does not know — one written by a newer backend — degrades to showing the code itself rather than a blank.

- Grant rows now carry one shared identity helper instead of three hand-rolled keys. The repository table joined the principal and resource prefix with a space in one place and a dash in another, and a literal NUL byte sat in the file — git and grep treated it as binary — so two different grants could collapse into the same React key and removing one row could take another with it. Changing a row's principal or resource prefix now removes the old row before writing the new one, so a failed removal leaves the grant narrower, or unchanged, rather than wider than the administrator asked for.

- The rules for writing a grant are now one set of functions shared by the single-row endpoints, the whole-list replace, and the authorization template editor. The single-row endpoints had drifted apart: an upsert accepted a principal of any length and a prefix up to the repository format's path limit, while the delete refused a principal over 512 bytes or a prefix over 255 and rejected control characters, so a grant could be created and then be impossible to remove from either console surface. Writes now enforce exactly the shape the contract declares, and delete only requires enough to name a stored row, so a row written by an earlier revision stays removable. The console's principal and prefix fields carry the same limits.

- The audit page reads its outcomes as words too. The outcome column printed the backend's code for every value except two, while the filter showed a third rendering that mixed both languages; outcomes now share one bilingual table, the filter searches the stored code as well as the readable label, and the "failed" and "denied" counters no longer count the same refusal twice. The operation map also gained the codes that denial audits and APT package operations write (admin, intelligence, apt.package.*) and marks the labels kept only for rows written by earlier revisions.

- The console stops throwing away what it already loaded when a later request fails, and stops showing a spinner under a first-load error. The task centre and webhook panels now render exactly one of loading, error, empty, or content, and the overview, groups, and API key pages keep their data on screen with the error above it instead of replacing the page with a banner. The three remaining task tab bars take the same 24px single-gutter spacing the repository tabs got, two tables that had missed the shared baseline class match the rest, the empty states that were a line of grey text read like every other empty state, and the grant row action says "remove" in both the button and its confirmation.

- The console's vertical rhythm has one source again. The users, API keys, search and repositories pages now root in the page stack, the shared header no longer carries a margin the stack cancels anyway, three pairwise CSS rules and a diagnostics margin chain are gone, and the dead margin classes that the stack's !important used to swallow were deleted where they lied about the rendered spacing. Error banners inside gap containers lost their wrapper divs; the ones inside form interiors that own an mt-chain are left for a dedicated pass. A new layout spec polls the settled page and fails on any direct child that still authors a vertical margin, or on a gap outside the 16/24px rhythm.

- Feedback now has two exits instead of twenty-nine hand-rolled ones. ErrorBanner takes a tone, so a failure the page can live with reads as a warning with a retry instead of a bespoke alert, and the new Notice covers what already happened: the fourteen copy-pasted success banners are closable now and clear their state on close, and the standing explanations that used to borrow a warning or info alert render without an icon and stay open. The artifact detail pages' bare error alerts and the scan panel's duplicated retry button are gone.

- Tables have one baseline again. A thin ConsoleTable wrapper now supplies the themed class, the page-level row density, and (for nested tables) the compact variant, so the three tables that had missed the class can no longer render with antd's light chrome in the dark console, and the task centre's four tabs stop switching row density mid-page. Forty-three call sites moved to the wrapper; page-level tables that had chosen the compact density on their own now follow the page rule.

- The console's dialogs come from one component again. Eight surfaces had bypassed the shared `Modal` — five of them copying its footer by hand and three of them re-implementing the "a save is in flight, do not close me" protection with `closable`, `mask` and `keyboard` flags — because the shared dialog offered only two widths and no busy state. The shared `Modal` now takes a `width` for the editors that sit between the two tiers and a `busy` that removes the close button and refuses escape and mask clicks while a mutation is in flight, so the role editor, the template editor and applier, the OIDC identity link, the password reset, the quarantine transition, the APT snapshot review and the theme package install all return to it; the theme installer's one-off 620px width joins the 680px editor tier, leaving three tiers (520 / 680 / 1152) instead of five.

- Dangerous confirmations now have two shapes and no third. The user drawer confirmed session revocation and account deletion through `modal.confirm`, a mechanism that renders outside the shared chrome and re-implements the danger and loading affordances on its own; both now use the shared `ConfirmDialog`, which keeps a failure visible inside the dialog instead of behind its mask, and whose own cancel and confirm labels follow the active locale instead of always printing Chinese. The rule is written on the component: irreversible, or blast-radius-needs-explaining, goes through `ConfirmDialog`; inline and reversible goes through `Popconfirm`.

- A dialog that embeds a table now scrolls once. The template editor's grant table capped its own body at 300px inside a dialog whose body is already height-capped, so two scrollbars fought over the wheel and the table's own hid rows; the table gives up its vertical cap and the dialog takes the wide tier, as the group capacity dialog now does too. A Playwright check opens the editor with enough rules to overflow and asserts the dialog body is the only scrolling layer, at desktop and at narrow mobile width.

- Feedback tones now follow one rule across the console: a first load that fails reads as an error, a refresh that fails while data stays on screen reads as a warning, a mutation that fails reads as an error, and a standing explanation reads as information. The scan panel's first-load and rescan failures move from warning to error, the audit retention page's standing cleanup note is information until the policy has unsaved changes and a warning from then on, and two banners that described ongoing business states stop borrowing the error tone: a quarantined artifact and a security policy that blocks promotion both read as warnings, because an operation or a policy change can lift them and nothing failed.

- An empty list no longer puts two identical primary buttons on one screen. The service accounts page and the webhook panel repeated the header's primary action inside their empty state; the empty state now explains and points at the single header action, matching the repository and user pages.

- Two strings that bypassed localization now travel through it: the scanning workspace's coordinate placeholder for formats without a canonical example showed Chinese in an English console, and the repository settings' egress protocol option printed full-width parentheses in both locales. The usage guides' `localize` alias for `text` is gone, so every component asks for strings the same way.

- The user drawer's session and identity panels use the shared loading and empty states instead of a bare spinner and antd's plain empty image, so they announce their state to assistive technology and match the console's empty-state icon and entrance.

- The runtime node card's health banner returns to the shared in-card alert shape. It was the only alert in the console flattened against the card header with squared corners and missing borders, a shape that only held together because the card clips its overflow; filter bars remain the single flush element, consistent across the four pages that use them.

- The "add grant" and "edit grant" dialogs are forms now, not one-row tables. The dialog carried a batch editor's column header row (principal / permission / resource scope / granted by this rule) over a five-column grid whose minimum width is 1148px: on a phone the dialog body could only show the principal field, and the permission and scope controls sat past its right edge with no horizontal scrollbar to reach them, so adding a grant was impossible below the desktop tier. Each field now carries its own visible label, the granted-capabilities badge reads as the result of the permission choice rather than a fourth column, the dialog takes the 680px editor tier instead of the wide one, and the surface that only ever manages one fixed principal shows that principal as text instead of a select whose value cannot change. A Playwright test opens the dialog at 1440 and 390 in both themes and fails if any content sits outside the dialog body's own width.

- The grant dialogs cannot be dismissed mid-save, and they warn before replacing a row. Both "add grant" and "edit grant" now pass `busy` to the shared dialog and disable their cancel button while a write is in flight, so escape, the mask and the close button all wait for the result instead of leaving the administrator looking at a closed dialog over an unfinished request. An edit that moves a grant onto a (principal, resource scope) pair another row already holds now says so before the save — the warning used to cover only the add path, so an edit could silently replace a different row while deleting its own. The save also takes the grant list from the upsert response instead of reading the repository a second time, and the save button waits for a principal to be chosen.

## 0.4.0 - 2026-09-26

- The level migration ships with the report it owed the instances it cannot decide on its own. `make member-role-migration-report` reads a deployment's database and writes nothing, so it runs before the upgrade as an inventory and after it as a confirmation. It prints four sections, each row naming the identifier to act on: principals that still reach repositories only through `GATEWAY_REPOSITORY_READERS` or `GATEWAY_REPOSITORY_WRITERS` and hold no grant on the repository they read, with the number of decisions and the last one seen; grants held by API keys and service accounts, which have no user row to show them, with the credential's name and state; values the model cannot express, namely accounts whose level is not `none`, `member`, or `admin` and keys carrying more than one level or an unrecognized one; and grants that can no longer take effect, because their repository is missing or inactive or because they name a `user:`, `api-key:`, or `service-account:` principal that does not exist. A token actor without one of those prefixes is deliberately not listed. The upgrade check seeds one instance per class and asserts the report lists it and does not claim an actor that already holds a grant. It also keeps a pre-upgrade dump of its probe database, restores it, and asserts the restored database still carries the removed levels and not the converged constraints, so the documented rollback for this migration is verified rather than assumed.

- A member with no repository grants is told where repository access comes from. The repository catalog's empty state described how to create a repository, which a member cannot do and which is not the reason the list is empty; it now names the actual reason — access is granted per repository by a platform administrator — and a platform administrator still reads the creation copy.

- A repository's Console page follows the repository's own authority instead of the platform flag. The management tabs — access grants, retention, security admission, capacity, tombstones, promotion and replication, lifecycle jobs, signed snapshots, and settings — appear for that repository's administrators, publishing for a write grant, scanning for the independent intelligence scope, and the artifact and usage views for any read authority. The platform administrator keeps every tab, and a tab whose authority is missing is not offered even when the URL names it.

- The V1 compatibility surface stays a platform-administrator operation. The `/api/v1/**` handlers do not take part in the platform/repository split: they predate per-repository authority, and a V1 Group member may carry no repository binding, so nothing attributes it to a repository an administrator could hold. The two tiers live on the v2 surface, and a V1 client that needs repository-scoped administration has to move to `/api/v2`.

- Scheduled tasks now follow the repository they target. A repository retention task is created, read, updated, run, and deleted by the administrators of that repository or by a platform administrator, the catalogue lists only the tasks a caller administers, and moving a task from one repository to another needs authority over both the repository it leaves and the one it reaches. A task with no repository, the audit retention job, remains a platform operation.

- Two repository management operations now belong to the repository's administrators instead of the platform administrator alone. Testing a repository's egress proxy and applying an authorization template to a repository's grants both act on exactly one repository, so a `member` holding `repositories:admin` on that repository may perform them, while the same caller is refused on any other repository with an audited denial. The authorization template catalogue, repository creation, and every other platform surface still require the `admin` level. In the same pass `GET /formats` dropped its administrator requirement for the authenticated caller its contract already documented: the format table carries no repository state. The remaining platform and repository tiers are follow-up work.

- Hosted groups follow the repositories they reference. Creating a group, replacing one, changing its members, and deleting it are now allowed for the administrators of every repository the change touches — the members it keeps or drops as well as the ones it adds — and for a platform administrator. A refusal names the member that is out of bounds and records a management audit entry. A member that is not bound to a repository belongs to the platform tier, because nothing attributes it to a repository. The answer is never cached, so removing a member, deleting a repository, or withdrawing a grant takes the group away from a repository administrator on the next request. Group browsing is unaffected.

- The cross-repository management views follow the same tier as the operations they describe. A repository administrator now sees the repository grants, capacity snapshots, and lifecycle jobs of the repositories it administers and nothing else, so a repository it cannot administer contributes no rows and is indistinguishable from one that does not exist; a platform administrator still sees all of them. The audit log is deliberately unchanged: it spans every repository and credential, and an audit view scoped to one administrator's repositories is a separate decision.

- Every `401` now carries a `WWW-Authenticate: Bearer` challenge, so a caller refused for missing credentials learns which scheme to use instead of guessing. Protocol handlers keep their own challenge and are never overwritten: OCI and Raw publish a protocol realm and Conan publishes `Basic`.

- The effective-access endpoint no longer reveals whether a repository exists to a caller that has no authority over it. It answered with a full permission matrix for any known identifier, which let any authenticated caller confirm a repository's existence and read its permission shape. It now answers only for a repository the caller holds read or intelligence authority over at the requested resource, and answers anything else exactly like a repository that does not exist. Intelligence authority counts because a scanning credential manages intelligence without read access; administrators are unaffected and can still evaluate any repository. The endpoint's OpenAPI description and the anonymous-access and Nexus gap documents now state this, replacing text that claimed the endpoint permits no impersonation and is administrator-only, neither of which was true.

- A deployment that configures no reader patterns now refuses an unmatched authenticated caller instead of admitting it. On 0.3.1 and earlier a legacy repository was readable by any authenticated caller whenever `GATEWAY_REPOSITORY_READERS` was empty, silently and under no configured policy; that fallback is gone and the refusal is an ordinary `403` on the legacy Maven, OCI, Raw, and Conan paths. `GATEWAY_LEGACY_READ_DEFAULT=allow` restores the earlier posture and logs a startup warning while it is set, so an upgrade can be staged, but it is a migration aid rather than a posture to keep: inventory the actors that read without a grant, give each an exact repository name or `prefix/*` pattern in `GATEWAY_REPOSITORY_READERS` or a per-repository grant, then unset the variable. There is no catch-all pattern, so every repository an actor needs has to be listed, and an unrecognized value fails closed. Administrators, configured reader patterns, a repository's own grants, anonymous access, and the pending and password-change blocks are unaffected. See the legacy group migration guide for the upgrade and rollback procedure.

- Existing `reader` and `writer` accounts are converted to the `member` level with equivalent per-repository grants on every repository that exists at upgrade time, so nobody loses or gains access on the repositories they already had. Repositories created after the upgrade are deliberately not covered: access to a new repository is an explicit grant rather than an inherited side effect. The migration does not mark any grant set as managed, so repositories keep serving their legacy static readers and writers exactly as before, and `member` is now accepted as an OIDC JIT default role at the database level.

- A grant set that names a principal now decides for that principal even when the set is not marked managed. Previously an explicit per-principal grant was ignored until an administrator replaced the repository's entire grant set, which made a targeted grant silently ineffective; principals the set does not name keep the legacy static policy, so nothing widens.

- Added a `member` account role that carries no repository capability of its own. Approving an SSO account or creating a local account with `member` grants no repository access at all; its reach comes only from per-repository grants, so one role assignment can no longer hand a user every repository. The role is available for users, API keys, OIDC role mappings and the OIDC JIT default, is offered first in the Console role pickers with an explicit label, and the effective-access explainer names it. The `reader` and `writer` roles it replaces are removed in the same release, so no deployment keeps a global role that covers every repository.

- The `reader` and `writer` account levels are removed. Assigning either value is now a `400` on user creation and update and on API key creation, rather than a silent downgrade; the Console pickers, the user-list filter, and the effective-access simulation offer only `none`, `member`, and `admin`; the OIDC role-mapping settings collapse from a reader list and a writer list into one member list, because a mapped external role approves an account instead of granting repository capability; and `users.role` and `api_keys.roles` are now constrained, so the database refuses the removed values too. An external realm role listed in either legacy mapping list stays mapped, now to `member`. Accounts and API keys that still named a removed level are narrowed to `member` during the upgrade — the level they already resolved to, since an unrecognized level granted nothing — while a key that also named `admin` keeps it, and a key with no level stays level-less. The OIDC JIT default role defaults to `member` instead of `reader`, and a stored default of `reader` or `writer` is migrated the same way. Because the migration replaces the reader and writer settings columns rather than leaving them in place, a rollback after this release restores the pre-upgrade database and the matching binary instead of only switching the image.

- A repository configuration change or deletion now requires repository `admin` scope instead of `write`. Updating a repository's endpoint, allowlist, egress, upstream credential, anonymous-read, or strict-publication settings, and disabling a repository entirely, change how that repository behaves for every client, so they now sit in the same tier as its grants, retention, security, and lifecycle operations rather than sharing the publish/delete-artifact tier. A principal holding only `write` scope is refused with `403`. The Console screens for these actions were already administrator-only.

- An account whose password must be changed can no longer reach repository surfaces. The block is enforced inside the authorizer, so management routes, artifact browse, global search, effective-access, publish sessions, protocol reads and writes, and Group member resolution all agree instead of only the administrator-gated routes. Affected requests answer `403 password_change_required` rather than a generic denial, and changing the password stays reachable because it is authenticated separately. The legacy static read and write policy paths apply the same block, so a permissive legacy configuration cannot admit such an account.

- Administrator-only management endpoints now answer an authenticated non-administrator with `403 access_denied` instead of `401`. A missing or invalid credential still returns `401`, and a session that must change its password returns `403 password_change_required`. Clients can now distinguish "sign in again" from "you are signed in but not allowed", which previously could not be told apart.

- Authorization changes are now audited. Replacing a repository's grants, creating, updating, or deleting an authorization role or template, and applying a template to a repository each record a management audit entry naming the acting principal and the changed resource. Previously these mutations left no trace, so `SECURITY.md` asked operators to review permission changes that were never recorded; rejected or conflicting requests are still not recorded as mutations.

## 0.3.1 - 2026-09-24

- Authorized `reader` and `writer` users can now browse repositories and artifacts in the Console. A `writer` can use Raw upload, the Maven publish wizard, and OCI/npm/PyPI publish guides where effective repository write access applies. OCI Docker/Podman publishing uses an administrator-issued service-account credential with a repository write grant. A `reader` has no upload or publish controls. The server filters repository discovery by read permission. Pending users remain blocked, while repository configuration, access grants, and user management remain administrator-only.

## 0.3.0 - 2026-09-24

- OIDC JIT provisioning can now register a user with the explicit `none` role. A first sign-in creates a passwordless local account and stable issuer/subject binding that appears as Pending in the Console Users page; it grants no repository access, including through legacy read defaults or managed grants. Administrators can assign or revoke global reader, writer, and admin roles there. Pending users see an authorization waiting page with a check-again action. Later sign-ins update safe SSO profile fields without overwriting the Gateway role; disabling an account or revoking its linked session still takes effect on the next request. The default JIT role remains reader for compatibility, so operators must choose `none` when enabling this onboarding policy.

## 0.2.0 - 2026-09-24

- Updated the indirect gRPC dependency to v1.83.1 to address GO-2026-6348.

- Added lifecycle artifact download usage. Every successful content download resolved through the Gateway — Hosted, Proxy, or Group, across Maven, OCI, Raw, Conan, npm, PyPI, Go, and APT — is folded at the single audit write point into a durable per-artifact aggregate (`artifact_usage_stats`), so counts accumulate for the artifact's whole lifecycle and survive audit log retention: download count, total bytes, first and last download time, and last actor, keyed by the artifact address clients resolved (a Group download counts for the Group). The management API serves `GET /api/v2/repositories/{repositoryId}/artifact-usage`, and each repository Console gains a Usage (使用统计) tab with lifetime totals and a per-artifact table. HEAD probes, 304 revalidations, publishes, and management operations never count. Repository retention now consumes the same aggregates as cleanup evidence: the dry-run JSON and CSV export attach each candidate's download count and last download time, and a new `keepDownloadedDays` policy field (default 0, preserving current behavior) exempts cleanup units downloaded within the window from the current round regardless of age or version-count reasons, with per-format matching for npm, PyPI, Go, Maven, Raw, OCI manifest pulls, and Conan v2 revisions.

- Added Go Proxy checksum database mirroring, closing the last known Go compatibility gap. A Go Proxy repository that lists a checksum database host in its egress allowlist now serves the go command's `/go/<repository>/sumdb/<name>/supported` probe and forwards `/latest`, `/lookup/`, and `/tile/` requests over allowlisted HTTPS egress. Signed tree bytes, status codes, and content types pass through verbatim and are never cached; the upstream credential is never attached; and mirrored requests follow the repository read policy, so the go command can verify module downloads against a checksum database it can only reach through the Gateway. The native Go E2E gate now proves a real `go` client verifies module downloads through the mirror.

- Added the npm registry identity endpoint. `GET /npm/<repository>/-/whoami` (Hosted, Proxy, and Group) answers an authenticated caller with `{"username": <Gateway principal subject>}` and challenges anonymous requests with `401`; it never consults repository authorization, so `npm whoami` and CI credential preflights work against any npm repository the client can reach. The native npm E2E gate now drives the real npm CLI `whoami` plus the anonymous rejection through the Nexus-compatible roots.

- Added upstream authentication credentials for Go Proxy Repositories. A Go Proxy Repository can store a `none`, `basic`, or `bearer` credential; the secret is sealed with `GATEWAY_SETTINGS_ENCRYPTION_KEY` under the `repository-upstream-auth` purpose, is never returned by the management API, and is applied only to the Go Proxy upstream fetch. `basic` and `bearer` require a secret, switching to `none` removes a stored credential, credentials are stripped on cross-host redirects, and every other format or Repository type rejects `upstreamAuth` with an explicit `400` instead of ignoring it.

- Added a Console disaster-recovery surface for APT signed snapshots. A repository administrator can export any visible or retired snapshot as a portable archive, receive its `sha256:` receipt in the same step for independent storage, and restore an archive against that stored receipt. Restore never derives the receipt from the uploaded bytes. APT Hosted remains an operator preview.

- The Console now exposes every Go lifecycle surface the Gateway already implements: Retention, Security admission, Promote/replicate, Lifecycle jobs, and Tombstones appear for Go Hosted Repositories, scheduled retention tasks accept Go targets, retention and promotion forms use Go module terminology, and the distributed-deployment and authorization-metrics documents list Go where it is accepted.

- Go Hosted Repositories now enforce the default-disabled Quarantine Read Policy. When an administrator enables the policy, a quarantined module version disappears from `@v/list`, cannot be selected by `/@latest`, and returns `403` for every `info`, `mod`, and `zip` representation; ordered Go Groups refuse to fall through past a quarantined higher-priority coordinate. Hosted reads stay compatible by default, administrators can now manage the Go policy through the same versioned management API and Console surface as other formats, and each denial records the standard `quarantine_read_policy` audit evidence.

- Added administrator-only exact APT archive recovery with independently saved backup digests and public-key trust, both signature and canonical package-index checks, atomic metadata visibility, quota reservations, concurrent replay and durable failed-attempt cleanup. Recovery needs no signer service; APT Hosted remains an operator preview.

- Added administrator-only deterministic APT signed-snapshot export and an offline archive integrity command, with published/retired snapshot coverage and Debian installation from exported bytes. APT Hosted remains an operator preview; trusted archive recovery uses independent backup receipts and public-key policy.

- Pin upgrade/restore rehearsals to verified image identities, use v0.1.0 as the upgrade baseline, and verify Group provenance and reader grants after restoration.

- Added Maven/Raw Group directories with per-member provenance, conflict evidence, exact SNAPSHOT builds, and scope-validated Group-only Maven cache entries. Signed navigation expires after configuration, authorization, or ordering changes. Refined the shared directory browser with clearer hierarchy, source links, compact evidence, collapse controls, and responsive file names.

- Maven Groups now try every eligible Proxy after Hosted misses and bind positive/negative cache results to the ordered authorized candidates and upstream settings. Member, permission, allowlist, or egress changes revalidate the result; legacy multi-member entries are refreshed, and a fallback after an upstream failure is not cached as a complete Group resolution.
- Isolated integration test, migration-check, and cleanup commands from the development Compose project, preventing a checkout `.env` or exported project name from redirecting test cleanup to running local services.
- Raw Groups now continue to later Proxy members after a 404, 410, or member-scoped negative-cache hit, while preserving terminal authorization, upstream, and checksum errors. Administrators can inspect the server's Hosted-first candidate order in the Groups Console, separately from configured positions and per-artifact resolution results.
- Added PostgreSQL-backed lazy directory browsing for Maven and Raw Proxy caches, with repository/upstream isolation, live cache evidence, exact timestamped SNAPSHOT nodes, and deep links that preserve asset paths and build numbers. Directory failures can be retried in place; Raw Proxy details no longer show Hosted deletion actions.
- Added a semantic Console theme system with four built-in packages, neutral
  text selection, restrained accent usage, and a reduced-motion-safe radial
  reveal anchored to the theme selector. Administrators can strictly validate,
  preview, install, replace, enable, and delete PostgreSQL-backed theme packages
  without rebuilding the Gateway; version checks, deletion protection, and
  audit records guard the managed lifecycle.
- Replaced decorative empty-state artwork with compact semantic icons,
  actionable explanations, and responsive layouts.
- Added a lazy, server-owned directory tree for Maven and Raw Hosted
  repositories. Signed node IDs and cursors bind navigation to the repository
  and principal, Maven SNAPSHOT nodes retain the exact selected build, and Raw
  paths stay readable while copy and actions preserve their canonical value.
- Refined the sign-in, public catalog, empty-state, and theme surfaces with
  responsive geometry checks and reduced-motion-safe visual behavior.
- Licensed Artifact Gateway under the MIT License and documented the project
  and contribution licensing terms in both README languages.
- Improved Raw usability across the Console: Unicode and space-containing paths
  are displayed as readable names in repository browse, public browse, global
  search, retention previews, and distribution selectors while canonical
  coordinates remain unchanged for protocol actions. Search now preserves
  literal percent signs, download snippets choose a readable shell-quoted file
  name, and file/checksum responses advertise it with `Content-Disposition`.
- Hardened Raw path validation with a 4096-byte encoded limit and rejection of
  control and bidirectional-formatting characters. Upload validation no longer
  silently trims spaces or removes a leading slash, and reports actionable
  errors before sending invalid paths.
- Reused an existing authenticated browser session when moving from public
  browse to the management Console. The public entry now opens the Console
  directly, and `/login` redirects an already authenticated user to the
  requested management location.
- Clarified that a Raw path is a Nexus-compatible mutable reference while
  `path + digest` is the immutable governance and distribution snapshot.

## 0.1.0 - 2026-08-24

- Published the first reproducible, versioned Gateway/healthcheck binary
  archives with migrations and an environment template, plus the Console static
  bundle, resolved OpenAPI contracts, checksums, GHCR images, and CI-qualified
  `main` snapshots. Release binaries and images report the same version and Git
  revision.

- Added a Nexus-style `/repository/<name>/...` migration root for Maven, npm,
  PyPI, Raw, and Go Hosted/Proxy/Group traffic. Real Maven/Gradle, npm,
  twine/pip, Raw HTTP, and Go clients now retain their Nexus base paths;
  generated npm tarball, Raw pagination/upload, PyPI publication, and Go
  publication URLs remain on that root. PyPI accepts Twine uploads at the
  repository root, and Go Hosted accepts Nexus 3.93+'s version-only ZIP upload
  after deriving and authorizing the module identity. The legacy canonical
  Maven prefix reserves the exact target name `maven` to prevent ambiguous
  cross-repository routing.

- Added Go Hosted repositories with authenticated single-ZIP publication,
  canonical module and `go.mod` validation, atomically derived `.info`/`.mod`
  representations, content-addressed PostgreSQL/RustFS persistence, idempotent
  replay, immutable-coordinate conflict rejection, publication scanning,
  management tombstone/restore with protocol, Group, search, and scan-identity
  visibility enforcement, repository retention planning, a 24-hour recovery
  window followed by reference-safe object reclamation and capacity release,
  immutable promotion of the complete three-representation version snapshot,
  checkpointed replication to target-specific verified objects with final
  snapshot and quarantine revalidation,
  Hosted-first mixed Groups, and a real
  `go mod download` acceptance gate.

- Added stable Service Accounts for Jenkins, CI robots, scanners, and
  third-party applications, with one-time expiring credentials, overlapping
  zero-downtime rotation, immediate account disable, Bearer and native-client
  Basic authentication, Repository Grant integration, generated management
  APIs, audit evidence, a bilingual Console workflow, and an isolated release
  gate. Redesigned the public artifact catalog with a clear read-only boundary,
  source and format summaries, repository search, format filters, and
  Hosted/Proxy/Group guidance. The administrator surface now also explains its
  global, Repository, and Group/member gates and blast radius without changing
  the default-deny read-only policy.
- Added each OCI manifest's immutable creation timestamp to repository browse
  responses so consumers can select the newest publication without inferring
  order from tags or digest text.
- Switched the runtime, local Compose, Kubernetes, integration tests, and
  configuration contract fully to RustFS using the official AWS SDK for Go v2;
  removed MinIO services, SDK dependencies, migration tooling, and cutover
  bypasses while retaining fail-closed detection of legacy resources.
- Added the first APT H3 signing hardening slice: remote signers require a
  public-only OpenPGP keyring matching one or two pinned fingerprints, and
  Gateway cryptographically verifies both signatures before visibility. This
  allows a controlled rotation overlap, derives signer identity and algorithm
  from verified keys, validates the complete keyring before preflight can pass,
  and records structured immutable signing evidence without changing
  authorization-reason semantics; the
  OpenPGP dependency chain now includes the CIRCL secp384r1 fix from v1.6.3.
  Repository administrators can now compare the configured old/next trust
  window with the latest visible snapshot in a generated API and bilingual
  Console view, while bounded outcome and latency metrics support operational
  alerting without high-cardinality signer or repository labels. During a
  rolling upgrade, a Console connected to an older Gateway now identifies the
  missing signing-state endpoint as an unavailable feature instead of
  incorrectly reporting that the repository does not exist.
  A dedicated external-signer gate now provisions signer-owned keys, mounts
  them read-only for serving, uses a signer-specific TLS CA, and proves old,
  overlap, new, rejection, and retirement behavior with clean Debian clients.
- Prioritized Cargo sparse-registry planning after APT H3 and deferred NuGet
  repository implementation while retaining its tested parser foundation.
- Kept access evaluation and repository grant editing focused on usable
  authorization principals by hiding disabled users and revoked or expired API
  keys from both principal pickers.

### Added

- Added a preparation-stage bilingual project and documentation entry point,
  an architecture-faithful README hero, a tested local-link documentation gate,
  and `make dev-bootstrap` for idempotent generation of the six credentials
  required by the local PostgreSQL/RustFS development stack.
- Added bilingual, reviewable Mermaid diagrams for the system boundary,
  standalone and split-role deployment, publication visibility, and durable
  background work, generated icon-rich system and deployment architecture
  overviews, plus an evidence-backed guide to the PostgreSQL-native locking,
  queueing, notification, JSONB, search-index, and observability features that
  keep the control plane lightweight.
- Added a reproducible isolated-Docker performance baseline covering stripped
  Go binaries, the distroless runtime image, quiet Gateway/PostgreSQL/RustFS
  memory, authenticated PostgreSQL metadata reads, and 64 KiB Raw reads through
  RustFS, with bilingual evidence, explicit limitations, and a one-command
  runner that removes its containers, volumes, and ephemeral credentials.
- Began the maintainability plan by extracting recursive public OCI metadata
  reads, tag pagination, and protocol-specific repository setup snippets from
  the large Console browse page behind tested pure module seams. Added Chinese
  architecture, contributing, protocol-compatibility, and recovery entry
  points, then completed substantive Chinese companions for every site
  document and added a tested framework-neutral navigation map.

- A bounded Cargo C0 parser foundation that validates official publish framing
  and complete `.crate` gzip/tar archives, derives collision-safe immutable
  crate/version identity from normalized `Cargo.toml`, and translates current
  publish metadata into checksum-owned sparse-index rows. Official
  `cargo package` and `cargo publish` tests exercise the byte boundary without
  admitting Cargo to the public format catalog.
- A pinned, non-root Traefik Ingress for the Docker Desktop Kubernetes overlay,
  exposing the complete same-origin Console, API, and artifact surface at
  `artifact-gateway.localhost` with bounded resources and least-privilege RBAC.
- A bounded NuGet `.nupkg`/`.nuspec` parser with normalized, case-insensitive
  immutable package identity, plus an explicit staged roadmap that keeps the
  format undiscoverable until its executable protocol gates are complete.
- Idempotent local RustFS credential bootstrapping with retained rollback copies
  and a fail-closed guard for unsupported legacy object-store resources.
- A hardened Kustomize base and one-command local Kubernetes deployment with
  Gateway, Console, PostgreSQL, RustFS, idempotent migrations, persistent local
  volumes, health checks, manifest validation, and same-origin protocol routes.
- A staged APT Hosted roadmap covering native publication, atomic signed
  repository snapshots, external signing, lifecycle, scanning, quarantine,
  promotion, replication, and real APT client acceptance gates.
- Streaming Debian binary metadata parsing for gzip, xz, zstd, and uncompressed
  control archives, with server-derived package/version/architecture identity.
- The completed APT Hosted H1 pre-visibility foundation: idempotent quota-
  reserving publication sessions, explicit management-only Hosted provisioning,
  repository-scoped management and binary-safe generated
  OpenAPI clients, streaming `.deb` staging, explicit package/suite/component/
  architecture records, transactionally durable audit evidence, reference-
  checked RustFS orphan collection with heartbeat-fenced lifecycle-job retries
  and cross-instance integration coverage, immutable repository-snapshot records,
  and a private-key-free signer port. Installable Hosted publication remains
  gated on atomic signed snapshots.
- The completed APT Hosted H2 operator preview: deterministic `Packages` and gzip
  indices, Release checksum closure, Acquire-By-Hash objects, signed
  `InRelease`/`Release.gpg` assets, repository-global immutable pool paths,
  audited PostgreSQL visibility switching, Hosted GET/HEAD/range reads, and
  durable reference-checked cleanup of interrupted snapshot objects, a
  generated idempotent snapshot-publish API, loopback reference signer with an
  isolated persistent private key, Console/search/capacity projection, and a
  real signed Debian update/install gate. The gate now also proves exact
  PostgreSQL/RustFS recovery by publishing a later mutation, restoring the
  original signing evidence and every signed/index/package byte, and installing
  with the signer offline. Hosted remains unadvertised until H3 production key
  custody and rotation are complete.
- A pinned RustFS-only object-store baseline for Compose, integration tests,
  and Kubernetes, including streaming, metadata, Range, lifecycle, backup, and
  recovery contract coverage.
- One-command local development lifecycle targets for starting the complete
  stack, checking the Console and Gateway paths, and stopping only the
  checkout-managed Console.
- Administrator-only sanitized system diagnostics covering build identity,
  runtime roles, dependency reachability, node health, and repository job
  queues, with a bilingual Console view and copyable support JSON.
- Role-based `api`, `scheduler`, and `worker` deployments with format- and
  job-specific worker filters.
- PostgreSQL-backed runtime node heartbeats and an administrator inventory of
  node roles and Worker capabilities.
- Server-side aggregate repository management and cross-repository search APIs.
- Per-repository outbound proxy configuration and connectivity checks.
- npm Proxy repositories with verified read-through metadata and tarball
  caching, stale-if-error reads, negative caching, and offline installs.
- npm Group registries that merge Hosted and Proxy package versions with
  Hosted-first conflict resolution, anonymous/grant filtering, and Group-local
  tarball URLs.
- PyPI Hosted, Proxy, and Group repositories with native `pip` upload and
  install flows, normalized project browsing, anonymous/grant filtering, and
  read-through package caching.
- npm and PyPI artifact lifecycle operations covering retention, tombstones,
  restore, object collection, promotion, and replication.
- Go Module Proxy and Group repositories with standard `GOPROXY` endpoints,
  verified read-through caching, offline resolution, anonymous/grant filtering,
  search, capacity accounting, deep-linked Console browsing, and a real Go CLI
  acceptance gate.
- APT Proxy and ordered Group repositories with byte-preserving `dists/` and
  `pool/` reads, conditional metadata revalidation, stale-if-error and range
  responses, anonymous/grant filtering, search, capacity accounting, and
  deep-linked Console browsing.
- Configurable external artifact scanning with durable, idempotent management
  jobs; isolated workers; native Raw, Maven, OCI, npm, PyPI, Go, and Conan asset
  resolution; and optimistic security-intelligence merging.
- An optional non-root Trivy reference-scanner Compose profile with loopback-
  only transport, immutable asset verification, persistent CycloneDX reports,
  license evidence, vulnerability findings, health metadata, and a persistent
  vulnerability database cache.
- Per-repository scan-on-publication policies for Maven, OCI, Raw, npm, PyPI,
  and Conan Hosted repositories, with idempotent lifecycle jobs, audit records,
  and capability-aware Console controls.
- Per-artifact scan status, manual rescan controls, and bounded reconciliation
  for publications that missed automatic scanning or need a failed scan retried.
- Bounded per-vulnerability scanner findings with severity-count consistency,
  immutable evidence persistence, and searchable bilingual Console details.
- Versioned per-artifact quarantine and release controls with optimistic
  concurrency, audit evidence, admission-time enforcement, and worker-time
  protection against queued promotion or replication publication.
- Versioned Hosted quarantine-read policies, disabled by default, with
  protocol-level GET/HEAD denial, aggregate npm/PyPI and Conan closure
  semantics, metadata filtering, Group anti-bypass behavior, PostgreSQL
  persistence, OpenAPI clients, and Console controls.
- Durable administrator-managed Webhook subscriptions for Artifact quarantine
  and release events, with transactional outbox persistence, encrypted HMAC
  secrets, SSRF-safe HTTPS delivery, bounded retry/dead-letter replay, cluster
  leases, OpenAPI clients, audits, and Console operations visibility.
- Local-user governance with profile metadata, case-insensitive identities,
  failed-sign-in lockout, last-sign-in and password-change timestamps,
  mandatory password changes, administrator password resets, revocable
  versioned sessions, and last-active-administrator protection.

### Changed

- Routed APT requests through both the Vite development proxy and the
  production Console container so direct package-client paths cannot fall
  through to the SPA.
- Replaced default coordinate-and-digest entry in repository scanning,
  promotion, and replication workflows with a shared searchable immutable-
  artifact picker backed by protocol-owned canonical identity queries, including
  historical npm/PyPI versions, locally cached Proxy assets, and Conan revisions,
  while retaining advanced exact-identity input as a recovery path.
- Added a discoverable repository Scanning workspace for manual immutable-
  artifact scans, capability and configuration guidance, historical backfill,
  and recent job status.
- Updated retention controls to use Maven, OCI, Conan, Raw, npm, PyPI, and Go
  cleanup-unit terminology instead of Maven fallback copy.
- Reorganized the repository security tab into separate quarantine-read and
  promotion-admission guardrails with format-aware scope, explicit saved and
  unsaved states, and contextual scanner availability.
- Corrected the repository scanning and security layouts with frameless tab
  surfaces, desktop alert and guardrail grids, and overflow-free single-column
  behavior on narrower viewports.
- Unified scan and promotion-intelligence lifecycle execution around shared
  claim, lease, metrics, terminal-state, polling, and PostgreSQL notification
  semantics, so queued work starts promptly without sacrificing polling-based
  recovery.
- Reduced Console repository request fan-out and split large vendor bundles for
  faster navigation and initial loading.
- Compacted repository detail navigation and added restrained interface motion.
- Streamed artifact uploads and bounded database, HTTP, and background-worker
  resource usage.
- Added package-level Go coverage floors and Console lint, formatting,
  accessibility, component-test, and coverage gates.
- Added session-aware distributed node inventory, graceful offline state,
  retention cleanup, and cluster capability health summaries.
- Reworked Console user management around server-side search, filtering, and
  pagination plus a focused account drawer for profile and security actions.
- Isolated artifact publication locks in a bounded, observable PostgreSQL pool
  and made multi-file object and coordinate locks share one backend session.
- Hardened the Raw large-object byte plane with bounded-buffer temporary
  staging, streaming cache publication and reads, true upstream `HEAD`,
  renewable per-request locks, and configurable per-Gateway staging admission
  with `503`, `Retry-After`, and a bounded rejection metric. Raw and OCI
  resumable uploads now persist immutable offset chunks and assemble them once
  at completion instead of rewriting every prior byte for each PATCH; durable
  Raw reclaim now removes residual chunks from completed, cancelled, or expired
  sessions while preserving their PostgreSQL trace. The performance report now
  includes warm 64 MiB Hosted reads and a controlled HTTPS Raw Proxy cold miss
  with verified single-flight and offline cache replay.

### Fixed

- Made Maven Hosted publication Nexus-compatible by default: successful
  standard Maven/Gradle uploads are directly readable without a companion
  integration. Added the default-disabled `mavenStrictPublication` repository
  switch for teams that prefer Gateway coordinate commits and atomic
  per-coordinate visibility, plus bilingual guidance and real-client coverage
  for both modes.
- Generated Maven-compatible SNAPSHOT metadata using the latest timestamped
  version value and separate extension/classifier fields, so standard Maven
  clients can resolve POM, JAR, sources, and javadoc assets while older
  immutable builds remain directly addressable; generated metadata now also
  serves SHA-512, SHA-256, SHA-1, and MD5 sidecars through Hosted and Group
  routes for warning-free client verification.
- Served npm package-version metadata through Hosted, Proxy, and Group routes,
  including cold proxy resolution and group tarball URL rewriting, so Corepack
  can install pinned package-manager versions through Artifact Gateway.
- Replaced expired native Maven staging sessions on the next authenticated PUT
  so interrupted publishes can retry without remaining permanently blocked by
  the expired coordinate lock.
- Resolved npm Proxy and Group tarballs directly from a cold `package-lock.json`
  URL before any packument request, accepted canonical single-root manifest
  layouts used by legacy scoped packages plus harmless dot segments emitted by
  official packages, retained valid versions when unrelated legacy metadata
  lacks modern integrity, removed dist-tags that target skipped versions,
  requested bounded install metadata and accepted large public packuments,
  retained online-to-offline `npm ci` caching, and emitted one member-owned
  terminal audit for metadata failures.
- Honored repeated OCI `Accept` request headers when selecting manifest media
  types, matching Docker clients that send one header field per supported type.
- Prevented OpenAPI contract checks from reinstalling dependencies underneath
  a running Vite Console, and replaced the default lazy-route exception page
  with a bilingual recovery screen.

- Resolved public artifact deep links even when the target coordinate is beyond
  the first browse page.
- Fixed PostgreSQL OCI upload expiry scans so scheduled reclaim jobs are
  created and completed reliably across worker instances.
- Stabilized generated OpenAPI output, dependency installation, integration
  readiness, and lifecycle ordering in CI.
- Included PyPI and Go object usage in aggregate repository capacity views.
- Prevented PyPI promotion, replication, and restore from publishing or
  restoring a partial version when its file membership changes mid-operation;
  parked replication plans now refresh checkpoints on exact replay.
- Corrected user-management audits so the actor is the administrator performing
  the action and self-service password changes use their own audit resource.

### Security

- Updated the Go toolchain and release images to 1.26.6 so release-readiness
  checks run on the patched standard library baseline.
