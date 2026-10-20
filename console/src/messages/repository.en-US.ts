import type { RepositoryMessageKey } from "./repository.zh-CN";

export const repositoryEnUS: Record<RepositoryMessageKey, string> = {
  "repository.copied": "Copied",
  "repository.copy": "Copy",
  "repository.publishWithDockerOrPodman": "Publish with Docker or Podman",
  "repository.askAnAdministratorForAServiceAccountCredential":
    "Ask an administrator for a service-account credential with write access to this repository, delivered through a secure channel. The SSO browser session is not a Docker credential.",
  "repository.signInToRegistry": "Sign in to registry",
  "repository.tagAndPush": "Tag and push",
  "repository.configureNpmRegistry": "Configure npm registry",
  "repository.useAGatewayAPIKeyOrResolverToken":
    "Use a Gateway API key or resolver token for authentication.",
  "repository.publish": "Publish",
  "repository.configurePyPIRepository": "Configure PyPI repository",
  "repository.anonymousPipReadsNeedNoCredentialsTwineUses":
    "Anonymous pip reads need no credentials; twine uses any non-empty username with the resolver token.",
  "repository.configureCargoRegistry": "Configure Cargo registry",
  "repository.askAnAdministratorForRepositoryWriteAccessFor":
    "Ask an administrator for repository write access. For private reads and publishing, inject CARGO_REGISTRIES_GATEWAY_TOKEN as Bearer followed by the Gateway token through a secret manager; never put it in the config file. Repository grants manage access; cargo owner is unsupported.",
  "repository.publishAndRead": "Publish and read",
  "repository.yankAndRestoreAVersion": "Yank and restore a version",
  "repository.fileName": "File name",
  "repository.size": "Size",
  "repository.actions": "Actions",
  "repository.remove": "Remove",
  "repository.status": "Status",
  "repository.uploaded": "Uploaded",
  "repository.pending": "Pending",
  "repository.declareCoordinateAndObjects": "Declare coordinate and objects",
  "repository.uploadFiles": "Upload files",
  "repository.complete": "Complete",
  "repository.mavenCoordinate": "Maven coordinate",
  "repository.groupArtifactVersionForExampleComAcmeWidget":
    "group:artifact:version, for example com.acme:widget:1.0.0",
  "repository.pOMObjectName": "POM object name",
  "repository.theStagedFileUsedAsThePOM": "The staged file used as the POM",
  "repository.selectAPOMFileFirst": "Select a POM file first",
  "repository.releaseFiles": "Release files",
  "repository.selectThePOMJARSourceArchiveAndOther":
    "Select the POM, JAR, source archive, and other files; SHA-256 is calculated in the browser",
  "repository.chooseReleaseFiles": "Choose release files",
  "repository.createPublishSession": "Create publish session",
  "repository.session": "Session",
  "repository.expires": "Expires: ",
  "repository.allFilesUploaded": "All files uploaded",
  "repository.uploadAllFiles": "Upload all files",
  "repository.commitRelease": "Commit release",
  "repository.discardThisRelease": "Discard this release?",
  "repository.localSelectionsAndSessionDetailsWillBeCleared":
    "Local selections and session details will be cleared. Uploaded temporary objects expire on the server.",
  "repository.discardRelease": "Discard release",
  "repository.continueEditing": "Continue editing",
  "repository.discardAndStartOver": "Discard and start over",
  "repository.releasePublished": "Release published",
  "repository.publishAnother": "Publish another",
  "repository.isUnavailable": "{feature} is unavailable",
  "repository.thisConsoleDoesNotSupportTheFormatYet":
    "This Console does not support the {unsupportedFormat} format yet",
  "repository.theCurrentBackendDoesNotExposeThisEndpoint":
    "The current backend does not expose this endpoint (404)",
  "repository.newRepository": "New repository",
  "repository.cancel": "Cancel",
  "repository.create": "Create",
  "repository.type": "Type",
  "repository.hosted": "Hosted",
  "repository.proxy": "Proxy",
  "repository.hostAndPublishArtifacts": "Host and publish artifacts",
  "repository.fetchAndCacheFromUpstream": "Fetch and cache from upstream",
  "repository.repositoryName": "Repository name",
  "repository.lowercaseLettersNumbersAndHyphensForExampleTeam":
    "Lowercase letters, numbers, and hyphens, for example team-images",
  "repository.format": "Format",
  "repository.strictPublication": "Strict publication",
  "repository.offByDefaultSuccessfulStandardMavenGradleUploads":
    "Off by default: successful standard Maven/Gradle uploads are immediately readable for Nexus-compatible migration. When enabled, a coordinate stays hidden until its Gateway commit succeeds.",
  "repository.upstreamEndpoint": "Upstream endpoint",
  "repository.externalProxySource": "External proxy source",
  "repository.commaSeparatedHostnamesRequiredForRawConanNpm":
    "Comma-separated hostnames; required for Raw, Conan, npm, PyPI, Go, Cargo, and APT proxies. PyPI normally also requires files.pythonhosted.org; Cargo normally also requires static.crates.io.",
  "repository.name": "Name",
  "repository.capacity": "Capacity",
  "repository.deleting": "Deleting",
  "repository.repositories": "Repositories",
  "repository.aUnifiedViewOfHostedAndProxyRepositories":
    "A unified view of hosted and proxy repositories",
  "repository.repositories2": "Repositories",
  "repository.archived": "{deletedCount} archived",
  "repository.active": "{activeCount} active",
  "repository.proxyRepositories": "Proxy repositories",
  "repository.upstreamCachesAndMirrors": "Upstream caches and mirrors",
  "repository.storageUsed": "Storage used",
  "repository.objects": "{number} objects",
  "repository.capacityUnavailable": "Capacity unavailable",
  "repository.clearFilters": "Clear filters",
  "repository.search": "Search",
  "repository.nameFormatOrRepositoryType": "Name, format, or repository type…",
  "repository.allFormats": "All formats",
  "repository.activeAndDeleting": "Active and deleting",
  "repository.allStatuses": "All statuses",
  "repository.active2": "Active",
  "repository.deleted": "Deleted",
  "repository.noRepositories": "No repositories",
  "repository.noMatchingRepositories": "No matching repositories",
  "repository.aRepositoryDefinesAFormatAndPolicyBoundary":
    "A repository defines a format and policy boundary for publishing, proxying, and governing artifacts.",
  "repository.youHaveNoRepositoryGrantsYetAPlatform":
    "You have no repository grants yet. A platform administrator assigns access repository by repository; ask one to grant yours.",
  "repository.deletedRepositoriesAreArchivedAndAvailableThroughThe":
    "Deleted repositories are archived and available through the status filter",
  "repository.adjustTheFiltersAndTryAgain": "Adjust the filters and try again",
  "repository.noMatchingRepositories2": "No matching repositories",
  "repository.adjustTheFiltersOrLoadMoreRepositories":
    "Adjust the filters or load more repositories",
  "repository.deleteRepository": "Delete repository",
  "repository.deleteRepository2": "Delete repository",
  "repository.readsAndWritesStopImmediatelyWhileTheRepository":
    "? Reads and writes stop immediately while the repository enters the deleting state. Background cleanup normally completes within one minute. This cannot be undone; audit metadata is retained.",
  "repository.delete2": "Delete",
  "repository.aFormatNamespaceContainingAccessPolicyArtifactsAnd":
    "A format namespace containing access policy, artifacts, and upstream configuration.",
  "repository.fetchesAndCachesUpstreamResponsesOnDemandPublishing":
    "Fetches and caches upstream responses on demand; publishing is disabled.",
  "repository.storesVerifiedPublishedArtifactsAndSupportsDeletionRestore":
    "Stores verified published artifacts and supports deletion, restore, and retention.",
  "repository.aUserVisibleLogicalIdentitySuchAsA":
    "A user-visible logical identity such as a Maven coordinate or OCI image.",
  "repository.anImmutableFileOrBlobUnderAnArtifact":
    "An immutable file or blob under an artifact, such as a JAR, POM, or image layer.",
  "repository.anIndexAndBytesForAnUpstreamResponse":
    "An index and bytes for an upstream response; it is not a hosted artifact.",
  "repository.turnsCompleteValidatedStagedContentIntoAVisible":
    "Turns complete, validated staged content into a visible artifact.",
  "repository.aRestorableDeletionRecordBytesAreReclaimedAfter":
    "A restorable deletion record; bytes are reclaimed after references are gone.",
  "repository.selectsExpiredVersionsOrPathsByFormatRules":
    "Selects expired versions or paths by format rules and creates a reviewable reclamation job.",
  "repository.concepts": "Concepts",
  "repository.viewConcepts": "View concepts",
  "repository.repositorySummary": "Repository summary",
  "repository.anonymousReads": "Anonymous reads",
  "repository.privateReads": "Private reads",
  "repository.protocolEndpoint": "Protocol endpoint",
  "repository.openPublicBrowser": "Open public browser",
  "repository.objects2": "{bytes} · {number} objects",
  "repository.viewCapacity": "View capacity",
  "repository.effectiveAccess": "Effective access",
  "repository.ociTokenExchange": "OCI token exchange",
  "repository.ociTokenBindingHint":
    "Bind a token service and audience for Registry Bearer challenges. This does not grant network access; allow the issuer host separately. Changing the binding requires a new upstream secret or clearing the stored credential.",
  "repository.ociTokenServiceURL": "Token service URL",
  "repository.ociTokenServiceURLHint":
    "The exact HTTPS realm in the upstream challenge. No username, password, query or fragment.",
  "repository.ociTokenAudience": "Token audience",
  "repository.ociTokenAudienceHint":
    "The exact service in the upstream challenge, without spaces or control characters.",
  "repository.upstreamCredentialToBoundIssuer":
    "Send this credential only to the bound token service. Registry content uses the exchanged pull token; cross-origin redirects strip credentials.",
  "repository.upstreamCredentialRebindRequired":
    "The upstream endpoint or OCI token binding changed. Enter a new secret, or choose Anonymous to clear the stored credential, then save again.",
  "repository.upstreamAuthentication": "Upstream authentication",
  "repository.attachACredentialToUpstreamFetchesWhenThe":
    "Authenticate to the configured Go, Maven or OCI upstream. Credentials are sent only to the bound source; cross-origin redirects strip them.",
  "repository.anonymous": "Anonymous",
  "repository.sendNoCredentialAndClearTheStoredOne":
    "Send no credential and clear the stored one",
  "repository.sendNoCredential": "Send no credential",
  "repository.basic": "Basic",
  "repository.sendAnHTTPBasicUsernameAndPassword":
    "Send an HTTP Basic username and password",
  "repository.bearerToken": "Bearer token",
  "repository.sendAuthorizationBearerToken":
    "Send Authorization: Bearer <token>",
  "repository.upstreamUsername": "Upstream username",
  "repository.upstreamPassword": "Upstream password",
  "repository.upstreamToken": "Upstream token",
  "repository.storedEncryptedWithAES256GCMLeaveBlank":
    "Write-only and encrypted at rest. Every save clears this input. Leave blank to keep the credential only when its binding and scheme are unchanged; otherwise enter it again.",
  "repository.configuredEnterAValueToReplaceIt":
    "Configured; enter a value to replace it",
  "repository.notConfigured": "Not configured",
  "repository.upstreamURL": "Upstream URL",
  "repository.hTTPSBaseURLChangesTakeEffectImmediatelyOn":
    "HTTPS base URL. Changing it requires a new upstream secret or clearing the stored credential. Saved changes apply on the next request.",
  "repository.allowedHosts": "Allowed hosts",
  "repository.commaSeparatedRequiredForRawConanNpmPyPI":
    "Comma-separated. Required for Raw, Conan, npm, PyPI, Go, Cargo, and APT proxies. Cargo normally needs index.crates.io and static.crates.io.",
  "repository.commaSeparatedOptionalForOCIAndMavenProxies":
    "Comma-separated. Optional for OCI and Maven proxies.",
  "repository.egressProxy": "Egress proxy",
  "repository.configureTheEgressProxyUsedWhenThisProxy":
    "Configure the egress proxy used when this proxy repository reaches its upstream, for private or restricted networks.",
  "repository.direct": "Direct",
  "repository.doNotUseAProxyRetainPrivateAddress":
    "Do not use a proxy; retain private-address protection",
  "repository.useEnvironmentVariables": "Use environment variables",
  "repository.useProcessLevelHTTPSPROXYAndNO":
    "Use process-level HTTP(S)_PROXY and NO_PROXY",
  "repository.customProxy": "Custom proxy",
  "repository.setAnHTTPOrSOCKS5ProxySpecificallyFor":
    "Set an HTTP or SOCKS5 proxy specifically for this repository",
  "repository.protocol": "Protocol",
  "repository.hTTPCONNECT": "HTTP (CONNECT)",
  "repository.proxyHost": "Proxy host",
  "repository.port": "Port",
  "repository.remoteDNSSocks5h": "Remote DNS (socks5h)",
  "repository.whenEnabledTheProxyResolvesTheUpstreamHostname":
    "When enabled, the proxy resolves the upstream hostname. Use this when the local DNS cannot reach the upstream network.",
  "repository.proxyUsernameOptional": "Proxy username (optional)",
  "repository.proxyPasswordOptional": "Proxy password (optional)",
  "repository.storedEncryptedWithAES256GCMLeaveBlank2":
    "Write-only and encrypted at rest. Every save clears this input. Leave blank to keep the current credential.",
  "repository.clearStoredProxyCredentials": "Clear stored proxy credentials",
  "repository.bypassListNoProxy": "Bypass list (noProxy)",
  "repository.commaSeparatedHostnameSuffixesOrCIDRsMatchingUpstreams":
    "Comma-separated hostname suffixes or CIDRs. Matching upstreams bypass the proxy.",
  "repository.testConnection": "Test connection",
  "repository.theTestUsesTheSavedConfiguration":
    "The test uses the saved configuration",
  "repository.proxyReachable": "Proxy reachable",
  "repository.upstreamReturned": "upstream returned {upstreamStatus}",
  "repository.latencyMs": "latency {latencyMs} ms",
  "repository.connectionFailed": "Connection failed: ",
  "repository.unknownError": "Unknown error",
  "repository.repositorySettingsSaved": "Repository settings saved",
  "repository.repositorySettings": "Repository settings",
  "repository.manageReadAccessAndUpstreamConnectivityForProxy":
    "Manage read access and upstream connectivity for proxy repositories. Repository name, format, and type cannot be changed after creation.",
  "repository.reset": "Reset",
  "repository.saveChanges": "Save changes",
  "repository.allowAnonymousReads": "Allow anonymous reads",
  "repository.whenEnabledProtocolGETHEADRequestsCanRead":
    "When enabled, protocol GET/HEAD requests can read this repository without credentials.",
  "repository.whenOffNexusStyleDirectVisibilityLetsStandard":
    "When off, Nexus-style direct visibility lets standard Maven/Gradle clients publish without an extra integration. When enabled, uploads stay hidden until the Gateway coordinate commit succeeds. Do not change this policy during an active publication.",
  "repository.supportedOperations": "Supported operations",
  "repository.invalidTargetPathEmptyDotDotDotBackslash":
    "Invalid target path: empty, dot, dot-dot, backslash, control, and bidirectional formatting characters are not allowed. Unicode, spaces, and parentheses are supported.",
  "repository.theTargetPathContainsCharactersThatCannotBe":
    "The target path contains characters that cannot be encoded. Change the file name or target path and try again.",
  "repository.theEncodedTargetPathCannotExceed4096Bytes":
    "The encoded target path cannot exceed 4096 bytes. Shorten the directories or file name.",
  "repository.uploadFailed400TheTargetDoesNotFollow":
    "Upload failed (400): the target does not follow the Raw path rules. Unicode, spaces, and parentheses are supported; remove empty, dot, dot-dot, backslash, or NUL segments and try again.",
  "repository.uploadFailed": "Upload failed",
  "repository.upload": "Upload",
  "repository.uploadTo": "Upload to",
  "repository.rawFileUploadFailed": "Raw file upload failed",
  "repository.file": "File",
  "repository.chooseFile": "Choose file",
  "repository.noFileSelected": "No file selected",
  "repository.targetPath": "Target path",
  "repository.relativeToTheRepositoryRootOneOptionalLeading":
    "Relative to the repository root. One optional leading / may explicitly represent the repository root and is removed on upload. Leave empty to use the file name. Unicode, spaces, and parentheses are supported up to 4096 encoded bytes; empty, dot, dot-dot, backslash, control, and bidirectional formatting characters are not.",
  "repository.theServerValidatesSHA256AnExistingPath":
    "The server validates SHA-256. An existing path with the same name will be overwritten.",
  "repository.artifactBrowseView": "Artifact browse view",
  "repository.directory": "Directory",
  "repository.list": "List",
  "repository.namespace": "Namespace",
  "repository.component": "Component",
  "repository.version": "Version",
  "repository.asset": "Asset",
  "repository.nodeDetails": "Node details",
  "repository.openInList": "Open in list",
  "repository.canonicalLocation": "Canonical location",
  "repository.localSources": "Local sources",
  "repository.orderedByVisibleMemberPriorityNotAnActual":
    "Ordered by visible member priority, not an actual download result.",
  "repository.differentContentAtThisPath": "Different content at this path",
  "repository.sourceRepository": "Source repository",
  "repository.cacheState": "Cache state",
  "repository.cached": "Cached",
  "repository.buildNumber": "Build number",
  "repository.contentType": "Content type",
  "repository.cacheUpdated": "Cache updated",
  "repository.updated": "Updated",
  "repository.assetPath": "Asset path",
  "repository.selectAnItemInTheDirectory": "Select an item in the directory",
  "repository.inspectItsFullNameSourcesAndVerificationDetails":
    "Inspect its full name, sources and verification details here.",
  "repository.arrowKeysToBrowseEnterToSelect":
    "Arrow keys to browse · Enter to select",
  "repository.published": "Published",
  "repository.cacheScope": "Cache scope",
  "repository.multipleLocalSources": "Multiple local sources",
  "repository.loadMore": "Load more",
  "repository.failedToLoadDirectory": "Failed to load directory",
  "repository.loadingDirectory": "Loading directory…",
  "repository.noBrowseableArtifacts": "No browseable artifacts",
  "repository.readableMembersHaveNoPublishedArtifactsOrLive":
    "Readable members have no published artifacts or live cache entries yet.",
  "repository.proxyDirectoriesShowOnlyFetchedAndRecordedCache":
    "Proxy directories show only fetched and recorded cache assets.",
  "repository.publishMavenArtifactsOrUploadRawFilesTo":
    "Publish Maven artifacts or upload Raw files to populate the format-aware directory.",
  "repository.repositoryDirectory": "Repository directory",
  "repository.artifactDirectory": "Artifact directory",
  "repository.memberPublicationsAndKnownCache":
    "Member publications and known cache",
  "repository.cachedAssetsOnly": "Cached assets only",
  "repository.exploreArtifactsByDirectory": "Explore artifacts by directory",
  "repository.collapseAllDirectories": "Collapse all directories",
  "repository.refresh": "Refresh",
  "repository.artifactDirectoryTree": "Artifact directory tree",
  "repository.entriesLoaded": "{loadedNodeCount} entries loaded",
  "repository.expandDirectoriesToLoadMore": "Expand directories to load more",
  "repository.enterAnInvalidationTarget": "Enter an invalidation target.",
  "repository.failedToInvalidateCache": "Failed to invalidate cache",
  "repository.enterAMavenGAVOrCachePath": "Enter a Maven GAV or cache path.",
  "repository.failedToClearNegativeCache": "Failed to clear negative cache",
  "repository.invalidateCache": "Invalidate cache",
  "repository.path": "Path",
  "repository.repository": "Repository",
  "repository.byPrefix": "By prefix",
  "repository.invalidate": "Invalidate",
  "repository.clearNegativeCache": "Clear negative cache",
  "repository.versionComponentAndRepositoryInvalidationsUseTheirMaven":
    "Version, component, and repository invalidations use their Maven cache prefixes. Only cache indexes are removed; byte objects are reclaimed later by the orphan collector.",
  "repository.cacheEntriesInvalidated":
    "{invalidateResult} cache entries invalidated.",
  "repository.negativeCacheEntriesCleared":
    "{negativeResult} negative-cache entries cleared.",
  "repository.enterAMavenGAVGroupIdArtifactIdVersionExtension":
    "Enter a Maven GAV (groupId:artifactId:version[:extension[:classifier]]) or a repository path.",
  "repository.cacheWarmUpRequestFailed": "Cache warm-up request failed",
  "repository.failedToRefreshCache": "Failed to refresh cache",
  "repository.warmCache": "Warm cache",
  "repository.warm": "Warm",
  "repository.forceRefresh": "Force refresh",
  "repository.checkUpstream": "Check upstream",
  "repository.primarySize": "Primary size",
  "repository.files": "Files",
  "repository.member": "Member",
  "repository.primaryFiles": "Primary files: ",
  "repository.mavenCoordinateUsage": "Maven coordinate usage",
  "repository.expand": "(expand)",
  "repository.fileDetails": "File details",
  "repository.checksumsSignatures": "Checksums / signatures ({length})",
  "repository.failedToLoadUpstreamStatus": "Failed to load upstream status",
  "repository.upstream": "Upstream",
  "repository.checking": "Checking…",
  "repository.health": "Health",
  "repository.reachable": "Reachable",
  "repository.unreachable": "Unreachable",
  "repository.cache": "Cache",
  "repository.enabled": "Enabled",
  "repository.disabled": "Disabled",
  "repository.usage": "Usage",
  "repository.theMavenProxyRequiresBasicAuthenticationUseAny":
    "The Maven proxy requires Basic authentication: use any non-empty username and the resolver token as the password.",
  "repository.directDownload": "Direct download",
  "repository.security": "Security",
  "repository.files2": "Files",
  "repository.files3": "{count} files",
  "repository.aPTPath": "APT path",
  "repository.digest": "Digest",
  "repository.firstCached": "First cached",
  "repository.versions": "Versions",
  "repository.versions2": "{versionCount} versions",
  "repository.openToInspect": "Open to inspect",
  "repository.latestVersion": "Latest version",
  "repository.filePath": "File path",
  "repository.lastUpdated": "Last updated",
  "repository.noMatchingArtifacts": "No matching artifacts",
  "repository.noArtifacts": "No artifacts",
  "repository.tryADifferentSearchTerm": "Try a different search term",
  "repository.failedToLoadProxyCache": "Failed to load proxy cache",
  "repository.artifactBrowser": "Artifact browser",
  "repository.search2": "Search…",
  "repository.primaryAssets": "Primary assets",
  "repository.allFiles": "All files",
  "repository.jAROnly": "JAR only",
  "repository.pOMOnly": "POM only",
  "repository.mavenVersionsShowing":
    "{number} Maven versions, showing {number2}",
  "repository.returnToFullList": "Return to full list",
  "repository.pageWithMavenVersionsPerPage":
    "Page {number} with {number2} Maven versions per page",
  "repository.quotaUpdated": "Quota updated",
  "repository.capacityManagement": "Capacity management",
  "repository.cacheUsage": "Cache usage",
  "repository.usedSpace": "Used space",
  "repository.cachedObjects": "Cached objects",
  "repository.objectCount": "Object count",
  "repository.quota": "Quota",
  "repository.unlimited": "Unlimited",
  "repository.utilization": "Utilization",
  "repository.quotaGiB0ForUnlimited": "Quota (GiB, 0 for unlimited)",
  "repository.updateQuota": "Update quota",
  "repository.artifactAddress": "Artifact address",
  "repository.downloads": "Downloads",
  "repository.totalBytes": "Total bytes",
  "repository.firstDownload": "First download",
  "repository.lastDownload": "Last download",
  "repository.lastDownloadedBy": "Last downloaded by",
  "repository.searchArtifactAddress": "Search artifact address",
  "repository.searchAddressCaseSensitive": "Search address (case-sensitive)",
  "repository.wholeRepositoryAddressesDownloads":
    "Whole repository: {number} addresses · {number2} downloads · {bytes}",
  "repository.noMatchingArtifactAddresses": "No matching artifact addresses",
  "repository.noDownloadsRecordedYet": "No downloads recorded yet",
  "repository.tryADifferentAddressSearch": "Try a different address search.",
  "repository.noDownloadsFromThisRepositoryHaveBeenRecorded":
    "No downloads from this repository have been recorded yet.",
  "repository.searchByPackagePathOrCoordinate":
    "Search by package, path, or coordinate",
  "repository.searching": "Searching…",
  "repository.noSelectableArtifacts": "No selectable artifacts",
  "repository.artifactSearchFailedChangeTheQueryAndTry":
    "Artifact search failed. Change the query and try again.",
  "repository.artifactIsQuarantinedReleaseItFirst":
    "Artifact is quarantined; release it first",
  "repository.policyIsDisabled": "Policy is disabled",
  "repository.signatureIsRequired": "Signature is required",
  "repository.aVerifiedSignatureIsRequired": "A verified signature is required",
  "repository.anSBOMIsRequired": "An SBOM is required",
  "repository.provenanceIsRequired": "Provenance is required",
  "repository.aVulnerabilityScanIsRequired": "A vulnerability scan is required",
  "repository.licenseInformationIsRequired": "License information is required",
  "repository.aLicenseIsNotAllowListed": "A license is not allow-listed",
  "repository.theVulnerabilityScanFailed": "The vulnerability scan failed",
  "repository.lowVulnerabilitiesFound": "Low vulnerabilities found",
  "repository.mediumVulnerabilitiesFound": "Medium vulnerabilities found",
  "repository.highVulnerabilitiesFound": "High vulnerabilities found",
  "repository.criticalVulnerabilitiesFound": "Critical vulnerabilities found",
  "repository.unknownSeverityVulnerabilitiesFound":
    "Unknown-severity vulnerabilities found",
  "repository.artifactIsQuarantinedAndCannotBePromotedOr":
    "Artifact is quarantined and cannot be promoted or replicated",
  "repository.securityPolicyAllowsPromotion":
    "Security policy allows promotion",
  "repository.securityPolicyBlocksPromotion":
    "Security policy blocks promotion",
  "repository.releaseTheArtifactFromItsDetailsThenEvaluate":
    "Release the artifact from its details, then evaluate admission again.",
  "repository.policyVersion": "Policy version",
  "repository.enforcement": "Enforcement",
  "repository.enabled2": "enabled",
  "repository.disabled2": "disabled",
  "repository.artifactIntelligence": "Artifact intelligence",
  "repository.found": "found",
  "repository.notFound": "not found",
  "repository.promotionSubmittedTrackItOnTheTargetRepository":
    "Promotion submitted. Track it on the target repository's Lifecycle jobs tab.",
  "repository.replicationPlanCreatedTrackItsProgressBelow":
    "Replication plan created. Track its progress below.",
  "repository.startPromotionReplication": "Start promotion / replication",
  "repository.selectAnImmutableArtifactAndATargetRepository":
    "Select an immutable artifact and a target repository of the same format. Promotion publishes a copy after target admission; replication creates a resumable plan without security approval.",
  "repository.sourceArtifact": "Source artifact",
  "repository.visiblePackagesInTheCurrentSuiteTypeA":
    "Visible packages in the current suite; type a package name to filter.",
  "repository.upTo50ItemsAreShownTypeA":
    "Up to 50 items are shown. Type a package, path, or coordinate prefix to narrow the results.",
  "repository.searchAndSelectASourceArtifact":
    "Search and select a source artifact",
  "repository.selectAPackageInThisSuite": "Select a package in this suite",
  "repository.targetHostedRepository": "Target Hosted repository",
  "repository.selectTargetRepository": "Select target repository",
  "repository.selectARepositoryWithTheSameFormat":
    "Select a repository with the same format…",
  "repository.noCompatibleHostedRepositoryIsAvailable":
    "No compatible Hosted repository is available",
  "repository.targetSuite": "Target suite",
  "repository.theTargetRepositorySignsANewSnapshotWith":
    "The target repository signs a new snapshot with its own signer and preserves existing members.",
  "repository.hideAdvancedManualInput": "Hide advanced manual input",
  "repository.advancedManualInput": "Advanced manual input",
  "repository.artifactCoordinate": "Artifact coordinate",
  "repository.immutableVersionIdentityUsedByLifecycleOperations":
    "Immutable version identity used by {format} lifecycle operations",
  "repository.digest2": "Digest",
  "repository.evaluateAdmission": "Evaluate admission",
  "repository.promote": "Promote",
  "repository.replicate": "Replicate",
  "repository.targetSuite2": "Target suite",
  "repository.targetRepository": "Target repository",
  "repository.created": "Created",
  "repository.completed": "Completed",
  "repository.progress": "Progress",
  "repository.cancelReplicationPlan": "Cancel replication plan?",
  "repository.workersWillNotRetryAfterCancellationBytesAlready":
    "Workers will not retry after cancellation. Bytes already copied are not deleted automatically.",
  "repository.cancelPlan": "Cancel plan",
  "repository.back": "Back",
  "repository.object": "Object",
  "repository.attempts": "Attempts",
  "repository.replicationPlanCanceledWorkersWillNotRetryIt":
    "Replication plan canceled. Workers will not retry it.",
  "repository.replicationPlans": "Replication plans ({count})",
  "repository.noReplicationPlans": "No replication plans",
  "repository.replicationProgress": "Replication progress",
  "repository.status2": "Status: ",
  "repository.target": "Target: ",
  "repository.created2": "Created: ",
  "repository.noCheckpoints": "No checkpoints",
  "repository.requeuedIntelligenceSyncJobS":
    "Requeued {count} intelligence sync job(s)",
  "repository.inspectedArtifactsQueuedRetried":
    "Inspected {inspected} artifacts, queued {enqueued}, retried {retried}",
  "repository.reconcileScans": "Reconcile scans",
  "repository.lifecycleJobs": "Lifecycle jobs",
  "repository.noLifecycleJobs": "No lifecycle jobs",
  "repository.retentionCleanupPromotionAndReplicationTasksAppearHere":
    "Retention cleanup, promotion, and replication tasks appear here.",
  "repository.kind": "Kind",
  "repository.error": "Error",
  "repository.reconciliationFillsPublicationScanGapsAndRetriesFailed":
    "Reconciliation fills publication scan gaps and retries failed jobs.",
  "repository.intelligenceSyncJobSNeedReconciliation":
    "{length} intelligence sync job(s) need reconciliation",
  "repository.requeueFailedIntelligenceSyncs":
    "Requeue failed intelligence syncs?",
  "repository.onlyFailedOrCancelledSyncJobsAreReset":
    "Only failed or cancelled sync jobs are reset; artifact content is unchanged.",
  "repository.requeue": "Requeue",
  "repository.reconcileIntelligence": "Reconcile intelligence",
  "repository.restored": "Restored {coordinate}",
  "repository.tombstoneManagement": "Tombstone management",
  "repository.coordinate": "Coordinate",
  "repository.deleted2": "Deleted",
  "repository.restoreThisArtifact": "Restore this artifact?",
  "repository.afterRestorationTheArtifactIsAvailableAgainIn":
    "After restoration, the artifact is available again in repository browsing and protocol reads.",
  "repository.restore": "Restore",
  "repository.noTombstones": "No tombstones",
  "repository.deletedArtifactsRetainTombstoneRecordsAndCanBe":
    "Deleted artifacts retain tombstone records and can be restored here.",
  "repository.mavenReleaseRetentionDays": "Maven release retention days",
  "repository.aReleaseBecomesEligibleAfterThisManyDays":
    "A release becomes eligible after this many days. SNAPSHOT builds use the separate period below.",
  "repository.minimumVersionsPerMavenModule":
    "Minimum versions per Maven module",
  "repository.groupByGroupIdArtifactIdAndAlwaysProtectThe":
    "Group by groupId:artifactId and always protect the newest versions.",
  "repository.maximumVersionsPerMavenModule":
    "Maximum versions per Maven module",
  "repository.use0ForNoLimitOlderReleasesOr":
    "Use 0 for no limit. Older releases or snapshot builds beyond the limit become eligible.",
  "repository.onlyCleanMatchingMavenCoordinates":
    "Only clean matching Maven coordinates",
  "repository.optionalRE2CoordinateRegexLeaveEmptyToMatch":
    "Optional RE2 coordinate regex. Leave empty to match all Maven coordinates.",
  "repository.protectMavenCoordinates": "Protect Maven coordinates",
  "repository.coordinatesMatchingAnyRegexNeverBecomeEligible":
    "Coordinates matching any regex never become eligible.",
  "repository.eGComExample": "e.g. ^com\\.example:",
  "repository.eGComExamplePlatform": "e.g. ^com\\.example:platform:",
  "repository.mavenArtifactVersions": "Maven artifact versions",
  "repository.mavenArtifactVersion": "Maven artifact version",
  "repository.imageVersionRetentionDays": "Image version retention days",
  "repository.aManifestBecomesEligibleAfterThisManyDays":
    "A manifest becomes eligible after this many days.",
  "repository.minimumVersionsPerImage": "Minimum versions per image",
  "repository.groupByImageNameAndAlwaysProtectThese":
    "Group by image name and always protect these newest manifests.",
  "repository.maximumVersionsPerImage": "Maximum versions per image",
  "repository.use0ForNoLimitOlderManifestsBeyond":
    "Use 0 for no limit. Older manifests beyond the limit become eligible.",
  "repository.onlyCleanMatchingImages": "Only clean matching images",
  "repository.matchesImageNameNameDigestOrNameTag":
    "Matches image name, name@digest, or name:tag. Leave empty for all images.",
  "repository.protectImageVersions": "Protect image versions",
  "repository.useAnImageNameToProtectAllVersions":
    "Use an image name to protect all versions, or a digest/tag for an exact version.",
  "repository.eGTeamBackend": "e.g. ^team/backend(@|:)",
  "repository.eGTeamBackendStable": "e.g. ^team/backend:stable$",
  "repository.imageVersions": "image versions",
  "repository.manifestVersion": "Manifest version",
  "repository.recipeRevisionRetentionDays": "Recipe revision retention days",
  "repository.aRecipeRevisionBecomesEligibleAfterThisMany":
    "A recipe revision becomes eligible after this many days.",
  "repository.minimumVersionsPerReference": "Minimum versions per reference",
  "repository.groupByFullConanReferenceAndProtectThe":
    "Group by full Conan reference and protect the newest recipe revisions.",
  "repository.maximumVersionsPerReference": "Maximum versions per reference",
  "repository.use0ForNoLimitCleaningARecipe":
    "Use 0 for no limit. Cleaning a recipe revision also hides its binary packages.",
  "repository.onlyCleanMatchingReferences": "Only clean matching references",
  "repository.matchesAFullReferenceOrReferenceRecipeRevision":
    "Matches a full reference or reference#recipe-revision.",
  "repository.protectConanVersions": "Protect Conan versions",
  "repository.aMatchingReferenceProtectsAllRevisionsAnExact":
    "A matching reference protects all revisions; an exact coordinate protects one version.",
  "repository.eGOpenssl3": "e.g. ^openssl/3\\.",
  "repository.eGReleaseStable": "e.g. @release/stable(#|$)",
  "repository.assetInactivityRetentionDays": "Asset inactivity retention days",
  "repository.aPathAssetBecomesEligibleAfterItHas":
    "A path asset becomes eligible after it has not been updated for this many days.",
  "repository.onlyCleanMatchingPaths": "Only clean matching paths",
  "repository.optionalRE2PathRegexLeaveEmptyToMatch":
    "Optional RE2 path regex. Leave empty to match every repository asset.",
  "repository.protectPaths": "Protect paths",
  "repository.pathsMatchingAnyRegexNeverBecomeEligible":
    "Paths matching any regex never become eligible.",
  "repository.eGReleasesNightly": "e.g. ^releases/nightly/",
  "repository.eGReleasesStable": "e.g. ^releases/stable/",
  "repository.pathAssets": "path assets",
  "repository.pathAsset": "Path asset",
  "repository.npmPackageVersionRetentionDays":
    "npm package version retention days",
  "repository.aPackageVersionBecomesEligibleAfterThisMany":
    "A package version becomes eligible after this many days.",
  "repository.minimumVersionsPerNpmPackage": "Minimum versions per npm package",
  "repository.groupByPackageNameAndAlwaysProtectThese":
    "Group by package name and always protect these most recently published versions.",
  "repository.maximumVersionsPerNpmPackage": "Maximum versions per npm package",
  "repository.use0ForNoLimitEarlierPublicationsBeyond":
    "Use 0 for no limit. Earlier publications beyond the limit become eligible.",
  "repository.onlyCleanMatchingNpmPackages": "Only clean matching npm packages",
  "repository.matchesAPackageNameOrPackageVersionLeave":
    "Matches a package name or package@version. Leave empty for all npm packages.",
  "repository.protectNpmPackageVersions": "Protect npm package versions",
  "repository.matchAPackageNameToProtectEveryVersion":
    "Match a package name to protect every version, or package@version for one version.",
  "repository.eGTeam": "e.g. ^@team/",
  "repository.eGTeamPlatform": "e.g. ^@team/platform(@|$)",
  "repository.npmPackageVersions": "npm package versions",
  "repository.npmPackageVersion": "npm package version",
  "repository.pyPIProjectVersionRetentionDays":
    "PyPI project version retention days",
  "repository.aProjectVersionBecomesEligibleAfterThisMany":
    "A project version becomes eligible after this many days.",
  "repository.minimumVersionsPerPyPIProject":
    "Minimum versions per PyPI project",
  "repository.groupByNormalizedProjectNameAndAlwaysProtect":
    "Group by normalized project name and always protect these most recently published versions.",
  "repository.maximumVersionsPerPyPIProject":
    "Maximum versions per PyPI project",
  "repository.use0ForNoLimitEarlierPublicationsBeyond2":
    "Use 0 for no limit. Earlier publications beyond the limit become eligible together with every file in that version.",
  "repository.onlyCleanMatchingPyPIProjects":
    "Only clean matching PyPI projects",
  "repository.matchesAProjectNameOrProjectVersionLeave":
    "Matches a project name or project@version. Leave empty for all PyPI projects.",
  "repository.protectPyPIProjectVersions": "Protect PyPI project versions",
  "repository.matchAProjectNameToProtectEveryVersion":
    "Match a project name to protect every version, or project@version for one version.",
  "repository.eGInternal": "e.g. ^internal-",
  "repository.eGPlatformCore": "e.g. ^platform-core(@|$)",
  "repository.pyPIProjectVersions": "PyPI project versions",
  "repository.pyPIProjectVersion": "PyPI project version",
  "repository.goModuleVersionRetentionDays": "Go module version retention days",
  "repository.aModuleVersionBecomesEligibleAfterThisMany":
    "A module version becomes eligible after this many days.",
  "repository.minimumVersionsPerGoModule": "Minimum versions per Go module",
  "repository.groupByModulePathAndAlwaysProtectThese":
    "Group by module path and always protect these most recently published versions.",
  "repository.maximumVersionsPerGoModule": "Maximum versions per Go module",
  "repository.onlyCleanMatchingGoModules": "Only clean matching Go modules",
  "repository.matchesAModulePathOrModuleVersionLeave":
    "Matches a module path or module@version. Leave empty for all Go modules.",
  "repository.protectGoModuleVersions": "Protect Go module versions",
  "repository.matchAModulePathToProtectEveryVersion":
    "Match a module path to protect every version, or module@version for one version.",
  "repository.eGExampleComTeam": "e.g. ^example\\.com/team/",
  "repository.eGExampleComTeamWidget": "e.g. ^example\\.com/team/widget(@|$)",
  "repository.goModuleVersions": "Go module versions",
  "repository.goModuleVersion": "Go module version",
  "repository.artifactRetentionDays": "Artifact retention days",
  "repository.anArtifactBecomesEligibleAfterThisManyDays":
    "An artifact becomes eligible after this many days.",
  "repository.minimumVersionsPerGroup": "Minimum versions per group",
  "repository.alwaysProtectTheseNewestVersionsInEachGroup":
    "Always protect these newest versions in each group.",
  "repository.maximumVersionsPerGroup": "Maximum versions per group",
  "repository.use0ForNoLimitOlderVersionsBeyond":
    "Use 0 for no limit. Older versions beyond the limit become eligible.",
  "repository.onlyCleanMatchingArtifacts": "Only clean matching artifacts",
  "repository.optionalRE2RegexLeaveEmptyToMatchAll":
    "Optional RE2 regex. Leave empty to match all artifacts.",
  "repository.protectArtifacts": "Protect artifacts",
  "repository.artifactsMatchingAnyRegexNeverBecomeEligible":
    "Artifacts matching any regex never become eligible.",
  "repository.eGTeam2": "e.g. ^team/",
  "repository.eGStable": "e.g. /stable$",
  "repository.artifacts": "artifacts",
  "repository.artifactVersion": "Artifact version",
  "repository.snapshotBuild": "Snapshot build",
  "repository.releaseVersion": "Release version",
  "repository.cleanupUnit": "Cleanup unit",
  "repository.reason": "Reason",
  "repository.exceededVersionLimit": "Exceeded version limit",
  "repository.notUpdatedForDays": "Not updated for {ageDays} days",
  "repository.retainedForDays": "Retained for {ageDays} days",
  "repository.downloadEvidence": "Download evidence",
  "repository.last": "{downloadCount} · last {date}",
  "repository.neverDownloaded": "Never downloaded",
  "repository.dryRunResultsLoadedOfCandidatePolicyVersion":
    "Dry-run results: loaded {length} of {totalCandidates} candidate {candidateName} (policy version {policyVersion})",
  "repository.exportTheCompleteCandidateSetIndependentOfThe":
    "Export the complete candidate set, independent of the current page",
  "repository.exportCSV": "Export CSV",
  "repository.byAge": "By age",
  "repository.types": "Types: ",
  "repository.release": "Release",
  "repository.snapshot": "Snapshot",
  "repository.none": "None",
  "repository.oldestCandidate": "Oldest candidate",
  "repository.noToClean": "No {candidateName} to clean",
  "repository.loadMoreCandidates": "Load more candidates",
  "repository.theDryRunResultExpiredOrThePolicy":
    "The dry-run result expired or the policy changed. Run it again.",
  "repository.theRetentionPolicyChangedSoThisPreviewIs":
    "The retention policy changed, so this preview is no longer valid. Run it again.",
  "repository.theRetentionTaskWasSubmittedTrackItOn":
    "The retention task was submitted. Track it on the Lifecycle jobs tab.",
  "repository.failedToExportDryRunResults": "Failed to export dry-run results",
  "repository.maximumVersionsMustBe0OrGreaterThan":
    "Maximum versions must be 0 or greater than or equal to minimum versions.",
  "repository.policySaved": "Policy saved",
  "repository.rawCleanupByPathAsset": "Raw cleanup by path asset",
  "repository.rawAssetsAreNotVersionGroupedSoMinimum":
    "Raw assets are not version-grouped, so minimum and maximum versions do not apply. Age is calculated from the last update.",
  "repository.automaticCleanup": "Automatic cleanup",
  "repository.whenDisabledNoScheduledOrManualCleanupTask":
    "When disabled, no scheduled or manual cleanup task is created. Existing tombstones are unaffected.",
  "repository.days": "days",
  "repository.snapshotRetentionDays": "Snapshot retention days",
  "repository.mavenSNAPSHOTVersionsCanUseARetentionPeriod":
    "Maven SNAPSHOT versions can use a retention period separate from releases.",
  "repository.items": "items",
  "repository.downloadProtectionDays": "Download protection days",
  "repository.artifactsDownloadedWithinTheWindowSkipCleanupEven":
    "Artifacts downloaded within the window skip cleanup even when expired; 0 disables it. Based on lifecycle download usage.",
  "repository.savePolicy": "Save policy",
  "repository.dryRun": "Dry run",
  "repository.runRetentionCleanup": "Run retention cleanup?",
  "repository.thisWillCleanAllCandidateThePolicyVersion":
    "This will clean all {totalCandidates} candidate {candidateName}. The policy version is checked again before execution.",
  "repository.runCleanup": "Run cleanup",
  "repository.runCleanup2": "Run cleanup ({totalCandidates})",
  "repository.retentionPolicy": "Retention policy",
  "repository.hostedRepositoryRetention": "Hosted repository retention",
  "repository.artifact": "Artifact",
  "repository.unknownArtifact": "Unknown artifact",
  "repository.lastError": "Last error",
  "repository.recentScanJobs": "Recent scan jobs",
  "repository.reconcileHistoricalArtifacts": "Reconcile historical artifacts",
  "repository.showsScansAmongTheLatest100LifecycleJobs":
    "Shows scans among the latest 100 lifecycle jobs; this is not the complete scan history.",
  "repository.recentJobsAreVisibleToRepositoryAdministrators":
    "Recent jobs are visible to repository administrators",
  "repository.aSuccessfulSubmissionStillReturnsItsJobID":
    "A successful submission still returns its job ID above.",
  "repository.noScansInRecentJobs": "No scans in recent jobs",
  "repository.checksTheLatest100LifecycleJobsOnlyJobs":
    "Checks the latest 100 lifecycle jobs only. Jobs appear here after a manual submission or historical reconciliation.",
  "repository.scanCompleted": "Scan completed",
  "repository.scanning": "Scanning",
  "repository.scanFailed": "Scan failed",
  "repository.scanned": "Scanned",
  "repository.affected": "Affected",
  "repository.scanError": "Scan error",
  "repository.hasEvidence": "Has evidence",
  "repository.notScanned": "Not scanned",
  "repository.scanJobQueued": "Scan job queued",
  "repository.jobID": "Job ID: ",
  "repository.selectAndScanAnImmutableArtifact":
    "Select and scan an immutable artifact",
  "repository.searchAndSelectAnArtifact": "Search and select an artifact",
  "repository.selectionLocksTheCanonicalCoordinateAndFullDigest":
    "Selection locks the canonical coordinate and full digest. Up to 50 matches are shown; search historical versions and Conan revisions by keyword. Use advanced manual input only as a recovery path.",
  "repository.enterAnArtifactCoordinate": "Enter an artifact coordinate",
  "repository.artifactCoordinateIsTooLong": "Artifact coordinate is too long",
  "repository.canonicalArtifactCoordinate": "Canonical artifact coordinate",
  "repository.sHA256Digest": "SHA-256 digest",
  "repository.enterASHA256Digest": "Enter a SHA-256 digest",
  "repository.enterSha256FollowedBy64LowercaseHexadecimalCharacters":
    "Enter sha256: followed by 64 lowercase hexadecimal characters",
  "repository.advancedModeRequiresAnExactCoordinateAndDigest":
    "Advanced mode requires an exact coordinate and digest match.",
  "repository.theScanOnlyReadsTheSelectedArtifactAnd":
    "The scan only reads the selected artifact and never fetches or modifies upstream content.",
  "repository.queueScan": "Queue scan",
  "repository.checkingScanCapability": "Checking scan capability…",
  "repository.artifactScanning": "Artifact scanning",
  "repository.scanningWritesSBOMLicenseAndVulnerabilityResultsTo":
    "Scanning writes SBOM, license, and vulnerability results to artifact intelligence. Failures do not roll back uploads, and results do not automatically quarantine artifacts or block reads.",
  "repository.manualScan": "Manual scan",
  "repository.available": "Available",
  "repository.unavailable": "Unavailable",
  "repository.scanOnPublishCapability": "Scan-on-publish capability",
  "repository.noScannerIsConfiguredForThisRepository":
    "No scanner is configured for this repository",
  "repository.askAPlatformAdministratorToConfigure":
    "Ask a platform administrator to configure ",
  "repository.and": " and ",
  "repository.inTheGatewayDeploymentAndEnableScanJobs":
    " in the Gateway deployment and enable scan jobs on worker nodes. The endpoint and token are not exposed in the Console.",
  "repository.thisIdentityCannotManageArtifactScans":
    "This identity cannot manage artifact scans",
  "repository.queueingScansAndHistoricalReconciliationRequireRepositoriesIntelligence":
    "Queueing scans and historical reconciliation require repositories:intelligence.",
  "repository.aPTPublicationTrust": "APT publication trust",
  "repository.signingTrustAndCurrentSnapshot":
    "Signing trust and current snapshot",
  "repository.verifyTheSigningKeysCurrentlyTrustedByGateway":
    "Verify the signing keys currently trusted by Gateway, the rotation window, and immutable evidence from the latest atomic publication. Signer endpoints, tokens, and private-key material are never shown here.",
  "repository.publicationRootOfTrust": "Publication root of trust",
  "repository.aPTReleaseSigning": "APT Release signing",
  "repository.remoteModeRequiresPinnedPublicKeyFingerprintsTwo":
    "Remote mode requires pinned public-key fingerprints. Two fingerprints represent a controlled old/new overlap window.",
  "repository.signerMode": "Signer mode",
  "repository.trustedKeyCount": "Trusted key count",
  "repository.currentKeyRole": "Current key role",
  "repository.latestPublication": "Latest publication",
  "repository.trustedFingerprintWindow": "Trusted fingerprint window",
  "repository.theFirstFingerprintIsPrimaryAndTheOptional":
    "The first fingerprint is primary and the optional second is the next key accepted during rotation. Client key distribution remains an explicit operator action.",
  "repository.noRemoteTrustedFingerprintsConfigured":
    "No remote trusted fingerprints configured.",
  "repository.primary": "Primary",
  "repository.next": "Next",
  "repository.inUse": "In use",
  "repository.latestImmutableEvidence": "Latest immutable evidence",
  "repository.theseValuesComeFromTheCommittedVisibleSnapshot":
    "These values come from the committed visible snapshot, not the signer's current self-reported state.",
  "repository.suiteSequence": "Suite / sequence",
  "repository.publishedAt": "Published at",
  "repository.signerIdentity": "Signer identity",
  "repository.signatureAlgorithm": "Signature algorithm",
  "repository.keyFingerprint": "Key fingerprint",
  "repository.releaseDigest": "Release digest",
  "repository.thisRepositoryHasNoVisibleSnapshotYet":
    "This repository has no visible snapshot yet.",
  "repository.productionTrustPinned": "Production trust pinned",
  "repository.keyRotationOverlap": "Key rotation overlap",
  "repository.bothOldAndNewKeysAreAcceptedConfirm":
    "Both old and new keys are accepted. Confirm a new snapshot uses the next key, then remove the old key only after the client trust window closes.",
  "repository.referenceSignerOnly": "Reference signer only",
  "repository.theLocalReferenceSignerIsSuitableOnlyFor":
    "The local reference signer is suitable only for H2 validation and must not become a production root of trust.",
  "repository.trustPolicyMismatch": "Trust policy mismatch",
  "repository.theLatestVisibleSnapshotUsesAKeyOutside":
    "The latest visible snapshot uses a key outside the active trust policy, or remote trust configuration is incomplete. Repair the policy before publishing.",
  "repository.notConfigured2": "Not configured",
  "repository.noUsableSignerIsConfiguredSoNewAPT":
    "No usable signer is configured, so new APT snapshots cannot be published.",
  "repository.unknownState": "Unknown state",
  "repository.remotePinnedTrust": "Remote pinned trust",
  "repository.localReferenceSigner": "Local reference signer",
  "repository.disabled3": "Disabled",
  "repository.activeTrustKey": "Active trust key",
  "repository.fixture": "Fixture",
  "repository.outsidePolicy": "Outside policy",
  "repository.pathAndChecksumSidecars": "Path and checksum sidecars",
  "repository.hiddenFromDirectoryListings": "Hidden from directory listings",
  "repository.assetsWithinTheCoordinate": "Assets within the coordinate",
  "repository.hiddenFromGeneratedMetadata": "Hidden from generated metadata",
  "repository.manifestAndDescriptorClosure": "Manifest and descriptor closure",
  "repository.hiddenFromTagsCatalogAndReferrers":
    "Hidden from tags, catalog, and referrers",
  "repository.hiddenFromPackumentsAndDistTags":
    "Hidden from packuments and dist-tags",
  "repository.hiddenFromSimpleMetadata": "Hidden from Simple metadata",
  "repository.recipeRevisionClosure": "Recipe revision closure",
  "repository.hiddenFromRevisionMetadata": "Hidden from revision metadata",
  "repository.everyModuleVersionRepresentation":
    "Every module@version representation",
  "repository.hiddenFromVListAndLatest": "Hidden from @v/list and @latest",
  "repository.artifactIdentity": "Artifact identity",
  "repository.hiddenFromDiscoveryMetadata": "Hidden from discovery metadata",
  "repository.repositorySecurityPolicySaved":
    "Repository security policy saved",
  "repository.noVulnerabilities": "No vulnerabilities",
  "repository.upToLow": "Up to low",
  "repository.upToMedium": "Up to medium",
  "repository.upToHigh": "Up to high",
  "repository.allowCritical": "Allow critical",
  "repository.noScannerConfigured": "No scanner configured",
  "repository.unavailableForThisFormat": "Unavailable for this format",
  "repository.promotionGuardrail": "Promotion guardrail",
  "repository.promotionAdmission": "Promotion admission",
  "repository.evaluatesSignaturesSBOMsProvenanceAndVulnerabilityIntelligenceBefore":
    "Evaluates signatures, SBOMs, provenance, and vulnerability intelligence before a promotion job is created. Ordinary reads are unchanged.",
  "repository.enforced": "Enforced",
  "repository.notEnforced": "Not enforced",
  "repository.unsavedChanges": "Unsaved changes",
  "repository.enforceAdmissionChecks": "Enforce admission checks",
  "repository.securityIntelligence": "Security intelligence",
  "repository.controlsWhetherNewlyPublishedArtifactsAutomaticallyProduceIntelligence":
    "Controls whether newly published artifacts automatically produce intelligence used by admission.",
  "repository.scanAfterPublication": "Scan after publication",
  "repository.appliesOnlyToArtifactsPublishedAfterThisPolicy":
    "Applies only to artifacts published after this policy is saved. Scans run asynchronously and results appear in artifact details.",
  "repository.evidenceRequirements": "Evidence requirements",
  "repository.selectedRequirementsRunBeforeEveryPromotion":
    "Selected requirements run before every promotion.",
  "repository.requirementsMayBeConfiguredInAdvanceAndTake":
    "Requirements may be configured in advance and take effect only after admission is enabled.",
  "repository.requireASignature": "Require a signature",
  "repository.rejectArtifactsWithoutSignatureEvidence":
    "Reject artifacts without signature evidence.",
  "repository.requireAVerifiedSignature": "Require a verified signature",
  "repository.atLeastOneSignatureMustBeVerifiedUseful":
    "At least one signature must be verified; useful for protected release repositories.",
  "repository.requireAnSBOM": "Require an SBOM",
  "repository.rejectArtifactsWithoutASoftwareBillOfMaterials":
    "Reject artifacts without a software bill of materials.",
  "repository.requireProvenance": "Require provenance",
  "repository.requireBuildSourceBuilderAndCommitProvenance":
    "Require build source, builder, and commit provenance.",
  "repository.requireVulnerabilityScanning": "Require vulnerability scanning",
  "repository.doNotAdmitArtifactsThatHaveNotBeen":
    "Do not admit artifacts that have not been scanned.",
  "repository.blockOnScanErrors": "Block on scan errors",
  "repository.blockWhenAScannerReportsAnErrorDisable":
    "Block when a scanner reports an error; disable for a fail-open fallback.",
  "repository.riskThresholds": "Risk thresholds",
  "repository.furtherRestrictTheVulnerabilitySeverityAndLicenseSet":
    "Further restrict the vulnerability severity and license set allowed through admission.",
  "repository.maximumAllowedVulnerabilitySeverity":
    "Maximum allowed vulnerability severity",
  "repository.affectedResultsAboveThisLevelAreRejectedUnknown":
    "Affected results above this level are rejected. Unknown findings are always treated as highest risk.",
  "repository.allowedSPDXLicenses": "Allowed SPDX licenses",
  "repository.leaveEmptyForNoRestrictionWhenSetEvery":
    "Leave empty for no restriction. When set, every license must be in this SPDX ID list.",
  "repository.eGApache20MIT": "e.g. Apache-2.0, MIT",
  "repository.currentVersion": "Current version {version}",
  "repository.quarantineReadPolicySaved": "Quarantine read policy saved",
  "repository.readGuardrail": "Read guardrail",
  "repository.quarantinedArtifactReads": "Quarantined artifact reads",
  "repository.controlsWhetherClientsMayContinueReadingQuarantinedArtifacts":
    "Controls whether clients may continue reading quarantined artifacts through repository protocols. Reads resume immediately after release.",
  "repository.readsBlocked": "Reads blocked",
  "repository.readsAllowed": "Reads allowed",
  "repository.unavailable2": "Unavailable",
  "repository.blockQuarantinedArtifactReads":
    "Block quarantined artifact reads",
  "repository.affectedRequests": "Affected requests",
  "repository.currentFormatBoundary": "Current format boundary",
  "repository.discoveryMetadata": "Discovery metadata",
  "repository.whenUsedByAGroup": "When used by a Group",
  "repository.noLowerPriorityFallback": "No lower-priority fallback",
  "repository.readsRemainAllowedByDefaultForUpgradeCompatibility":
    "Reads remain allowed by default for upgrade compatibility. Review existing quarantine records before enabling enforcement.",
  "repository.readPolicyVersion": "Read policy version {version}",
  "repository.saveReadPolicy": "Save read policy",
  "repository.aPTSigningState": "APT signing state",
  "repository.securityAdmissionPolicy": "Security admission policy",
  "repository.repositoryGuardrails": "Repository guardrails",
  "repository.admissionAndQuarantinedReads": "Admission and quarantined reads",
  "repository.twoIndependentGuardrailsLiveHereTheReadPolicy":
    "Two independent guardrails live here: the read policy decides whether quarantined artifacts can be downloaded, while admission decides whether artifacts may enter this repository.",
  "repository.exportResponseIsEmpty": "Export response is empty",
  "repository.restoreResponseIsEmpty": "Restore response is empty",
  "repository.restoredSnapshotFromTheArchive":
    "Restored {suite} snapshot #{sequence} from the archive",
  "repository.exportAPortableDisasterRecoveryArchiveOrRestore":
    "Export a portable disaster-recovery archive, or restore a historical snapshot of this suite after a disaster using the receipt stored independently. An archive carries public signatures and package bytes only, never private-key material.",
  "repository.archiveDownloadStarted": "Archive download started",
  "repository.fileStoreItSeparatelyFromTheReceiptRestore":
    "File {filename}. Store it separately from the receipt {digest}; restore requires that exact receipt.",
  "repository.fileThisBrowserCannotComputeTheDigestRun":
    "File {filename}. This browser cannot compute the digest; run gateway apt-snapshot verify on the archive and store the sha256 receipt it prints.",
  "repository.exportDisasterRecoveryArchive":
    "Export disaster-recovery archive",
  "repository.state": "State",
  "repository.published2": "Published",
  "repository.action": "Action",
  "repository.exportArchive": "Export archive",
  "repository.restoreFromArchive": "Restore from archive",
  "repository.onlyAGatewayV1ArchiveBelongingToThis":
    "Only a Gateway v1 archive belonging to this repository is accepted. Restore first verifies the backup receipt, both OpenPGP signatures, and canonical indexes; replaying the same archive is idempotent and never re-signs or promotes a retired snapshot.",
  "repository.chooseArchiveFile": "Choose archive file",
  "repository.backupReceiptSha256": "Backup receipt (sha256)",
  "repository.mustBeTheSha25664LowercaseHexReceipt":
    "Must be the sha256:<64 lowercase hex> receipt stored independently at backup time. Do not derive it from this upload; the server verifies they match.",
  "repository.restoreFromThisArchive": "Restore from this archive?",
  "repository.restoreWritesPackagesAndSignedIndexesAConflicting":
    "Restore writes packages and signed indexes. A conflicting snapshot or coordinate in the target suite is rejected.",
  "repository.restoreArchive": "Restore archive",
  "repository.currentSignedSnapshot": "Current signed snapshot",
  "repository.currentSnapshot": "Current snapshot #{sequence}",
  "repository.visible": "Visible",
  "repository.snapshotID": "Snapshot ID",
  "repository.signingFingerprint": "Signing fingerprint",
  "repository.signer": "Signer",
  "repository.noVisibleSnapshotInThisSuite":
    "No visible snapshot in this suite",
  "repository.checkTheSuiteNameOrCreateTheFirst":
    "Check the suite name, or create the first signed snapshot through the publication API.",
  "repository.prunableAfter": "Prunable after",
  "repository.pruneThisRetiredSnapshotSReferences":
    "Prune this retired snapshot's references?",
  "repository.clientsUsingThisOldIndexCanNoLonger":
    "Clients using this old index can no longer read it. Other references still protect shared objects.",
  "repository.pruneReferences": "Prune references",
  "repository.package": "Package",
  "repository.recoverUntil": "Recover until",
  "repository.previewRecovery": "Preview recovery",
  "repository.canonicalPathSHA256": "Canonical path / SHA-256",
  "repository.selectPackagesToPreviewRemovalFromTheCurrent":
    "Select packages to preview removal from the current suite.",
  "repository.previewDeletion": "Preview deletion",
  "repository.retentionPreview": "Retention preview",
  "repository.retainByUploadTime": "Retain by upload time",
  "repository.groupedByComponentPackageAndArchitectureOnlyUploads":
    "Grouped by component, package and architecture. Only uploads beyond both limits qualify; this is not Debian version ordering.",
  "repository.keepLatestUploads": "Keep latest uploads",
  "repository.olderThanDays": "Older than days",
  "repository.previewRetention": "Preview retention",
  "repository.reviewSnapshotChange": "Review snapshot change",
  "repository.applyAndSignNewSnapshot": "Apply and sign new snapshot",
  "repository.basedOnSnapshot": "Based on snapshot",
  "repository.removeRestorePackagesRemain":
    "Remove {length}, restore {length2}; {remainingPackages} packages remain",
  "repository.deletedPackagesCanBeRecoveredForDaysSubmission":
    "Deleted packages can be recovered for {recoveryDays} days. Submission creates new indices and signatures; a changed current snapshot requires a fresh preview.",
  "repository.noMatchingChangesThereIsNothingToApply":
    "No matching changes; there is nothing to apply.",
  "repository.snapshotResponseIsEmpty": "Snapshot response is empty",
  "repository.previewResponseIsEmpty": "Preview response is empty",
  "repository.theSnapshotChangedRefreshAndPreviewAgain":
    "The snapshot changed. Refresh and preview again.",
  "repository.theSnapshotOrCandidatesChangedOrGovernanceBlocked":
    "The snapshot or candidates changed, or governance blocked a member. Refresh and preview again; no automatic retry was made.",
  "repository.noApplyResultWasReceivedRetryTheSame":
    "No apply result was received. Retry the same request.",
  "repository.publishedSnapshot": "Published {suite} snapshot #{sequence}",
  "repository.snapshotReferencesPrunedUnreferencedObjectsAreReclaimedAsynchronously":
    "Snapshot references pruned; unreferenced objects are reclaimed asynchronously.",
  "repository.refreshSnapshots": "Refresh snapshots",
  "repository.packages": "Packages ({length})",
  "repository.recovery": "Recovery ({length})",
  "repository.snapshots": "Snapshots ({length})",
  "repository.disasterRecovery": "Disaster recovery",
  "repository.promoteReplicate": "Promote / replicate",
  "repository.repositoryAdministratorAccessRequired":
    "Repository administrator access required",
  "repository.aPTSignedSnapshotsOperatorPreview":
    "APT signed snapshots · Operator preview",
  "repository.membershipChangesRebuildAndSignACompleteSnapshot":
    "Membership changes rebuild and sign a complete snapshot. Applying requires a configured signer; production key custody has not been accepted.",
  "repository.suite": "Suite",
  "repository.enterTheExactSuiteForExampleStableOr":
    "Enter the exact suite, for example stable or bookworm.",
  "repository.view": "View",
  "repository.repositoryDetails": "Repository details",
  "repository.repositoryTasks": "Repository tasks",
  "repository.tabArtifacts": "Artifacts",
  "repository.tabGovernance": "Governance",
  "repository.tabDistribution": "Distribution",
  "repository.tabSettings": "Settings",
  "repository.tabBrowse": "Browse",
  "repository.tabUsage": "Usage",
  "repository.tabPublish": "Publish",
  "repository.tabAccessGrants": "Access grants",
  "repository.tabRetention": "Retention",
  "repository.tabScanning": "Scanning",
  "repository.tabSecurityAdmission": "Security admission",
  "repository.tabTombstones": "Tombstones",
  "repository.tabPromoteReplicate": "Promote / replicate",
  "repository.tabSignedSnapshots": "Signed snapshots",
  "repository.tabGeneral": "General",
  "repository.tabCapacity": "Capacity",
  "repository.tabLifecycleJobs": "Lifecycle jobs",
  "repository.pushArtifactsWithTheMatchingClient":
    "Push artifacts with the {format} client to display them here.",
  "repository.allowedHostsRequired": "Allowed hosts (required)",
  "repository.allowedHostsOptional": "Allowed hosts (optional)",
  "repository.moreActionsFor": "More actions for {name}",
  "repository.copyRepositoryId": "Copy repository ID",
  "repository.deleteRepositoryAction": "Delete repository",
};
