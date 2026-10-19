import type { MigrationMessageKey } from "./migration.zh-CN";

export const migrationEnUS: Record<MigrationMessageKey, string> = {
  "migration.tab": "Migration sources",
  "migration.sources": "Nexus migration sources",
  "migration.scope": "Connect a source and preview its scope",
  "migration.scopeHint":
    "Read source configuration and asset metadata. Discovery and mapping previews do not copy bytes or create target repositories.",
  "migration.newSource": "Add migration source",
  "migration.editSource": "Edit migration source",
  "migration.sourceName": "Source name",
  "migration.baseUrl": "Nexus HTTPS address",
  "migration.baseUrlHint":
    "Enter the instance address and optional context path, without credentials, query or fragment. Private networks need an explicit server allowlist.",
  "migration.credentials": "Source credentials",
  "migration.noCredentials": "Use no credentials",
  "migration.keepCredentials": "Keep current credentials",
  "migration.replaceCredentials": "Set new credentials",
  "migration.clearCredentials": "Clear current credentials",
  "migration.credentialsConfigured": "Credentials configured",
  "migration.credentialsAbsent": "No credentials configured",
  "migration.credentialHint":
    "Credentials are never read back or saved in the browser. Inputs clear after every submission; re-enter them after a failure. Changing the source address requires replacing or clearing existing credentials.",
  "migration.username": "Source username",
  "migration.password": "Source password",
  "migration.save": "Save migration source",
  "migration.create": "Create migration source",
  "migration.cancel": "Cancel",
  "migration.edit": "Edit source",
  "migration.delete": "Delete source",
  "migration.deleteTitle": "Delete migration source?",
  "migration.deleteHint":
    "Delete the connection and encrypted credentials for {name}. Source Nexus configuration and content are unaffected.",
  "migration.refresh": "Refresh source",
  "migration.reloadLatest": "Read latest version",
  "migration.moreSources": "Load more migration sources",
  "migration.noSources": "No migration sources configured",
  "migration.noSourcesHint":
    "Add a Nexus 3 source to inspect credential-visible repositories and preview native target mappings.",
  "migration.selectSource": "Choose a migration source to inspect",
  "migration.selectSourceHint":
    "Connections are saved on the server. A preflight is a read-only observation, not evidence of migration or Nexus retirement readiness.",
  "migration.version": "Connection version {version}",
  "migration.preflight": "Run preflight",
  "migration.preflightTitle": "Repository scope and explicit mapping",
  "migration.preflightEmpty": "Preflight has not run",
  "migration.preflightEmptyHint":
    "Read credential-visible repositories, formats and limitations, then select repositories for discovery or mapping preview.",
  "migration.visibleScope":
    "Only the current credential-visible scope; global inventory completeness is unverified. Snapshot connection version {version}.",
  "migration.noRepositories": "No repositories visible to these credentials",
  "migration.sourceRepository": "Source repository",
  "migration.targetName": "Native target name",
  "migration.targetNameHint":
    "Lowercase letters, digits and hyphens. Enter explicit names; old URLs are not inherited. Select up to 10 source repositories.",
  "migration.chooseRepository": "Select {name}",
  "migration.targetFor": "Target name for {name}",
  "migration.members": "Source member order",
  "migration.groupHint":
    "Select the members and enter their target names. The preview preserves this member order.",
  "migration.supported": "Hosted discovery supported",
  "migration.previewOnly": "Configuration preview only",
  "migration.unsupported": "Unsupported",
  "migration.discover": "Discover selected Hosted content ({count})",
  "migration.preview": "Preview explicit mappings",
  "migration.discoveryTitle": "Source asset discovery",
  "migration.discoverySummary":
    "{assets} source assets, {bytes} total metadata size; {unsupported} explicitly unsupported paths. No bytes have been downloaded or verified.",
  "migration.path": "Source path",
  "migration.size": "Source metadata size",
  "migration.reason": "Handling notes",
  "migration.noAssets": "No supported assets returned in the selected scope",
  "migration.unsupportedAssets": "Explicitly unsupported assets",
  "migration.mappingTitle": "Mapping preview results",
  "migration.mappingHint":
    "Preview validates names, formats, conflicts and ordered members without applying mappings. Permissions, credentials and manual fields need explicit configuration.",
  "migration.mappingValid":
    "Selected mappings pass current validation. No targets have been created and no content has migrated.",
  "migration.mappingInvalid":
    "Mappings contain conflicts or unsupported items. Adjust them and preview again.",
  "migration.ready": "Passes current validation",
  "migration.manual": "Manual configuration required",
  "migration.invalid": "Invalid mapping",
  "migration.targetMembers": "Target member order: {members}",
  "migration.observation":
    "Read-only observation: {requests} requests, {pages} pages, {bytes} received.",
  "migration.previousResult":
    "Keeping the last successful result. The latest request failed; this result has not been updated.",
  "migration.failed":
    "The request did not complete. Check the network and service status, then retry. Errors never display source credentials.",
  "migration.denied":
    "This migration source requires a platform administrator identity.",
  "migration.notFound":
    "The migration source no longer exists. Refresh the source list.",
  "migration.versionConflict":
    "Another operation updated this connection. Read the latest version before submitting again; re-enter any credentials.",
  "migration.invalidSource":
    "Invalid connection. Check its HTTPS address, name and credentials, and re-enter any credentials being set.",
  "migration.replaceRequired":
    "Changing the source address requires explicitly replacing or clearing configured credentials.",
  "migration.sourceChanged":
    "The source connection version changed. Refresh the connection and run preflight again.",
  "migration.upstreamDenied":
    "Source access was denied. Check credentials, read-only permissions and the server egress allowlist.",
  "migration.sourceUnavailable":
    "Source read failed. Check Nexus connectivity, TLS and read-only API permissions, then retry.",
  "migration.budget":
    "Discovery exceeded its safety budget or pagination was invalid. Select fewer repositories and retry. No partial success was returned.",
  "migration.keyUnavailable":
    "Server credential encryption is unavailable. Contact the platform administrator.",
  "migration.lookupUnavailable":
    "Target conflicts could not be checked. Restore target metadata access and preview again.",
  "migration.reasonUnsupported":
    "This format, repository type or source configuration is unsupported. Inspect the source configuration before deciding how to proceed.",
  "migration.reasonHostedInventory":
    "Source configuration and asset metadata are readable. No content has migrated.",
  "migration.reasonReleasePolicy":
    "Only release versions in Maven RELEASE/MIXED repositories can be discovered. SNAPSHOT or unknown policies need separate handling.",
  "migration.reasonSnapshot":
    "Maven SNAPSHOT content is outside this migration scope.",
  "migration.reasonMetadata":
    "Shared Maven metadata or paths without a single release version need separate handling.",
  "migration.reasonManual":
    "Configure native policies and credentials explicitly.",
  "migration.reasonFieldManual":
    "This source field requires manual configuration.",
  "migration.reasonFieldExpressible":
    "Expressible through native configuration.",
  "migration.reasonFieldUnsupported": "This source field is unsupported.",
  "migration.reasonConflict":
    "An existing target conflicts in format, type or state.",
  "migration.reasonName": "The target name is invalid or duplicated.",
  "migration.reasonFormat":
    "The target format must match the source repository.",
  "migration.reasonType": "The target type must match the source repository.",
  "migration.reasonMembers":
    "Map all Group members explicitly, preserving their source order and format.",
  "migration.reasonExistingGroup":
    "The existing Group needs explicit configuration.",
  "migration.reasonMembersOnly": "Only a Group may have members.",
};
