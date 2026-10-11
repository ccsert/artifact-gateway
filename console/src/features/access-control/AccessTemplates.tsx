import { useMemo } from "react";
import type { ApiKey, Repository, ServiceAccount, User } from "../../client";
import { ErrorBanner } from "../../components/ui/Feedback";
import { useAuth } from "../../lib/auth";
import {
  isActiveApiKeyPrincipal,
  isActiveUserPrincipal,
} from "../../lib/accessPrincipals";
import { usePreferences } from "../../lib/preferences";
import {
  AuthorizationTemplatesPanel,
  type PrincipalOption,
} from "./AuthorizationTemplatesPanel";

/** Principal projections run inside the templates' recoverable boundary. */
export function AccessTemplates({
  users,
  apiKeys,
  serviceAccounts,
  repositories,
  directoryError,
  onRetry,
}: {
  users: User[];
  apiKeys: ApiKey[];
  serviceAccounts: ServiceAccount[];
  repositories: Repository[];
  directoryError: unknown;
  onRetry: () => void;
}) {
  const { identity } = useAuth();
  const { t } = usePreferences();
  const principalOptions = useMemo<PrincipalOption[]>(() => {
    const options: PrincipalOption[] = [];
    const seen = new Set<string>();
    const add = (value: string, label: string) => {
      const normalized = value.trim();
      if (!normalized || seen.has(normalized)) return;
      seen.add(normalized);
      options.push({ value: normalized, label });
    };
    if (identity?.actor)
      add(
        identity.actor,
        `${t("access.currentIdentity2")} · ${identity.actor}`,
      );
    users
      .filter(isActiveUserPrincipal)
      .forEach((user) =>
        add(
          `user:${user.name}`,
          `${t("access.user")} · ${user.name}${user.displayName ? ` · ${user.displayName}` : ""}`,
        ),
      );
    apiKeys
      .filter(isActiveApiKeyPrincipal)
      .forEach((key) => add(`api-key:${key.id}`, `API Key · ${key.name}`));
    serviceAccounts
      .filter((account) => account.state === "active")
      .forEach((account) =>
        add(
          `service-account:${account.id}`,
          `${t("access.serviceAccount")} · ${account.name}`,
        ),
      );
    return options;
  }, [apiKeys, identity, serviceAccounts, t, users]);
  return (
    <div className="ag-page-stack">
      {directoryError !== null && (
        <ErrorBanner error={directoryError} onRetry={onRetry} />
      )}
      <AuthorizationTemplatesPanel
        repositories={repositories}
        principalOptions={principalOptions}
      />
    </div>
  );
}
