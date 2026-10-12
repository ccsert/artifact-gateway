import type { PublicBrowseMessageKey } from "./public-browse.zh-CN";

export const publicBrowseEnUS: Record<PublicBrowseMessageKey, string> = {
  "public.browseTitle": "Public Artifact Browser",
  "public.catalogTitle": "Public artifacts",
  "public.description":
    "Only repositories with anonymous reads enabled are shown. Writes and management still require sign-in.",
  "public.managementLogin": "Management sign in",
  "public.managementConsole": "Open management console",
  "publicBrowse.loadingPublicRepositories": "Loading public repositories…",
  "publicBrowse.globalAnonymousReadsAreDisabled":
    "Global anonymous reads are disabled",
  "publicBrowse.anonymousDisabledHint":
    "An administrator must enable global anonymous reads before repository settings take effect.",
  "publicBrowse.openAccessControl": "Open access control",
  "publicBrowse.publicRepositoryNotFoundOrVisible":
    "Public repository not found or visible",
  "publicBrowse.repositoryMissingHint":
    "Return to the catalog and choose a repository with anonymous reads enabled.",
  "publicBrowse.backToCatalog": "Back to catalog",
  "publicBrowse.publicReadOnly": "Public read-only",
  "publicBrowse.catalogHeading":
    "Discover and consume trusted public artifacts",
  "publicBrowse.catalogDescription":
    "Search images, dependencies, and packages from repositories explicitly published by your team. Reading needs no sign-in; publishing, grants, and repository administration always require authentication.",
  "publicBrowse.searchRepositoryNameOrFormat":
    "Search repository name or format",
  "publicBrowse.publicSources": "public sources",
  "publicBrowse.artifactFormats": "artifact formats",
  "publicBrowse.managementActionsRequireSignIn":
    "Management actions require sign-in",
  "publicBrowse.catalogAccessHint":
    "The public catalog never grants write or admin access",
  "publicBrowse.noPublicRepositories": "No public repositories",
  "publicBrowse.emptyCatalogHint":
    "An administrator must enable global anonymous access and allow reads on a repository. Public sources and formats will appear here as soon as they are available.",
  "publicBrowse.chooseAnArtifactSource": "Choose an artifact source",
  "publicBrowse.publicSourcesArtifactFormats":
    "{sourceCount} public sources · {formatCount} artifact formats",
  "publicBrowse.filterByArtifactFormat": "Filter by artifact format",
  "publicBrowse.all": "All",
  "publicBrowse.noMatchingPublicRepositories":
    "No matching public repositories",
  "publicBrowse.catalogFilterHint":
    "Adjust the search term or format filter and try again.",
  "publicBrowse.clearFilters": "Clear filters",
  "publicBrowse.proxyDescription":
    "Caches and accelerates artifacts from an administrator-trusted upstream.",
  "publicBrowse.groupDescription":
    "Aggregates multiple public repositories behind one stable endpoint.",
  "publicBrowse.hostedDescription":
    "Public artifacts published and maintained directly by the team.",
  "publicBrowse.noSignInToRead": "No sign-in to read",
  "publicBrowse.openRepository": "Open repository",
  "publicBrowse.publicRepositories": "Public repositories",
  "publicBrowse.unknownRepository": "Unknown repository",
  "publicBrowse.coordinatePathOrNamePrefixOptional":
    "Coordinate, path, or name prefix (optional)",
  "publicBrowse.coordinatePathOrNameKeywordOptional":
    "Coordinate, package, or path keyword (optional)",
  "publicBrowse.search": "Search",
  "publicBrowse.loadingPublicArtifacts": "Loading public artifacts…",
  "publicBrowse.noMatchingPublicArtifacts": "No matching public artifacts",
  "publicBrowse.emptyArtifactsHint":
    "Confirm anonymous reads are enabled or adjust the query above.",
  "publicBrowse.found": "Found",
  "publicBrowse.artifacts": "artifacts",
  "publicBrowse.versions": " ({versionCount} versions)",
  "publicBrowse.anonymousReadOnly": "Anonymous read-only",
  "publicBrowse.image": "Image",
  "publicBrowse.aptPath": "APT path",
  "publicBrowse.artifactCoordinate": "Artifact coordinate",
  "publicBrowse.loadedVersions": "Loaded versions",
  "publicBrowse.loading": "Loading…",
  "publicBrowse.loadWhenExpanded": "Load when expanded",
  "publicBrowse.versionCount": "{versionCount}{more}",
  "publicBrowse.selectedVersion": "Selected version",
  "publicBrowse.imageDigest": "Image digest",
  "publicBrowse.digest": "Digest",
  "publicBrowse.size": "Size",
  "publicBrowse.contentType": "Content type",
  "publicBrowse.firstCached": "First cached",
  "publicBrowse.created": "Created",
  "publicBrowse.actions": "Actions",
  "publicBrowse.collapse": "Collapse",
  "publicBrowse.selectVersion": "Select version",
  "publicBrowse.usage": "Usage",
  "publicBrowse.openVersion": "Open version",
  "publicBrowse.open": "Open",
  "publicBrowse.copied": "Copied",
  "publicBrowse.copyCoordinate": "Copy coordinate",
  "publicBrowse.copy": "Copy {coordinate}",
  "publicBrowse.repository": "Repository",
  "publicBrowse.published": "Published",
  "publicBrowse.publisher": "Publisher",
  "publicBrowse.notRecorded": "Not recorded",
  "publicBrowse.selectImageVersion": "Select image version",
  "publicBrowse.loaded": "Loaded",
  "publicBrowse.retry": "Retry",
  "publicBrowse.loadingVersions": "Loading versions…",
  "publicBrowse.noMatchingVersions": "No matching versions",
  "publicBrowse.searchImageVersions": "Search image versions",
  "publicBrowse.loadingVersionPage": "Loading…",
  "publicBrowse.loadMoreVersions": "Load {pageSize} more versions",
  "publicBrowse.versionPageHint":
    "Up to {pageSize} versions are loaded at a time; select one to view details and usage.",
  "publicBrowse.openVersionPage": "Open version",
  "publicBrowse.linkCopied": "Link copied",
  "publicBrowse.copyLink": "Copy link",
  "publicBrowse.version": "Version",
  "publicBrowse.checksum": "Digest",
  "publicBrowse.imageSize": "Image size",
  "publicBrowse.selectLoadedVersionHint":
    "Once versions load, search and select one on the left.",
  "publicBrowse.forRepository": "For repository",
  "publicBrowse.usageAccessHint":
    "Anonymous browsing needs no token. Publishing, private repositories, and management still require sign-in.",
  "publicBrowse.registerAnOciRegistry": "Register an OCI registry",
  "publicBrowse.registerAMavenRepository": "Register a Maven repository",
  "publicBrowse.registerAConanRemote": "Register a Conan remote",
  "publicBrowse.registerAnNpmRegistry": "Register an npm registry",
  "publicBrowse.registerAPypiRepository": "Register a PyPI repository",
  "publicBrowse.registerAGoModuleProxy": "Register a Go module proxy",
  "publicBrowse.registerACargoRegistry": "Register a Cargo registry",
  "publicBrowse.registerAnAptSource": "Register an APT source",
  "publicBrowse.registerARawSource": "Register a Raw source",
};
