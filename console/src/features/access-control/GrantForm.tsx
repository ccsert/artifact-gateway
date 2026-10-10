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
  const { text } = usePreferences();
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
          <Field
            label={text("授权主体", "Principal")}
            hint={selectedChoice?.detail}
          >
            <div className="min-h-8 font-mono text-sm text-fg">
              {grant.principal}
            </div>
          </Field>
        ) : (
          <Field
            label={text("授权主体", "Principal")}
            hint={
              kind === "custom"
                ? text(
                    "必须与认证完成后产生的 actor 完全一致",
                    "Must exactly match the authenticated actor",
                  )
                : selectedChoice?.detail
            }
          >
            <Select
              className="w-full"
              aria-label={text("授权主体", "Principal")}
              showSearch={{ optionFilterProp: "label" }}
              value={
                kind === "custom"
                  ? CUSTOM_PRINCIPAL
                  : grant.principal || undefined
              }
              placeholder={text(
                "选择用户、API Key、服务账号或外部身份",
                "Select a user, API key, service account, or external identity",
              )}
              options={[
                {
                  label: text("用户", "Users"),
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
                  label: text("服务账号", "Service accounts"),
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
                  label: text("外部身份", "External identities"),
                  options: [
                    {
                      value: CUSTOM_PRINCIPAL,
                      label: text("OIDC / 自定义 actor", "OIDC / custom actor"),
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
              label={text("主体标识", "Principal identifier")}
              hint={text(
                "完整 actor，例如 oidc:gitlab:team/release",
                "Complete actor, for example oidc:gitlab:team/release",
              )}
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
      <Field label={text("权限级别", "Permission")}>
        <Select
          className="w-full"
          aria-label={text("权限级别", "Permission")}
          value={selectedPermission}
          options={[
            ...(selectedPermission === SNAPSHOT_PERMISSION
              ? [
                  {
                    value: SNAPSHOT_PERMISSION,
                    label: text(
                      "已保存的权限快照",
                      "Saved permission snapshot",
                    ),
                  },
                ]
              : []),
            {
              label: text("内置权限", "Built-in permissions"),
              options: [
                {
                  value: `${BUILTIN_PERMISSION_PREFIX}read`,
                  label: text("读取 · 浏览 / 拉取", "Read · browse / pull"),
                },
                {
                  value: `${BUILTIN_PERMISSION_PREFIX}write`,
                  label: text("写入 · 发布 / 编辑", "Write · publish / edit"),
                },
                {
                  value: `${BUILTIN_PERMISSION_PREFIX}admin`,
                  label: text("管理 · 授权 / 删除", "Admin · grant / delete"),
                },
                {
                  value: `${BUILTIN_PERMISSION_PREFIX}intelligence`,
                  label: text(
                    "制品情报 · 安全元数据",
                    "Artifact intelligence · security metadata",
                  ),
                },
              ],
            },
            {
              label: text("自定义角色", "Custom roles"),
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
          {text("本规则授予", "Granted by this rule")}
        </span>
        <div className="flex min-h-8 items-center">
          <Badge tone={grantTone(level)}>
            {grantedCapabilitiesLabel(grant.scopes, text)}
          </Badge>
        </div>
      </div>
      <div className="sm:col-span-2">
        <Field label={text("资源范围", "Resource scope")} group>
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
