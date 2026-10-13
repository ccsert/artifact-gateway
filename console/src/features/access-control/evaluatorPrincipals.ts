import type {
  ApiKey,
  CurrentIdentity,
  ServiceAccount,
  User,
} from "../../client";
import {
  isActiveApiKeyPrincipal,
  isActiveUserPrincipal,
} from "../../lib/accessPrincipals";
import type { usePreferences } from "../../lib/preferences";
import {
  CURRENT_PRINCIPAL,
  CUSTOM_PRINCIPAL,
  strongestRole,
  type EvaluatorPrincipal,
  type EvaluatorRole,
} from "./accessModel";

/**
 * Who the evaluator can act as: the signed-in identity, active users, API
 * keys and service accounts, and a custom authenticated actor.
 */
export function evaluatorPrincipalChoices({
  identity,
  users,
  apiKeys,
  serviceAccounts,
  customActor,
  customRole,
  t,
}: {
  identity: CurrentIdentity | null | undefined;
  users: User[];
  apiKeys: ApiKey[];
  serviceAccounts: ServiceAccount[];
  customActor: string;
  customRole: EvaluatorRole;
  t: ReturnType<typeof usePreferences>["t"];
}): EvaluatorPrincipal[] {
  const choices: EvaluatorPrincipal[] = [];
  if (identity) {
    choices.push({
      value: CURRENT_PRINCIPAL,
      actor: identity.actor,
      label: t("access.currentIdentity"),
      detail: `${identity.actor} · ${identity.role ?? t("access.noGlobalRole")}`,
      role: identity.role ?? "none",
      current: true,
    });
  }
  choices.push(
    ...users.filter(isActiveUserPrincipal).map((user) => ({
      value: `user:${user.name}`,
      actor: `user:${user.name}`,
      label: `${t("access.user")} · ${user.name}`,
      detail: `${t("access.globalRole")} ${user.role}`,
      role: user.role,
    })),
    ...apiKeys
      .filter((key) => isActiveApiKeyPrincipal(key))
      .map((key) => {
        const role = strongestRole(key.roles);
        return {
          value: `api-key:${key.id}`,
          actor: `api-key:${key.id}`,
          label: `API Key · ${key.name}`,
          detail: `${t("access.globalRole")} ${
            role === "none" ? t("access.none") : role
          }`,
          role,
        };
      }),
    ...serviceAccounts
      .filter((account) => account.state === "active")
      .map((account) => ({
        value: `service-account:${account.id}`,
        actor: `service-account:${account.id}`,
        label: `${t("access.serviceAccount")} · ${account.name}`,
        detail: t("access.noGlobalRoleRepositoryGrantsDecide"),
        role: "none" as const,
      })),
    {
      value: CUSTOM_PRINCIPAL,
      actor: customActor.trim(),
      label: t("access.oidcCustomActor"),
      detail: t("access.enterTheCompleteAuthenticatedActorAndSelectItsGlobal"),
      role: customRole,
    },
  );
  return choices;
}
