import type { RepositoryGrantsMessageKey } from "./repository-grants.zh-CN";

export const repositoryGrantsEnUS: Record<RepositoryGrantsMessageKey, string> =
  {
    "repositoryGrants.couldNotLoadUsersApiKeysServiceAccountsOr":
      "Could not load users, API keys, service accounts, or custom roles. You can still use a custom identity and built-in permissions.",
    "repositoryGrants.selectOrEnterAPrincipalForTheGrant":
      "Select or enter a principal for the grant.",
    "repositoryGrants.thePreviousRowCouldNotBeRemovedSoThe":
      "The previous row could not be removed, so the new grant was not saved. Please retry.",
    "repositoryGrants.thePreviousRowWasRemovedButSavingTheNew":
      "The previous row was removed, but saving the new grant failed. Save again to write it.",
    "repositoryGrants.grantRemoved": "Grant removed",
    "repositoryGrants.accessGrants": "Access grants",
    "repositoryGrants.principal": "Principal",
    "repositoryGrants.grantedCapabilities": "Granted capabilities",
    "repositoryGrants.resourceScope": "Resource scope",
    "repositoryGrants.entireRepository": "Entire repository",
    "repositoryGrants.actions": "Actions",
    "repositoryGrants.edit": "Edit",
    "repositoryGrants.removeThisGrant": "Remove this grant?",
    "repositoryGrants.onlyThisRowIsRemovedEveryOtherPrincipalKeeps":
      "Only this row is removed; every other principal keeps its grants.",
    "repositoryGrants.remove": "Remove",
    "repositoryGrants.cancel": "Cancel",
    "repositoryGrants.removeThisGrantRow": "Remove this grant row",
    "repositoryGrants.addGrant": "Add grant",
    "repositoryGrants.noAccessGrants": "No access grants",
    "repositoryGrants.chooseAUserApiKeyOrServiceAccountVia":
      "Choose a user, API key, or service account via Add grant, or enter an OIDC subject/custom actor.",
    "repositoryGrants.editGrant": "Edit grant",
    "repositoryGrants.save": "Save",
    "repositoryGrants.thisPrincipalAlreadyHoldsAGrantAtThisResource":
      "This principal already holds a grant at this resource scope; saving replaces it.",
    "repositoryGrants.repositoryRulesAddPermissionsTheyCannotRevokeAnExisting":
      "Repository rules add permissions; they cannot revoke an existing global user or API key role. Service accounts have no global role.",
    "repositoryGrants.usersApiKeysOrServiceAccountsAreTemporarilyUnavailable":
      "Users, API keys, or service accounts are temporarily unavailable. You can still choose OIDC/custom actor and enter its identifier.",
  };
