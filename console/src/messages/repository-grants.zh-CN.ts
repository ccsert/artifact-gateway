export const repositoryGrantsZhCN = {
  "repositoryGrants.couldNotLoadUsersApiKeysServiceAccountsOr":
    "无法加载用户、API Key、服务账号或自定义角色，可继续使用自定义身份和内置权限。",
  "repositoryGrants.selectOrEnterAPrincipalForTheGrant":
    "请选择或填写授权主体。",
  "repositoryGrants.thePreviousRowCouldNotBeRemovedSoThe":
    "旧授权删除失败，新授权未保存，请重试。",
  "repositoryGrants.thePreviousRowWasRemovedButSavingTheNew":
    "旧行已移除，但新授权保存失败，请再次保存以写入新授权。",
  "repositoryGrants.grantRemoved": "授权已移除",
  "repositoryGrants.accessGrants": "访问授权",
  "repositoryGrants.principal": "授权主体",
  "repositoryGrants.grantedCapabilities": "授予能力",
  "repositoryGrants.resourceScope": "资源范围",
  "repositoryGrants.entireRepository": "整个仓库",
  "repositoryGrants.actions": "操作",
  "repositoryGrants.edit": "编辑",
  "repositoryGrants.removeThisGrant": "移除这条授权？",
  "repositoryGrants.onlyThisRowIsRemovedEveryOtherPrincipalKeeps":
    "只移除当前行，其他主体的授权不受影响。",
  "repositoryGrants.remove": "移除",
  "repositoryGrants.cancel": "取消",
  "repositoryGrants.removeThisGrantRow": "移除该行授权",
  "repositoryGrants.addGrant": "添加授权",
  "repositoryGrants.noAccessGrants": "暂无授权规则",
  "repositoryGrants.chooseAUserApiKeyOrServiceAccountVia":
    "点击「添加授权」选择用户、API Key、服务账号，或填写 OIDC subject / 自定义 actor。",
  "repositoryGrants.editGrant": "编辑授权",
  "repositoryGrants.save": "保存",
  "repositoryGrants.thisPrincipalAlreadyHoldsAGrantAtThisResource":
    "该主体在此资源范围已有授权，保存将替换原有规则。",
  "repositoryGrants.repositoryRulesAddPermissionsTheyCannotRevokeAnExisting":
    "仓库规则只会追加权限，不能撤销用户或 API Key 已有的全局角色；服务账号没有全局角色。",
  "repositoryGrants.usersApiKeysOrServiceAccountsAreTemporarilyUnavailable":
    "用户、API Key 或服务账号列表暂时不可用；仍可选择“OIDC / 自定义 actor”并填写主体标识。",
} as const;

export type RepositoryGrantsMessageKey = keyof typeof repositoryGrantsZhCN;
