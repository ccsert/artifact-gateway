import { Descriptions } from "antd";
import type { CurrentIdentity } from "../client";
import { Badge } from "./Badge";
import { usePreferences } from "../lib/preferences";

export function authenticationKindLabel(kind: CurrentIdentity["kind"]): string {
  const labels: Record<CurrentIdentity["kind"], string> = {
    static_admin: "静态管理员令牌",
    static_resolver: "静态解析凭据",
    local_session: "本地用户会话",
    api_key: "API Key",
    service_account_credential: "服务账号凭据",
    oidc: "OIDC",
  };
  return labels[kind];
}

export function IdentitySummary({ identity }: { identity: CurrentIdentity }) {
  const { text } = usePreferences();
  const role = identity.role;
  const kindLabels: Record<CurrentIdentity["kind"], string> = {
    static_admin: text("静态管理员令牌", "Static administrator token"),
    static_resolver: text("静态解析凭据", "Static resolver credential"),
    local_session: text("本地用户会话", "Local user session"),
    api_key: "API Key",
    service_account_credential: text(
      "服务账号凭据",
      "Service account credential",
    ),
    oidc: "OIDC",
  };
  const roleLabels = {
    admin: text("管理员", "Administrator"),
    writer: text("写入者", "Writer"),
    reader: text("只读者", "Reader"),
  };
  return (
    <div className="min-w-0">
      <Descriptions
        className="ag-identity-summary"
        size="small"
        colon={false}
        column={4}
        items={[
          {
            key: "actor",
            label: text("主体", "Principal"),
            children: (
              <span className="font-mono text-xs text-zinc-200">
                {identity.actor}
              </span>
            ),
          },
          {
            key: "kind",
            label: text("认证来源", "Authentication"),
            children: (
              <Badge tone="visualization-1">{kindLabels[identity.kind]}</Badge>
            ),
          },
          {
            key: "role",
            label: text("全局角色", "Global role"),
            children: role ? (
              <Badge
                tone={
                  role === "admin"
                    ? "visualization-3"
                    : role === "writer"
                      ? "visualization-5"
                      : "visualization-4"
                }
              >
                {roleLabels[role]}
              </Badge>
            ) : (
              <span className="text-xs text-zinc-500">
                {text("无，由仓库规则判定", "None; repository rules decide")}
              </span>
            ),
          },
          {
            key: "administrator",
            label: text("管理员身份", "Administrator"),
            children: identity.administrator ? (
              <Badge tone="danger">{text("是", "Yes")}</Badge>
            ) : (
              <Badge tone="neutral">{text("否", "No")}</Badge>
            ),
          },
        ]}
      />
      {identity.oidc && (
        <div className="mt-3 flex min-w-0 items-start gap-4 border-t border-zinc-800/70 pt-3 text-xs">
          <span className="shrink-0 text-zinc-500">
            {text("OIDC 映射", "OIDC mapping")}
          </span>
          <div className="flex min-w-0 flex-wrap gap-2">
            {identity.oidc.adminSubject && (
              <Badge tone="danger">
                {text(
                  "subject 管理员名单 → admin",
                  "Administrator subject list → admin",
                )}
              </Badge>
            )}
            {identity.oidc.roleMappings.map((mapping) => (
              <Badge
                key={`${mapping.externalRole}-${mapping.gatewayRole}`}
                tone={
                  mapping.gatewayRole === "admin"
                    ? "visualization-3"
                    : mapping.gatewayRole === "writer"
                      ? "visualization-5"
                      : "visualization-4"
                }
              >
                {mapping.externalRole} → {mapping.gatewayRole}
              </Badge>
            ))}
            {!identity.oidc.adminSubject &&
              identity.oidc.roleMappings.length === 0 && (
                <span className="text-zinc-500">
                  {text(
                    "未命中已配置的角色映射",
                    "No configured role mapping matched",
                  )}
                </span>
              )}
          </div>
        </div>
      )}
    </div>
  );
}
