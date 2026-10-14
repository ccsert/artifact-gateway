import type { AccessMessageKey } from "./access.zh-CN";

export const accessEnUS: Record<AccessMessageKey, string> = {
  "access.accessControl": "Access control",
  "access.evaluateEffectivePermissionsAndReviewAnonymousAndRepositoryAccess":
    "Evaluate effective permissions and review anonymous and repository access rules",
  "access.grants": "Grants ({grants})",
  "access.accessEvaluation": "Access evaluation",
  "access.policiesTemplates": "Policies & templates",
  "access.currentIdentity": "Current identity",
  "access.noGlobalRole": "no global role",
  "access.user": "User",
  "access.globalRole": "Global role",
  "access.none": "none",
  "access.serviceAccount": "Service account",
  "access.noGlobalRoleRepositoryGrantsDecide":
    "No global role; repository grants decide",
  "access.oidcCustomActor": "OIDC / custom actor",
  "access.enterTheCompleteAuthenticatedActorAndSelectItsGlobal":
    "Enter the complete authenticated actor and select its global role",
  "access.selectAPrincipalAndRepository": "Select a principal and repository",
  "access.enterTheCompleteActorProducedByTheAuthenticationSystem":
    "Enter the complete actor produced by the authentication system",
  "access.accessEvaluationFailed": "Access evaluation failed",
  "access.runTheGatewayDecisionChainAcrossGlobalRolesRepository":
    "Run the Gateway decision chain across global roles, repository grants, and resource prefixes.",
  "access.someIdentitySourcesAreUnavailable":
    "Some identity sources are unavailable",
  "access.serviceAccountsAreTemporarilyUnavailableYouCanStillEvaluate":
    "Service accounts are temporarily unavailable. You can still evaluate the current identity, users, API keys, and custom actors.",
  "access.retry": "Retry",
  "access.principal": "Principal",
  "access.none2": "None",
  "access.repository": "Repository",
  "access.selectRepository": "Select repository",
  "access.resourceOptional": "Resource (optional)",
  "access.blankEvaluatesRepositoryWideAccess":
    "Blank evaluates repository-wide access",
  "access.evaluate": "Evaluate",
  "access.completeActorIdentifier": "Complete actor identifier",
  "access.forExampleOidcGitlabTeamReleaseOrCiBot":
    "For example: oidc:gitlab:team/release or ci-bot",
  "access.thisMustExactlyMatchTheAuthenticatedActorChooseThe":
    "This must exactly match the authenticated actor; choose the global role produced by the OIDC mapping.",
  "access.simulated": "Simulated",
  "access.currentIdentity2": "Current identity",
  "access.entireRepository": "Entire repository",
  "access.authorizationOrderAndRoles": "Authorization order and roles",
  "access.howRulesCombine": "How rules combine",
  "access.repositoryRulesAddPermissionsTheyCannotRevokeAGlobal":
    "Repository rules add permissions; they cannot revoke a global role.",
  "access.failedToLoadRepositoryGrantVersion":
    "Failed to load repository grant version",
  "access.applyAuthorizationTemplate": "Apply authorization template",
  "access.cancel": "Cancel",
  "access.apply": "Apply",
  "access.chooseATargetRepositoryApplyingReplacesItsCurrentGrants":
    "Choose a target repository. Applying replaces its current grants with optimistic concurrency protection.",
  "access.read": "Read",
  "access.write": "Write",
  "access.artifactIntelligence": "Artifact intelligence",
  "access.admin": "Admin",
  "access.roleNameAndAtLeastOnePermissionAreRequired":
    "Role name and at least one permission are required",
  "access.failedToSaveAuthorizationRole": "Failed to save authorization role",
  "access.role": "Role",
  "access.noDescription": "No description",
  "access.permissions": "Permissions",
  "access.updated": "Updated",
  "access.actions": "Actions",
  "access.editRole": "Edit role",
  "access.deleteThisRole": "Delete this role?",
  "access.savedTemplatesAndRepositoryGrantsWillNotChange":
    "Saved templates and repository grants will not change.",
  "access.delete": "Delete",
  "access.deleteRole": "Delete role",
  "access.customAuthorizationRoles": "Custom authorization roles",
  "access.newRole": "New role",
  "access.rolesReusePermissionSetsTemplatesAndGrantsCopyA":
    "Roles reuse permission sets. Templates and grants copy a scope snapshot, so later role edits or deletion never change saved access.",
  "access.noCustomRolesYet": "No custom roles yet",
  "access.savePermissionsThatAreCommonlyGrantedTogetherAndReuse":
    "Save permissions that are commonly granted together and reuse them while editing templates.",
  "access.editAuthorizationRole": "Edit authorization role",
  "access.newAuthorizationRole": "New authorization role",
  "access.save": "Save",
  "access.roleName": "Role name",
  "access.forExampleReleaseReviewer": "For example Release reviewer",
  "access.description": "Description",
  "access.describeTheResponsibilityThisRoleServes":
    "Describe the responsibility this role serves",
  "access.permissions2": "Permissions",
  "access.selectOneOrMorePermissions": "Select one or more permissions",
  "access.teamReadOnly": "Team read-only",
  "access.forPrincipalsThatOnlyDownloadSearchAndBrowseArtifacts":
    "For principals that only download, search, and browse artifacts.",
  "access.releasePublisher": "Release publisher",
  "access.forCiPrincipalsThatPublishAndEditArtifacts":
    "For CI principals that publish and edit artifacts.",
  "access.artifactIntelligenceBot": "Artifact intelligence bot",
  "access.onlyAllowsSecurityAndArtifactIntelligenceMetadataWrites":
    "Only allows security and artifact-intelligence metadata writes.",
  "access.repositoryOwner": "Repository owner",
  "access.forPrincipalsResponsibleForRepositoryConfigurationGrantsAndLifecycle":
    "For principals responsible for repository configuration, grants, and lifecycle.",
  "access.template": "Template",
  "access.rules": "Rules",
  "access.edit": "Edit",
  "access.deleteThisTemplate": "Delete this template?",
  "access.deleteTemplate": "Delete template",
  "access.authorizationTemplates": "Authorization templates",
  "access.builtInTemplates": "Built-in templates",
  "access.newTemplate": "New template",
  "access.templatesStoreReusableRulesOnlyApplyingOneUpdatesA":
    "Templates store reusable rules only. Applying one updates a repository with optimistic version checks.",
  "access.noAuthorizationTemplatesYet": "No authorization templates yet",
  "access.templatesReuseASetOfPrincipalScopeAndResource":
    "Templates reuse a set of principal, scope, and resource-prefix rules.",
  "access.principal2": "Principal",
  "access.format": "Format",
  "access.permission": "Permission",
  "access.elevated": "Elevated",
  "access.resourcePrefix": "Resource prefix",
  "access.edit2": "Edit →",
  "access.repositoryGrants": "{filtered} repository grants",
  "access.clear": "Clear",
  "access.usernameApiKeyOrActor": "Username, API key, or actor",
  "access.repositoryName": "Repository name",
  "access.permissionLevel": "Permission level",
  "access.allPermissions": "All permissions",
  "access.readBrowsePull": "Read · browse / pull",
  "access.writePublishEdit": "Write · publish / edit",
  "access.adminGrantDelete": "Admin · grant / delete",
  "access.noRepositoryGrants": "No repository grants",
  "access.noMatchingGrants": "No matching grants",
  "access.addAPrincipalFromARepositorySAccessGrants":
    "Add a principal from a repository's Access grants tab to see it here",
  "access.tryAnotherFilter": "Try another filter",
  "access.oidcCustom": "OIDC / custom",
  "access.publicAccessBoundary": "Public access boundary",
  "access.repositoriesPublic":
    "{publicRepositoryCount} / {repositoryCount} repositories public",
  "access.disableGlobalAnonymousReads": "Disable global anonymous reads?",
  "access.enableGlobalAnonymousReads": "Enable global anonymous reads?",
  "access.allUnauthenticatedProtocolReadsWillBeRejected":
    "All unauthenticated protocol reads will be rejected.",
  "access.artifactsAllowedByRepositoryOrGroupPolicyBecomeReadable":
    "Artifacts allowed by repository or group policy become readable without authentication.",
  "access.continue": "Continue",
  "access.toggleGlobalAnonymousReads": "Toggle global anonymous reads",
  "access.loading": "Loading…",
  "access.globalGateOnReadOnly": "Global gate on · Read-only",
  "access.globalGateOffReadOnly": "Global gate off · Read-only",
  "access.readOnly": "Read-only",
  "access.anonymousReadsRequireGlobalRepositoryAndApplicableGroupOpt":
    "Anonymous reads require global, repository, and applicable group opt-in; writes, deletes, and management still require authentication.",
  "access.templateNameAndEveryGrantPrincipalAreRequired":
    "Template name and every grant principal are required",
  "access.principalAndResourcePrefixPairsMustBeUnique":
    "Principal and resource prefix pairs must be unique",
  "access.failedToSaveAuthorizationTemplate":
    "Failed to save authorization template",
  "access.editAuthorizationTemplate": "Edit authorization template",
  "access.newAuthorizationTemplate": "New authorization template",
  "access.templateName": "Template name",
  "access.descriptionOptional": "Description (optional)",
  "access.chooseAKnownUserOrApiKeyOrEnter":
    "Choose a known user or API key, or enter an OIDC / CI actor directly. Leave the resource prefix blank for the entire repository; suggestions follow the repository format.",
  "access.chooseOrEnterAnActorEGUserRelease":
    "Choose or enter an actor, e.g. user:release",
  "access.scopes": "Scopes",
  "access.customPermissions": "Custom permissions",
  "access.admin2": "Admin",
  "access.removeRule": "Remove rule",
  "access.addRule": "Add rule",
  "access.allOperationsBrowsePublishDeleteGrantsAndKeyManagement":
    "All operations: browse, publish, delete, grants, and key management",
  "access.noImplicitCapabilityRepositoryAccessComesOnlyFromPer":
    "No implicit capability: repository access comes only from per-repository grants",
  "access.awaitingApprovalNoRepositoryPathIsReachable":
    "Awaiting approval: no repository path is reachable",
  "access.identifyThePrincipal": "Identify the principal",
  "access.administratorIdentitiesPassDirectlyUsersApiKeysAndOidc":
    "Administrator identities pass directly. Users, API keys, and OIDC identities enter with their global role.",
  "access.applyTheGlobalRole": "Apply the global role",
  "access.adminAllowsAllOperationsIncludingArtifactIntelligenceWritesA":
    "Admin allows all operations, including artifact intelligence writes. A member level carries no repository capability, so per-repository grants decide; artifact intelligence is an independent least-privilege scope.",
  "access.applyRepositoryRules": "Apply repository rules",
  "access.thePrincipalPermissionAndResourcePrefixMustAllMatch":
    "The principal, permission, and resource prefix must all match. Artifact intelligence only writes security metadata; otherwise access is denied.",
  "access.legacyCompatibility": "Legacy compatibility",
  "access.unmanagedRepositoriesCanFallBackToLegacyStaticPolicy":
    "Unmanaged repositories can fall back to legacy static policy. Anonymous access is evaluated separately.",
  "access.principals": "Principals",
  "access.usersApiKeysOrOtherPrincipals":
    "Users, API keys, or other principals",
  "access.writeGrants": "Write grants",
  "access.canPublishOrEditArtifacts": "Can publish or edit artifacts",
  "access.adminGrants": "Admin grants",
  "access.canManageGrantsAndDelete": "Can manage grants and delete",
  "access.scopeIntelligence": "Artifact intelligence",
  "access.scopeAdmin": "Admin",
  "access.scopeWrite": "Write",
  "access.scopeRead": "Read",
  "access.scopeUnknown": "Not configured",
  "access.administratorIdentity": "Administrator identity",
  "access.legacyStaticPolicy": "Legacy static policy",
  "access.anonymousAccessPolicy": "Anonymous access policy",
  "access.repositoryGrant": "Repository grant",
  "access.notSpecified": "Not specified",
  "access.allowedByAdministratorIdentity": "Allowed by administrator identity",
  "access.allowedByTheGlobalAdminRole": "Allowed by the global admin role",
  "access.aMemberLevelCarriesNoImplicitCapabilityRepository":
    "A member level carries no implicit capability; repository grants decide",
  "access.principalPermissionAndResourceScopeAllMatch":
    "Principal, permission, and resource scope all match",
  "access.noRepositoryRuleMatchesThePrincipalPermissionAnd":
    "No repository rule matches the principal, permission, and resource scope",
  "access.repositoryGrantsCouldNotBeLoadedAccessIs":
    "Repository grants could not be loaded; access is denied",
  "access.matchedALegacyReadRule": "Matched a legacy read rule",
  "access.matchedALegacyWriteRule": "Matched a legacy write rule",
  "access.globalAnonymousReadsAreDisabled":
    "Global anonymous reads are disabled",
  "access.anonymousReadsAreDisabledForThisRepository":
    "Anonymous reads are disabled for this repository",
  "access.anonymousReadsAreEnabledGloballyAndForThis":
    "Anonymous reads are enabled globally and for this repository",
  "access.theRepositoryIsNotCurrentlyActive":
    "The repository is not currently active",
  "access.allowed": "Allowed",
  "access.denied": "Denied",
  "access.anonymousReads": "Anonymous reads",
  "access.artifactIdOptional": "artifactId (optional)",
  "access.namespaceTeam": "Namespace · team",
  "access.imageNameOptional": "Image name (optional)",
  "access.name": "Name",
  "access.versionOptional": "Version (optional)",
  "access.userOptional": "User (optional)",
  "access.channelOptional": "Channel (optional)",
  "access.scopeTeam": "Scope · @team",
  "access.packageOptional": "Package (optional)",
  "access.pathPrefixReleases": "Path prefix · releases/",
  "access.projectPrefixGateway": "Project prefix · gateway-",
  "access.modulePathPrefixExampleComTeam":
    "Module path prefix · example.com/team/",
  "access.repositoryPathPrefixDistsBookworm":
    "Repository path prefix · dists/bookworm/",
  "access.limitByPrefix": "Limit by prefix",
  "access.canonicalPrefix": "Canonical prefix",
  "access.readWriteAdminIntelligence": "Read + write + admin + intelligence",
  "access.readWrite": "Read + write",
  "access.noGlobalRoleRepositoryGrantsDecideAccess":
    "No global role; repository grants decide access",
  "access.mustExactlyMatchTheAuthenticatedActor":
    "Must exactly match the authenticated actor",
  "access.selectAUserAPIKeyServiceAccountOr":
    "Select a user, API key, service account, or external identity",
  "access.users": "Users",
  "access.serviceAccounts": "Service accounts",
  "access.externalIdentities": "External identities",
  "access.principalIdentifier": "Principal identifier",
  "access.completeActorForExampleOidcGitlabTeamRelease":
    "Complete actor, for example oidc:gitlab:team/release",
  "access.permission2": "Permission",
  "access.savedPermissionSnapshot": "Saved permission snapshot",
  "access.builtInPermissions": "Built-in permissions",
  "access.adminGrantDelete2": "Admin · grant / delete",
  "access.artifactIntelligenceSecurityMetadata":
    "Artifact intelligence · security metadata",
  "access.customRoles": "Custom roles",
  "access.grantedByThisRule": "Granted by this rule",
  "access.resourceScope": "Resource scope",
};
