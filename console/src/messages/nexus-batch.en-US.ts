import type { NexusBatchMessageKey } from "./nexus-batch.zh-CN";
export const nexusBatchEnUS: Record<NexusBatchMessageKey, string> = {
  "nexusBatch.sourceView": "Connections and mappings",
  "nexusBatch.batchView": "Batches and acceptance",
  "nexusBatch.title": "Nexus migration batches",
  "nexusBatch.background": "Batches continue in the background",
  "nexusBatch.backgroundHint":
    "Closing this page or stopping polling does not cancel work. Pause stops new entries; an in-flight entry may finish. Cancellation preserves published target content.",
  "nexusBatch.new": "Create migration batch",
  "nexusBatch.useMappings": "Create a batch with {count} Hosted mappings",
  "nexusBatch.rescan": "Rescan and catch up",
  "nexusBatch.rescanHint":
    "Create a new batch and fully enumerate the source. Identical identities are reverified, conflicts are preserved, and source deletions are reported only.",
  "nexusBatch.start": "Apply mappings and create batch",
  "nexusBatch.close": "Close",
  "nexusBatch.empty": "No migration batches yet",
  "nexusBatch.emptyHint":
    "Save and inspect a source, bind real Hosted targets, then create a batch using verified storage capacity evidence.",
  "nexusBatch.select": "Choose a batch to inspect progress and reports",
  "nexusBatch.refresh": "Refresh batches",
  "nexusBatch.more": "Load more batches",
  "nexusBatch.readLatest": "Read latest batch",
  "nexusBatch.source": "Saved migration source",
  "nexusBatch.target": "Existing Hosted target repository",
  "nexusBatch.targetFor": "Target repository for {name}",
  "nexusBatch.targetHint":
    "Bind real, active Hosted repositories of the same format. Create missing targets explicitly in repository management first; Nexus policies and permissions are not inherited.",
  "nexusBatch.manageRepositories": "Open repository management",
  "nexusBatch.moreRepositories": "Load more target repositories",
  "nexusBatch.moreSources": "Load more sources",
  "nexusBatch.mappingSummary": "{source} → {target}",
  "nexusBatch.explicitMapping":
    "Mappings become immutable batch inputs. Selected Maven releases, Raw, OCI, Conan 2 and npm Hosted content are copied. npm packages commit as complete version sets; different existing tags or version metadata conflict. Configure Proxy/Group separately.",
  "nexusBatch.capabilities": "Execution capability and capacity evidence",
  "nexusBatch.disabled": "Migration writes are disabled in this deployment",
  "nexusBatch.disabledHint":
    "Configure a real object-storage pool and enable a migration worker. Connections, inventory observations and existing reports remain readable; unknown capacity cannot start work.",
  "nexusBatch.binding": "Target {target} · Storage pool {pool}",
  "nexusBatch.capacityHint":
    "Enter external evidence verified for this real storage pool. Every byte field must be known (enter 0 only for an explicit zero). Download peak must cover at least the largest full source-asset spool. This is neither a Gateway measurement nor a reservation. Do not substitute log or temporary-mount capacity.",
  "nexusBatch.observedAt": "Capacity observation time (with time zone)",
  "nexusBatch.validUntil": "Evidence expiry time (with time zone)",
  "nexusBatch.timeHint":
    "Use ISO timestamps with a time zone. Observations cannot be in the future and validity is at most one hour. Resume/retry needs a strictly newer observation.",
  "nexusBatch.freeBytes": "Free storage-pool bytes",
  "nexusBatch.downloadBytes": "Peak download bytes",
  "nexusBatch.uploadBytes": "Peak upload bytes",
  "nexusBatch.backupBytes": "Peak backup bytes",
  "nexusBatch.restoreBytes": "Peak restore bytes",
  "nexusBatch.headroomBytes": "Headroom bytes",
  "nexusBatch.required":
    "Enter a verified value. Unknown values cannot start or resume work.",
  "nexusBatch.invalidNumber":
    "Enter nonnegative whole bytes within the browser's exact integer range.",
  "nexusBatch.invalidTime":
    "Enter a valid ISO timestamp including a time zone.",
  "nexusBatch.expired":
    "Evidence expired, is from the future, or exceeds the one-hour validity window. Verify a new observation.",
  "nexusBatch.newerEvidence":
    "Resume/retry requires a strictly newer observation than the previous evidence.",
  "nexusBatch.previousEvidence":
    "Previous observation: {time}. Old evidence is not prefilled.",
  "nexusBatch.pause": "Pause batch",
  "nexusBatch.resume": "Resume batch",
  "nexusBatch.retry": "Retry eligible failed entries",
  "nexusBatch.retryHint":
    "An unsealed scan restarts full scanning in the same batch. Once sealed, only failed/source-missing entries below the {attempts}-attempt limit are requeued. Conflicts and unsupported entries are never overwritten.",
  "nexusBatch.cancel": "Cancel batch",
  "nexusBatch.cancelTitle": "Cancel this migration batch?",
  "nexusBatch.cancelHint":
    "Stop new work and fence subsequent publication. Committed identities and source content are preserved. This does not delete or roll back data.",
  "nexusBatch.scanning": "Scanning and verifying source",
  "nexusBatch.ready": "Waiting for a background worker",
  "nexusBatch.running": "Migrating",
  "nexusBatch.pausing": "Draining the current entry",
  "nexusBatch.paused": "Paused",
  "nexusBatch.blocked": "Blocked; action required",
  "nexusBatch.completed": "Background batch completed",
  "nexusBatch.failed": "Background batch failed",
  "nexusBatch.cancelled": "Cancelled",
  "nexusBatch.version": "Batch version {version}",
  "nexusBatch.updated": "Last server update: {time}",
  "nexusBatch.scanned": "Source metadata records discovered",
  "nexusBatch.planned": "Sealed planned entries",
  "nexusBatch.total": "Unique supported primary source bytes",
  "nexusBatch.unknown": "Unknown; complete manifest is not sealed",
  "nexusBatch.sourceVerification":
    "Source-verification bytes read during scanning",
  "nexusBatch.sourceVerificationHint":
    "Additional source reads to fill missing full hashes in the manifest. Recorded even for incomplete or failed scans; these are not copied bytes.",
  "nexusBatch.transferred": "Actual copy-stage source bytes read",
  "nexusBatch.transferredHint":
    "Includes retries and source revalidation of existing identities; this can exceed the unique content total.",
  "nexusBatch.verified":
    "Unique target bytes with complete readback verification",
  "nexusBatch.visible": "Visible target identities",
  "nexusBatch.pending": "Pending",
  "nexusBatch.uploaded": "Uploaded; publication incomplete",
  "nexusBatch.entryVerified": "Target verified; publication incomplete",
  "nexusBatch.committed": "Published",
  "nexusBatch.skipped": "Skipped after full revalidation",
  "nexusBatch.conflict": "Target conflict; preserved",
  "nexusBatch.entryFailed": "Failed",
  "nexusBatch.unsupported": "Unsupported",
  "nexusBatch.sourceMissing": "Source missing during read",
  "nexusBatch.sourceDeleted": "Source deletion reported by a later scan",
  "nexusBatch.partial":
    "Conflicts, failures, unsupported content or source changes remain. This does not prove complete catch-up.",
  "nexusBatch.entries": "Batch entries and complete hashes",
  "nexusBatch.entry": "Entry",
  "nexusBatch.state": "Server state",
  "nexusBatch.attempts": "Attempt {count} / {max}",
  "nexusBatch.asset": "Asset",
  "nexusBatch.noEntries": "No executable entries yet",
  "nexusBatch.noEntriesHint":
    "An empty entry list before complete scanning and manifest sealing does not prove an empty source.",
  "nexusBatch.loaded": "Loaded {loaded} / {total} planned entries",
  "nexusBatch.moreEntries": "Load next entry page",
  "nexusBatch.allEntries": "Read complete inventory",
  "nexusBatch.sourcePath": "Source path",
  "nexusBatch.targetPath": "Target path",
  "nexusBatch.sha256": "Complete SHA-256",
  "nexusBatch.assetVerified": "Target fully read back and verified",
  "nexusBatch.assetUploaded": "Target bytes uploaded",
  "nexusBatch.assetUnverified": "Target bytes are unverified",
  "nexusBatch.report": "Migration acceptance observation report",
  "nexusBatch.export": "Export current observation report",
  "nexusBatch.reportHint":
    "Exports server batch facts, loaded inventory and current target endpoints, without source connection credentials. Missing acceptance evidence remains unverified.",
  "nexusBatch.manifest": "Sealed manifest SHA-256",
  "nexusBatch.inventoryIncomplete":
    "Inventory is not fully loaded or scanning is not sealed. This report is not complete inventory acceptance.",
  "nexusBatch.coldClient":
    "Target-only cold-client acceptance for this batch: unverified",
  "nexusBatch.catchup":
    "Final catch-up after freezing source writes: unverified",
  "nexusBatch.cutover": "Cutover and rollback data boundaries",
  "nexusBatch.cutoverHint":
    "Explicitly freeze source writes, run a final complete rescan, and verify cold clients. After new target writes, reconcile and copy those writes back before reverting to the source. Changing endpoints alone loses those new writes.",
  "nexusBatch.residual": "Residual Nexus dependencies and consumer changes",
  "nexusBatch.residualHint":
    "Check Proxy/Group upstreams and members, unsupported formats/SNAPSHOTs, permissions and credentials, CI Maven/Gradle/Raw settings, and source publishers. Switch consumers to native target endpoints. This page does not change CI/DNS/Ingress or prove Nexus can be retired.",
  "nexusBatch.endpoints":
    "Native target endpoints at the current Console origin",
  "nexusBatch.endpointsHint":
    "Derived from real target repositories. Confirm public addresses and authentication before changing consumers. A reachable endpoint is not cold-client acceptance evidence for this batch.",
  "nexusBatch.targetUnavailable":
    "Target details are unavailable; its endpoint and configuration are unconfirmed.",
  "nexusBatch.requestFailed":
    "The batch request did not complete. Read current server state before retrying. No source credentials or upstream response body are displayed.",
  "nexusBatch.unknownOutcome":
    "The submission outcome is unconfirmed. Refresh batches first. Retrying unchanged inputs reuses the same request to avoid duplicate creation or control.",
  "nexusBatch.versionConflict":
    "A worker or another administrator updated the batch. A server read was attempted; select the operation again and check the new version. Writes are never automatically resent.",
  "nexusBatch.idempotencyConflict":
    "This request identifier was used with different content. Read server facts before starting another operation.",
  "nexusBatch.capacityUnknown":
    "Capacity is unknown and target writes are blocked. Verify every capacity field.",
  "nexusBatch.capacityInsufficient":
    "Target pool capacity or repository quota is insufficient. Restore capacity or verify fresh evidence before resuming.",
  "nexusBatch.capacityBinding":
    "Evidence does not match the real target/storage pool. Read capability again and verify its binding.",
  "nexusBatch.sourceChanged":
    "The source connection version changed. Inspect it again and explicitly create a new batch; this batch cannot continue.",
  "nexusBatch.invalidState":
    "The current server state does not allow this action. Read the latest batch.",
  "nexusBatch.sourceFailure":
    "Source reads, hashes or membership changed. Inspect the source before proceeding; target conflicts were preserved.",
  "nexusBatch.scanFailure": "Most recent durable scan failure",
  "nexusBatch.scanStageLabel": "Failure stage",
  "nexusBatch.scanCode": "Bounded diagnostic code",
  "nexusBatch.scanScope": "Selected repository scan scope",
  "nexusBatch.scanOccurred": "Failure observed at",
  "nexusBatch.scanMetadata": "Source metadata discovery",
  "nexusBatch.scanManifest": "Manifest planning",
  "nexusBatch.scanOpen": "Open source verification stream",
  "nexusBatch.scanRead": "Read source verification stream",
  "nexusBatch.scanDigest": "Check source asset digest",
  "nexusBatch.scanVersion": "Revalidate source connection version",
  "nexusBatch.scanNPM": "Check the npm packument and complete version set",
  "nexusBatch.scanPyPISimple": "Check the Python package index",
  "nexusBatch.scanPyPIDistribution": "Verify the Python distribution identity",
  "nexusBatch.pypiClosure":
    "The PyPI release files or index metadata are incomplete or inconsistent. Check the source release and rescan.",
  "nexusBatch.pypiIdentity":
    "The archive metadata does not match the PyPI project and version. Check the source distribution and rescan.",
  "nexusBatch.pypiAdministrator":
    "PyPI migration requires an active administrator account or administrator API key. Check the batch or schedule owner's current access.",
  "nexusBatch.pypiAuthorityOrTarget":
    "The PyPI migration owner or target is unavailable. Check the owner's current access and that the target is an active Hosted repository.",
  "nexusBatch.npmClosure":
    "The npm metadata and version set are incomplete or inconsistent. Check read access to the selected repository and every version, then rescan.",
  "nexusBatch.npmIntegrity":
    "The npm bytes do not match the source shasum or SRI. This package was not published. Inspect the source and rescan.",
  "nexusBatch.npmIdentity":
    "The npm component, packument and archive identities differ. This package was not published. Check the source package name and version.",
  "nexusBatch.scanGraph": "Validate the complete OCI dependency graph",
  "nexusBatch.scanInterrupted":
    "The scan was interrupted and never sealed. Fresh capacity evidence allows a full rescan of the same batch.",
  "nexusBatch.pageFailure":
    "Inventory pagination is incomplete or repeated. Refresh and read again; it will not be exported as complete inventory.",
  "nexusBatch.targetConflict":
    "The target has a different identity or bytes. It was preserved and reported as a conflict.",
  "nexusBatch.unsupportedReason":
    "The entry does not meet its native format's complete migration requirements. It remains unsupported and needs separate handling.",
};
