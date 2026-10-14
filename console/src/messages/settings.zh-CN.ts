export const settingsZhCN = {
  "settings.requiredWhenOIDCIsEnabled": "启用 OIDC 时必填",
  "settings.clientIDAndRedirectURLMustBeConfigured":
    "Client ID 与回调地址必须同时配置",
  "settings.clientIDIsRequiredWhenAClientSecret":
    "配置 Client Secret 时必须填写 Client ID",
  "settings.providerConnection": "提供方连接",
  "settings.forExampleAKeycloakRealmOrGitLabOIDC":
    "例如 Keycloak Realm 或 GitLab 的 OIDC Issuer。",
  "settings.aPIAudience": "API Audience",
  "settings.redirectURL": "回调地址",
  "settings.leaveEmptyToUseProviderDiscovery":
    "留空时使用提供方 discovery 文档。",
  "settings.discoveredAutomatically": "自动发现",
  "settings.roleMapping": "角色映射",
  "settings.supportsCommonKeycloakRealmClientRolesTopLevel":
    "支持 Keycloak Realm/Client Roles、顶层 roles、groups 等常见声明；匹配 admin 映射即成为管理员，匹配 member 映射则通过审批但不附带仓库权限（仓库访问始终来自按仓库授权），两者同时匹配时 admin 优先。",
  "settings.administratorSubjectAllowlist": "管理员 Subject 白名单",
  "settings.useOnlyForAccountsThatRequireAdministratorAccess":
    "仅用于必须按稳定 sub 直接授予管理员权限的账号。",
  "settings.unlinkedIdentities": "未绑定身份",
  "settings.whenDisabledOnlyAdministratorLinkedIdentitiesCanEnter":
    "关闭时只有管理员手动绑定的身份可以进入本地账户；JIT 会在首次登录时创建账户。",
  "settings.keepExternalPrincipal": "拒绝并保持外部身份",
  "settings.provisionOnFirstSignIn": "首次登录自动创建",
  "settings.jITDefaultRole": "JIT 默认角色",
  "settings.usedOnlyWhenNoExternalRoleMappingMatches":
    "仅当声明没有匹配角色映射时使用；选择 none 后新用户会进入待授权状态，选择 member 则不附带任何仓库权限、需按仓库授权。",
  "settings.noneAwaitingApproval": "none · 待授权",
  "settings.memberNoRepositoryAccess": "member · 无仓库权限",
  "settings.verifiedEmailLinking": "已验证邮箱自动关联",
  "settings.onlyEmailVerifiedTrueClaimsAreUsedMultiple":
    "仅使用 email_verified=true 的声明；多个匹配账户会拒绝登录。",
  "settings.on": "允许",
  "settings.off": "关闭",
  "settings.enterpriseIdentityProvider": "企业身份提供方",
  "settings.usesAuthorizationCodePKCESessionsRetainOnlyThe":
    "使用 Authorization Code + PKCE；会话仅保存已验证的受限身份。",
  "settings.on2": "启用",
  "settings.off2": "停用",
  "settings.clientCredentials": "客户端凭据",
  "settings.theCurrentSecretWillBeRemovedOnSave":
    "保存后将移除当前密钥。输入新值可替换。",
  "settings.configuredLeaveEmptyToRetainTheCurrentSecret":
    "已配置。留空会保留现有密钥。",
  "settings.publicPKCEClientsMayOmitASecret":
    "公共 PKCE 客户端可以不配置密钥。",
  "settings.keepExistingSecret": "保持现有密钥",
  "settings.optional": "可选",
  "settings.undoRemoval": "撤销移除",
  "settings.removeClientSecret": "移除客户端密钥？",
  "settings.theSecretIsNotRemovedUntilSettingsAre": "保存配置前不会实际删除。",
  "settings.markForRemoval": "标记移除",
  "settings.cancel": "取消",
  "settings.removeSecret": "移除密钥",
  "settings.pendingSave": "等待保存",
  "settings.encryptedAtRest": "已加密存储",
  "settings.effectiveContext": "生效上下文",
  "settings.source": "来源",
  "settings.version": "版本",
  "settings.otherNodes": "其他节点",
  "settings.convergeWithin5s": "最长 5 秒收敛",
  "settings.existingSessions": "现有会话",
  "settings.remainValidUntilLogoutOrExpiry": "退出或到期前继续有效",
  "settings.currentCallback": "当前回调：",
  "settings.testSavedSettings": "测试已保存配置",
  "settings.saveAndApply": "保存并应用",
  "settings.settingsSynchronized": "配置已同步",
  "settings.failedToLoadAuthenticationSettings": "加载身份认证配置失败",
  "settings.failedToSaveAuthenticationSettings": "保存身份认证配置失败",
  "settings.settingsSavedAndActiveOnThisNode": "配置已保存并在当前节点生效",
  "settings.identityProviderConnectionFailed": "身份提供方连接失败",
  "settings.authentication": "身份认证",
  "settings.runtimeSettings": "运行时配置",
  "settings.environmentBootstrap": "环境变量引导",
  "settings.configureAnExternalOIDCProviderBrowserSignIn":
    "配置外部 OIDC 身份提供方、浏览器登录与角色映射",
  "settings.runtimeStatus": "运行状态",
  "settings.enabled": "已启用",
  "settings.disabled": "未启用",
  "settings.configurationSource": "配置来源",
  "settings.version2": "版本 {version}",
  "settings.versionUnknown": "版本未知",
  "settings.clientSecret": "客户端密钥",
  "settings.configured": "已配置",
  "settings.notConfigured": "未配置",
  "settings.neverReturnedByTheAPI": "密钥不会从 API 回读",
  "settings.lastUpdated": "最近更新",
  "settings.notPersisted": "尚未持久化",
  "settings.databaseSettingsAreShared": "数据库配置跨节点共享",
  "settings.settingsCurrentlyComeFromTheEnvironmentTheFirst":
    "当前读取环境变量；首次保存会创建数据库运行时配置。",
  "settings.connectionSucceededInMs": "连接成功，发现耗时 {latencyMs} ms",
  "settings.brandPreview": "品牌预览",
  "settings.livePreview": "实时预览",
  "settings.untitledSite": "未命名站点",
  "settings.thisPreviewShowsTheIdentityLockupOnlyOther":
    "预览仅展示身份组合；保存前不会影响其他用户。",
  "settings.dark": "深色",
  "settings.light": "浅色",
  "settings.uploaded": "已上传",
  "settings.directory": "目录",
  "settings.builtIn": "内置",
  "settings.useAsDefault": "设为默认",
  "settings.deleteThisUploadedTheme": "删除这个上传主题？",
  "settings.thisCannotBeUndoneUploadThePackageAgain":
    "删除后无法恢复；若仍需使用，需要重新上传主题包。",
  "settings.delete": "删除",
  "settings.disableTheThemeAndSaveSiteSettingsFirst":
    "请先取消启用并保存站点设置",
  "settings.replaceUploadedTheme": "替换上传主题",
  "settings.installThemePackage": "安装主题包",
  "settings.replaceTheme": "确认替换",
  "settings.iDUnavailable": "ID 不可用",
  "settings.installAndEnable": "安装并启用",
  "settings.themePackageFailed": "主题包处理失败",
  "settings.validationPassedReadyToInstall": "校验通过，可以安装",
  "settings.thisIDBelongsToAnUploadedThemeA":
    "该 ID 已由上传主题使用，将创建新版本",
  "settings.thisIDBelongsToABuiltInOr":
    "该 ID 属于内置或目录主题，不能通过上传覆盖",
  "settings.name": "名称",
  "settings.mode": "模式",
  "settings.file": "文件",
  "settings.theServerStrictlyParsesThePackageAgainstV1":
    "主题包将在服务端按严格 v1 规范再次解析；安装新主题后，还需要保存站点设置才会向用户开放。",
  "settings.theThemePackageMustBeAJSONFile":
    "主题包必须是 1 字节到 256 KiB 的 JSON 文件",
  "settings.theThemePackageIsNotValidJSON": "主题包不是有效的 JSON 文件",
  "settings.themeInstalledAndAddedToTheEnabledSet":
    "主题已安装并加入启用列表；请保存并应用后对用户生效",
  "settings.themeInstalledThe32ThemeEnabledLimitHas":
    "主题已安装；启用主题已达到 32 个上限，请先停用其他主题",
  "settings.themePackageReplacedEnabledThemesWillUseThe":
    "主题包已替换；已启用主题将在重新载入设置时使用新版本",
  "settings.uploadedThemeDeleted": "已删除上传主题",
  "settings.siteSettingsAreLive": "站点设置已生效",
  "settings.onlyPNGJPEGOrWebPImagesAreSupported":
    "仅支持 PNG、JPEG 或 WebP 图片",
  "settings.theLogoMustBeNoLargerThan192": "Logo 文件不能超过 192 KiB",
  "settings.couldNotReadTheLogo": "读取 Logo 失败",
  "settings.siteSettings": "站点设置",
  "settings.manageTheSiteNameAndBrandIdentityUsed":
    "统一管理登录页、公开目录和控制台导航中的站点名称与品牌标识。",
  "settings.brandIdentity": "品牌信息",
  "settings.changesApplyToThisSessionImmediatelyAndAre":
    "保存后将立即同步到当前会话，并供未登录页面公开读取。",
  "settings.siteName": "站点名称",
  "settings.shownInNavigationSignInPublicBrowseAnd":
    "显示在导航、登录页、公开目录和浏览器标题中。",
  "settings.brandMark": "品牌标识",
  "settings.18CharactersUsedWhenNoLogoIs":
    "1–8 个字符；未设置 Logo 或 Logo 加载失败时作为回退。",
  "settings.siteLogo": "站点 Logo",
  "settings.uploadLogo": "上传 Logo",
  "settings.upload": "上传",
  "settings.removeLogo": "移除 Logo",
  "settings.useASameOriginPathHTTPSURLOr":
    "支持同源路径、HTTPS 地址，或上传不超过 192 KiB 的 PNG、JPEG、WebP。建议使用透明背景的方形图。",
  "settings.consoleThemes": "Console 主题",
  "settings.enableTheThemesUsersMayChooseAndSelect":
    "启用用户可以选择的主题，并指定首次访问时使用的默认主题。主题颜色统一由 Ant Design token 解析。",
  "settings.uploadThemePackage": "上传主题包",
  "settings.uploadAThemePackageV1MatchingThemesConsole":
    "上传符合 themes/console-theme.schema.json 的 Theme Package v1 即可动态安装；主题包只允许受限颜色 token，不会执行 CSS、脚本或外部资源。运维目录主题仍可通过 gateway theme 命令管理。",
  "settings.discardChanges": "撤销更改",
  "settings.logoUrlPlaceholder": "/assets/logo.webp 或 https://…",
};

export type SettingsMessageKey = keyof typeof settingsZhCN;
