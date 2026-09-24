import { Button, Input, Select, Tooltip } from "antd";
import { DeleteOutlined } from "@ant-design/icons";
import type { AuthorizationRole, Repository } from "../../client";
import { Badge } from "../../components/ui/Badge";
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
 * One controlled grant row: a principal, a permission level (built-in or a
 * custom authorization role), a resource prefix, the capabilities the row
 * grants, and its remove button.
 *
 * The parent owns the draft array and all state; this component only renders a
 * row and reports the next value of that row through `onChange`. It is fully
 * self-contained so any grant editor can render a row without importing
 * anything from the repository grants tab.
 */
export type GrantRowEditorProps = {
  /** The draft grant this row edits. */
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
  /** Receives the complete next row after any control changes. */
  onChange: (next: DraftGrant) => void;
  /** Removes this row from the parent draft. */
  onRemove: () => void;
};

export function GrantRowEditor({
  grant,
  principalOptions,
  authorizationRoles,
  format,
  onChange,
  onRemove,
}: GrantRowEditorProps) {
  const { text } = usePreferences();
  const kind = principalEditorKind(grant.principal);
  const selectedChoice = principalOptions.find(
    (choice) => choice.value === grant.principal,
  );
  const level = grantLevel(grant.scopes);
  const selectedPermission = permissionSelection(grant, authorizationRoles);

  return (
    <div className="grid grid-cols-[minmax(300px,1.35fr)_170px_minmax(360px,1.5fr)_170px_40px] items-start gap-3 border-t border-zinc-800/70 px-2 py-3">
      <div className="min-w-0">
        <Select
          className="w-full"
          aria-label={text("授权主体", "Principal")}
          showSearch={{ optionFilterProp: "label" }}
          value={
            kind === "custom" ? CUSTOM_PRINCIPAL : grant.principal || undefined
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
                .filter((choice) => choice.value.startsWith("service-account:"))
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
        {kind === "custom" && (
          <Input
            className="mt-2 font-mono"
            placeholder={text(
              "完整 actor，例如 oidc:gitlab:team/release",
              "Complete actor, for example oidc:gitlab:team/release",
            )}
            value={grant.principal === CUSTOM_PRINCIPAL ? "" : grant.principal}
            onChange={(event) =>
              onChange({ ...grant, principal: event.target.value })
            }
          />
        )}
        <div className="mt-1 min-h-4 text-xs leading-4 text-zinc-600">
          {kind === "custom"
            ? text(
                "必须与认证完成后产生的 actor 完全一致",
                "Must exactly match the authenticated actor",
              )
            : selectedChoice?.detail}
        </div>
      </div>
      <div className="min-w-0">
        <Select
          className="w-full"
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
      </div>
      <div className="min-w-0">
        <ResourcePrefixEditor
          format={format}
          value={grant.resourcePrefix ?? ""}
          onChange={(value) => onChange({ ...grant, resourcePrefix: value })}
        />
      </div>
      <div className="flex min-h-10 items-center">
        <Badge tone={grantTone(level)}>
          {grantedCapabilitiesLabel(grant.scopes, text)}
        </Badge>
      </div>
      <Tooltip title={text("移除规则", "Remove rule")}>
        <Button
          type="text"
          danger
          aria-label={text("移除规则", "Remove rule")}
          icon={<DeleteOutlined />}
          onClick={onRemove}
        />
      </Tooltip>
    </div>
  );
}
