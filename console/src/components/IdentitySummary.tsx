import { Descriptions } from "antd";
import type { CurrentIdentity } from "../client";
import { Badge } from "./Badge";

const AUTHENTICATION_KIND_LABELS: Record<CurrentIdentity["kind"], string> = {
  static_admin: "静态管理员令牌",
  static_resolver: "静态解析凭据",
  local_session: "本地用户会话",
  api_key: "API Key",
  oidc: "OIDC",
};

const ROLE_LABELS: Record<NonNullable<CurrentIdentity["role"]>, string> = {
  admin: "管理员",
  writer: "写入者",
  reader: "只读者",
};

export function authenticationKindLabel(kind: CurrentIdentity["kind"]): string {
  return AUTHENTICATION_KIND_LABELS[kind];
}

export function IdentitySummary({ identity }: { identity: CurrentIdentity }) {
  const role = identity.role;
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
            label: "主体",
            children: (
              <span className="font-mono text-xs text-zinc-200">
                {identity.actor}
              </span>
            ),
          },
          {
            key: "kind",
            label: "认证来源",
            children: (
              <Badge tone="cyan">
                {authenticationKindLabel(identity.kind)}
              </Badge>
            ),
          },
          {
            key: "role",
            label: "全局角色",
            children: role ? (
              <Badge
                tone={
                  role === "admin"
                    ? "red"
                    : role === "writer"
                      ? "blue"
                      : "green"
                }
              >
                {ROLE_LABELS[role]}
              </Badge>
            ) : (
              <span className="text-xs text-zinc-500">无，由仓库规则判定</span>
            ),
          },
          {
            key: "administrator",
            label: "管理员身份",
            children: identity.administrator ? (
              <Badge tone="red">是</Badge>
            ) : (
              <Badge tone="zinc">否</Badge>
            ),
          },
        ]}
      />
      {identity.oidc && (
        <div className="mt-3 flex min-w-0 items-start gap-4 border-t border-zinc-800/70 pt-3 text-xs">
          <span className="shrink-0 text-zinc-500">OIDC 映射</span>
          <div className="flex min-w-0 flex-wrap gap-2">
            {identity.oidc.adminSubject && (
              <Badge tone="red">subject 管理员名单 → admin</Badge>
            )}
            {identity.oidc.roleMappings.map((mapping) => (
              <Badge
                key={`${mapping.externalRole}-${mapping.gatewayRole}`}
                tone={
                  mapping.gatewayRole === "admin"
                    ? "red"
                    : mapping.gatewayRole === "writer"
                      ? "blue"
                      : "green"
                }
              >
                {mapping.externalRole} → {mapping.gatewayRole}
              </Badge>
            ))}
            {!identity.oidc.adminSubject &&
              identity.oidc.roleMappings.length === 0 && (
                <span className="text-zinc-500">未命中已配置的角色映射</span>
              )}
          </div>
        </div>
      )}
    </div>
  );
}
