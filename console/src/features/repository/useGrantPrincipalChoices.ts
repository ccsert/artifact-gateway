import { useMemo } from "react";
import { useAuth } from "../../lib/auth";
import { platformCapabilities } from "../../lib/authorization";
import { usePreferences } from "../../lib/preferences";
import {
  useAuthorizationRoles,
  usePrincipalDirectory,
} from "../access-control/accessQueries";
import { principalOptions } from "../access-control/grantDraft";

/**
 * Who a repository grant can name, and the custom roles it can use. Only a
 * platform administrator can list them; a repository administrator composes
 * grants from a typed identity instead, so nothing is requested for them.
 */
export function useGrantPrincipalChoices() {
  const { t, text } = usePreferences();
  const { identity } = useAuth();
  const canListPrincipals = platformCapabilities(identity).platformAdmin;
  const directory = usePrincipalDirectory({ enabled: canListPrincipals });
  const rolesQuery = useAuthorizationRoles({ enabled: canListPrincipals });
  const directoryUsers = directory.users.data?.items;
  const directoryApiKeys = directory.apiKeys.data?.items;
  const directoryServiceAccounts = directory.serviceAccounts.data?.items;
  const principalChoices = useMemo(
    () =>
      principalOptions(
        directoryUsers ?? [],
        directoryApiKeys ?? [],
        directoryServiceAccounts ?? [],
        text,
      ),
    [directoryApiKeys, directoryServiceAccounts, directoryUsers, text],
  );
  const authorizationRoles = rolesQuery.data ?? [];
  const principalChoicesError =
    directory.users.error ||
    directory.apiKeys.error ||
    directory.serviceAccounts.error ||
    rolesQuery.error
      ? new Error(
          t("repositoryGrants.couldNotLoadUsersApiKeysServiceAccountsOr"),
        )
      : null;

  return { principalChoices, authorizationRoles, principalChoicesError };
}
