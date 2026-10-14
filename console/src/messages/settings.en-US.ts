import type { SettingsMessageKey } from "./settings.zh-CN";

export const settingsEnUS: Record<SettingsMessageKey, string> = {
  "settings.requiredWhenOIDCIsEnabled": "Required when OIDC is enabled",
  "settings.clientIDAndRedirectURLMustBeConfigured":
    "Client ID and Redirect URL must be configured together",
  "settings.clientIDIsRequiredWhenAClientSecret":
    "Client ID is required when a client secret is configured",
  "settings.providerConnection": "Provider connection",
  "settings.forExampleAKeycloakRealmOrGitLabOIDC":
    "For example, a Keycloak realm or GitLab OIDC issuer.",
  "settings.aPIAudience": "API audience",
  "settings.redirectURL": "Redirect URL",
  "settings.leaveEmptyToUseProviderDiscovery":
    "Leave empty to use provider discovery.",
  "settings.discoveredAutomatically": "Discovered automatically",
  "settings.roleMapping": "Role mapping",
  "settings.supportsCommonKeycloakRealmClientRolesTopLevel":
    "Supports common Keycloak realm/client roles, top-level roles, and groups claims. A matched admin mapping makes the account an administrator; a matched member mapping approves it without repository access, which always comes from per-repository grants. Admin wins when both match.",
  "settings.administratorSubjectAllowlist": "Administrator subject allowlist",
  "settings.useOnlyForAccountsThatRequireAdministratorAccess":
    "Use only for accounts that require administrator access by stable sub claim.",
  "settings.unlinkedIdentities": "Unlinked identities",
  "settings.whenDisabledOnlyAdministratorLinkedIdentitiesCanEnter":
    "When disabled, only administrator-linked identities can enter local accounts. JIT creates an account on first sign-in.",
  "settings.keepExternalPrincipal": "Keep external principal",
  "settings.provisionOnFirstSignIn": "Provision on first sign-in",
  "settings.jITDefaultRole": "JIT default role",
  "settings.usedOnlyWhenNoExternalRoleMappingMatches":
    "Used only when no external role mapping matches; none registers new users without access, member registers them with no repository access until granted.",
  "settings.noneAwaitingApproval": "none · awaiting approval",
  "settings.memberNoRepositoryAccess": "member · no repository access",
  "settings.verifiedEmailLinking": "Verified email linking",
  "settings.onlyEmailVerifiedTrueClaimsAreUsedMultiple":
    "Only email_verified=true claims are used; multiple matching accounts reject sign-in.",
  "settings.on": "On",
  "settings.off": "Off",
  "settings.enterpriseIdentityProvider": "Enterprise identity provider",
  "settings.usesAuthorizationCodePKCESessionsRetainOnlyThe":
    "Uses Authorization Code + PKCE; sessions retain only the validated bounded identity.",
  "settings.on2": "On",
  "settings.off2": "Off",
  "settings.clientCredentials": "Client credentials",
  "settings.theCurrentSecretWillBeRemovedOnSave":
    "The current secret will be removed on save. Enter a new value to replace it.",
  "settings.configuredLeaveEmptyToRetainTheCurrentSecret":
    "Configured. Leave empty to retain the current secret.",
  "settings.publicPKCEClientsMayOmitASecret":
    "Public PKCE clients may omit a secret.",
  "settings.keepExistingSecret": "Keep existing secret",
  "settings.optional": "Optional",
  "settings.undoRemoval": "Undo removal",
  "settings.removeClientSecret": "Remove client secret?",
  "settings.theSecretIsNotRemovedUntilSettingsAre":
    "The secret is not removed until settings are saved.",
  "settings.markForRemoval": "Mark for removal",
  "settings.cancel": "Cancel",
  "settings.removeSecret": "Remove secret",
  "settings.pendingSave": "Pending save",
  "settings.encryptedAtRest": "Encrypted at rest",
  "settings.effectiveContext": "Effective context",
  "settings.source": "Source",
  "settings.version": "Version",
  "settings.otherNodes": "Other nodes",
  "settings.convergeWithin5s": "Converge within 5s",
  "settings.existingSessions": "Existing sessions",
  "settings.remainValidUntilLogoutOrExpiry":
    "Remain valid until logout or expiry",
  "settings.currentCallback": "Current callback: ",
  "settings.testSavedSettings": "Test saved settings",
  "settings.saveAndApply": "Save and apply",
  "settings.settingsSynchronized": "Settings synchronized",
  "settings.failedToLoadAuthenticationSettings":
    "Failed to load authentication settings",
  "settings.failedToSaveAuthenticationSettings":
    "Failed to save authentication settings",
  "settings.settingsSavedAndActiveOnThisNode":
    "Settings saved and active on this node",
  "settings.identityProviderConnectionFailed":
    "Identity provider connection failed",
  "settings.authentication": "Authentication",
  "settings.runtimeSettings": "Runtime settings",
  "settings.environmentBootstrap": "Environment bootstrap",
  "settings.configureAnExternalOIDCProviderBrowserSignIn":
    "Configure an external OIDC provider, browser sign-in, and role mapping",
  "settings.runtimeStatus": "Runtime status",
  "settings.enabled": "Enabled",
  "settings.disabled": "Disabled",
  "settings.configurationSource": "Configuration source",
  "settings.version2": "Version {version}",
  "settings.versionUnknown": "Version unknown",
  "settings.clientSecret": "Client secret",
  "settings.configured": "Configured",
  "settings.notConfigured": "Not configured",
  "settings.neverReturnedByTheAPI": "Never returned by the API",
  "settings.lastUpdated": "Last updated",
  "settings.notPersisted": "Not persisted",
  "settings.databaseSettingsAreShared": "Database settings are shared",
  "settings.settingsCurrentlyComeFromTheEnvironmentTheFirst":
    "Settings currently come from the environment. The first save creates runtime database settings.",
  "settings.connectionSucceededInMs": "Connection succeeded in {latencyMs} ms",
  "settings.brandPreview": "Brand preview",
  "settings.livePreview": "Live preview",
  "settings.untitledSite": "Untitled site",
  "settings.thisPreviewShowsTheIdentityLockupOnlyOther":
    "This preview shows the identity lockup only; other users are unaffected until you save.",
  "settings.dark": "Dark",
  "settings.light": "Light",
  "settings.uploaded": "Uploaded",
  "settings.directory": "Directory",
  "settings.builtIn": "Built in",
  "settings.useAsDefault": "Use as default",
  "settings.deleteThisUploadedTheme": "Delete this uploaded theme?",
  "settings.thisCannotBeUndoneUploadThePackageAgain":
    "This cannot be undone; upload the package again to restore it.",
  "settings.delete": "Delete",
  "settings.disableTheThemeAndSaveSiteSettingsFirst":
    "Disable the theme and save site settings first",
  "settings.replaceUploadedTheme": "Replace uploaded theme",
  "settings.installThemePackage": "Install theme package",
  "settings.replaceTheme": "Replace theme",
  "settings.iDUnavailable": "ID unavailable",
  "settings.installAndEnable": "Install and enable",
  "settings.themePackageFailed": "Theme package failed",
  "settings.validationPassedReadyToInstall":
    "Validation passed; ready to install",
  "settings.thisIDBelongsToAnUploadedThemeA":
    "This ID belongs to an uploaded theme; a new version will replace it",
  "settings.thisIDBelongsToABuiltInOr":
    "This ID belongs to a built-in or directory theme and cannot be overwritten",
  "settings.name": "Name",
  "settings.mode": "Mode",
  "settings.file": "File",
  "settings.theServerStrictlyParsesThePackageAgainstV1":
    "The server strictly parses the package against v1. After installing a new theme, save site settings to make it available to users.",
  "settings.theThemePackageMustBeAJSONFile":
    "The theme package must be a JSON file between 1 byte and 256 KiB",
  "settings.theThemePackageIsNotValidJSON":
    "The theme package is not valid JSON",
  "settings.themeInstalledAndAddedToTheEnabledSet":
    "Theme installed and added to the enabled set; save and apply to make it available to users",
  "settings.themeInstalledThe32ThemeEnabledLimitHas":
    "Theme installed; the 32-theme enabled limit has been reached, so disable another theme first",
  "settings.themePackageReplacedEnabledThemesWillUseThe":
    "Theme package replaced; enabled themes will use the new version when settings reload",
  "settings.uploadedThemeDeleted": "Uploaded theme deleted",
  "settings.siteSettingsAreLive": "Site settings are live",
  "settings.onlyPNGJPEGOrWebPImagesAreSupported":
    "Only PNG, JPEG, or WebP images are supported",
  "settings.theLogoMustBeNoLargerThan192":
    "The logo must be no larger than 192 KiB",
  "settings.couldNotReadTheLogo": "Could not read the logo",
  "settings.siteSettings": "Site settings",
  "settings.manageTheSiteNameAndBrandIdentityUsed":
    "Manage the site name and brand identity used by sign-in, public browse, and Console navigation.",
  "settings.brandIdentity": "Brand identity",
  "settings.changesApplyToThisSessionImmediatelyAndAre":
    "Changes apply to this session immediately and are publicly readable before sign-in.",
  "settings.siteName": "Site name",
  "settings.shownInNavigationSignInPublicBrowseAnd":
    "Shown in navigation, sign-in, public browse, and the browser title.",
  "settings.brandMark": "Brand mark",
  "settings.18CharactersUsedWhenNoLogoIs":
    "1–8 characters; used when no logo is set or the image cannot load.",
  "settings.siteLogo": "Site logo",
  "settings.uploadLogo": "Upload logo",
  "settings.upload": "Upload",
  "settings.removeLogo": "Remove logo",
  "settings.useASameOriginPathHTTPSURLOr":
    "Use a same-origin path, HTTPS URL, or upload a PNG, JPEG, or WebP up to 192 KiB. A square image with a transparent background works best.",
  "settings.consoleThemes": "Console themes",
  "settings.enableTheThemesUsersMayChooseAndSelect":
    "Enable the themes users may choose and select the default for first-time visitors. All theme colors are resolved from Ant Design tokens.",
  "settings.uploadThemePackage": "Upload theme package",
  "settings.uploadAThemePackageV1MatchingThemesConsole":
    "Upload a Theme Package v1 matching themes/console-theme.schema.json to install it dynamically. Packages contain bounded color tokens only—no CSS, scripts, or external assets. Operator directory themes remain available through gateway theme commands.",
  "settings.discardChanges": "Discard changes",
  "settings.logoUrlPlaceholder": "/assets/logo.webp or https://…",
};
