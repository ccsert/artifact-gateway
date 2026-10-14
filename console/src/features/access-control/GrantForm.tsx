import { Input, Select } from "antd";
import type { AuthorizationRole, Repository } from "../../client";
import { Badge } from "../../components/ui/Badge";
import { Field } from "../../components/ui/Layout";
import { usePreferences } from "../../lib/preferences";
import { ResourcePrefixEditor } from "./ResourcePrefixEditor";
import {
  BUILTIN_PERMISSION_PREFIX,
  CUSTOM_PRINCIPAL,
  CUSTOM_ROLE_PREFIX,
  SNAPSHOT_PERMISSION,
  grantedCapabilitiesLabel,
  grantLevel,
  grantTone,
  permissionSelection,
  principalEditorKind,
  scopesForLevel,
  scopesForRole,
  type DraftGrant,
  type GrantLevel,
  type PrincipalOption,
} from "./grantDraft";

/**
 * The form behind "add grant" and "edit grant": one principal, one permission
 * (built-in level or custom authorization role), one resource scope, and the
 * capabilities that combination actually grants.
 *
 * Every field carries its own visible label rather than a shared column header
 * row: the dialog holds exactly one grant, so a header would sit far from the
 * control it names and the form would not fit a phone. The parent owns the
 * draft and all state; this component only reports the next value.
 */
export type GrantFormProps = {
  grant: DraftGrant;
  /**
   * Principal choices offered by the principal select, normally built with
   * `principalOptions()` from `grantDraft`.
   */
  principalOptions: PrincipalOption[];
  /** Custom authorization roles offered by the permission select. */
  authorizationRoles: AuthorizationRole[];
  /** Repository format that drives the resource-prefix editor. */
  format: Repository["format"];
  /** Receives the complete next draft after any control changes. */
  onChange: (next: DraftGrant) => void;
  /**
   * Renders the principal as a read-only line instead of a select, for the
   * surfaces that only ever manage one fixed principal. A select whose value
   * cannot change is a control that silently ignores the administrator.
   */
  lockPrincipal?: boolean;
};

export function GrantForm({
  grant,
  principalOptions,
  authorizationRoles,
  format,
  onChange,
  lockPrincipal = false,
}: GrantFormProps) {
  const { t } = usePreferences();
  const kind = principalEditorKind(grant.principal);
  const selectedChoice = principalOptions.find(
    (choice) => choice.value === grant.principal,
  );
  const level = grantLevel(grant.scopes);
  const selectedPermission = permissionSelection(grant, authorizationRoles);

  return (
    <div className="grid gap-4 sm:grid-cols-2">
      <div className="sm:col-span-2">
        {lockPrincipal ? (
          <Field label={t("access.principal")} hint={selectedChoice?.detail}>
            <div className="min-h-8 font-mono text-sm text-fg">
              {grant.principal}
            </div>
          </Field>
        ) : (
          <Field
            label={t("access.principal")}
            hint={
              kind === "custom"
                ? t("access.mustExactlyMatchTheAuthenticatedActor")
                : selectedChoice?.detail
            }
          >
            <Select
              className="w-full"
              aria-label={t("access.principal")}
              showSearch={{ optionFilterProp: "label" }}
              value={
                kind === "custom"
                  ? CUSTOM_PRINCIPAL
                  : grant.principal || undefined
              }
              placeholder={t("access.selectAUserAPIKeyServiceAccountOr")}
              options={[
                {
                  label: t("access.users"),
                  options: principalOptions
                    .filter((choice) => choice.value.startsWith("user:"))
                    .map((choice) => ({
                      value: choice.value,
                      label: `${choice.label} · ${choice.detail}`,
                    })),
                },
                {
                  label: "API Keys",
                  options: principalOptions
                    .filter((choice) => choice.value.startsWith("api-key:"))
                    .map((choice) => ({
                      value: choice.value,
                      label: `${choice.label} · ${choice.detail}`,
                    })),
                },
                {
                  label: t("access.serviceAccounts"),
                  options: principalOptions
                    .filter((choice) =>
                      choice.value.startsWith("service-account:"),
                    )
                    .map((choice) => ({
                      value: choice.value,
                      label: `${choice.label} · ${choice.detail}`,
                    })),
                },
                {
                  label: t("access.externalIdentities"),
                  options: [
                    {
                      value: CUSTOM_PRINCIPAL,
                      label: t("access.oidcCustomActor"),
                    },
                  ],
                },
              ]}
              onChange={(value) =>
                onChange({
                  ...grant,
                  principal:
                    value === CUSTOM_PRINCIPAL
                      ? principalEditorKind(grant.principal) === "custom"
                        ? grant.principal
                        : CUSTOM_PRINCIPAL
                      : value,
                })
              }
            />
          </Field>
        )}
        {!lockPrincipal && kind === "custom" && (
          <div className="mt-3">
            <Field
              label={t("access.principalIdentifier")}
              hint={t("access.completeActorForExampleOidcGitlabTeamRelease")}
            >
              <Input
                className="font-mono"
                maxLength={512}
                value={
                  grant.principal === CUSTOM_PRINCIPAL ? "" : grant.principal
                }
                onChange={(event) =>
                  onChange({ ...grant, principal: event.target.value })
                }
              />
            </Field>
          </div>
        )}
      </div>
      <Field label={t("access.permission2")}>
        <Select
          className="w-full"
          aria-label={t("access.permission2")}
          value={selectedPermission}
          options={[
            ...(selectedPermission === SNAPSHOT_PERMISSION
              ? [
                  {
                    value: SNAPSHOT_PERMISSION,
                    label: t("access.savedPermissionSnapshot"),
                  },
                ]
              : []),
            {
              label: t("access.builtInPermissions"),
              options: [
                {
                  value: `${BUILTIN_PERMISSION_PREFIX}read`,
                  label: t("access.readBrowsePull"),
                },
                {
                  value: `${BUILTIN_PERMISSION_PREFIX}write`,
                  label: t("access.writePublishEdit"),
                },
                {
                  value: `${BUILTIN_PERMISSION_PREFIX}admin`,
                  label: t("access.adminGrantDelete2"),
                },
                {
                  value: `${BUILTIN_PERMISSION_PREFIX}intelligence`,
                  label: t("access.artifactIntelligenceSecurityMetadata"),
                },
              ],
            },
            {
              label: t("access.customRoles"),
              options: authorizationRoles.map((role) => ({
                value: `${CUSTOM_ROLE_PREFIX}${role.id}`,
                label: role.name,
              })),
            },
          ]}
          onChange={(value) => {
            if (value === SNAPSHOT_PERMISSION) return;
            const role = value.startsWith(CUSTOM_ROLE_PREFIX)
              ? authorizationRoles.find(
                  (item) => item.id === value.slice(CUSTOM_ROLE_PREFIX.length),
                )
              : undefined;
            const nextScopes = role
              ? scopesForRole(role)
              : scopesForLevel(
                  value.slice(BUILTIN_PERMISSION_PREFIX.length) as GrantLevel,
                );
            onChange({ ...grant, scopes: nextScopes, roleId: role?.id });
          }}
        />
      </Field>
      <div className="min-w-0">
        <span className="mb-1.5 block text-xs font-medium text-fg-secondary">
          {t("access.grantedByThisRule")}
        </span>
        <div className="flex min-h-8 items-center">
          <Badge tone={grantTone(level)}>
            {grantedCapabilitiesLabel(grant.scopes, t)}
          </Badge>
        </div>
      </div>
      <div className="sm:col-span-2">
        <Field label={t("access.resourceScope")} group>
          <ResourcePrefixEditor
            format={format}
            value={grant.resourcePrefix ?? ""}
            onChange={(value) => onChange({ ...grant, resourcePrefix: value })}
          />
        </Field>
      </div>
    </div>
  );
}
