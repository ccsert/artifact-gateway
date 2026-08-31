import { useCallback, useEffect, useState } from "react";
import { Alert, Button, Input, Select, Space, Table, Tooltip } from "antd";
import type { ColumnsType } from "antd/es/table";
import { DeleteOutlined, EditOutlined, PlusOutlined } from "@ant-design/icons";
import {
  listAuthorizationRoles,
  listApiKeys,
  listGrants,
  listServiceAccounts,
  listUsers,
  replaceGrants,
} from "../../client";
import type {
  ApiKey,
  AuthorizationRole,
  Grant,
  Repository,
  ServiceAccount,
  User,
} from "../../client";
import { Badge, type BadgeTone } from "../../components/Badge";
import {
  EmptyState,
  ErrorBanner,
  Loading,
  isNotFound,
} from "../../components/Feedback";
import { Modal, useDisclosure } from "../../components/Modal";
import { usePreferences } from "../../lib/preferences";
import {
  isActiveApiKeyPrincipal,
  isActiveUserPrincipal,
} from "../../lib/accessPrincipals";
import { RepositoryFeatureUnavailable } from "./RepositoryFeatureUnavailable";
import { ResourcePrefixEditor } from "../../components/ResourcePrefixEditor";

type Localize = (chinese: string, english: string) => string;

type GrantLevel = "read" | "write" | "admin" | "intelligence";
const CUSTOM_PRINCIPAL = "__custom__";
const BUILTIN_PERMISSION_PREFIX = "builtin:";
const CUSTOM_ROLE_PREFIX = "role:";
const SNAPSHOT_PERMISSION = "snapshot";

type DraftGrant = Grant & { roleId?: string };

interface PrincipalOption {
  value: string;
  label: string;
  detail: string;
}

type PrincipalKind = "user" | "api-key" | "service-account" | "custom";

function principalKind(principal: string): PrincipalKind {
  if (principal.startsWith("user:")) return "user";
  if (principal.startsWith("api-key:")) return "api-key";
  if (principal.startsWith("service-account:")) return "service-account";
  return "custom";
}

function principalEditorKind(principal: string): PrincipalKind | "" {
  if (principal === CUSTOM_PRINCIPAL) return "custom";
  return principal ? principalKind(principal) : "";
}

function grantTone(level: GrantLevel): BadgeTone {
  if (level === "intelligence") return "visualization-1";
  if (level === "admin") return "visualization-3";
  if (level === "write") return "visualization-5";
  return "visualization-4";
}

function grantLevel(scopes: Grant["scopes"]): GrantLevel {
  if (scopes.includes("repositories:intelligence")) return "intelligence";
  if (scopes.includes("repositories:admin")) return "admin";
  if (scopes.includes("repositories:write")) return "write";
  return "read";
}

function scopesForLevel(level: GrantLevel): Grant["scopes"] {
  return [`repositories:${level}`] as Grant["scopes"];
}

function permissionSelection(
  grant: DraftGrant,
  roles: AuthorizationRole[],
): string {
  if (grant.roleId && roles.some((role) => role.id === grant.roleId))
    return `${CUSTOM_ROLE_PREFIX}${grant.roleId}`;
  return grant.scopes.length === 1
    ? `${BUILTIN_PERMISSION_PREFIX}${grantLevel(grant.scopes)}`
    : SNAPSHOT_PERMISSION;
}

function grantedCapabilitiesLabel(
  scopes: Grant["scopes"],
  text: Localize,
): string {
  if (scopes.includes("repositories:admin"))
    return text(
      "读取 + 写入 + 管理 + 制品情报",
      "Read + write + admin + intelligence",
    );
  const capabilities: string[] = scopes.includes("repositories:write")
    ? [text("读取 + 写入", "Read + write")]
    : scopes.includes("repositories:read")
      ? [text("读取", "Read")]
      : [];
  if (scopes.includes("repositories:intelligence"))
    capabilities.push(text("制品情报", "Artifact intelligence"));
  return capabilities.join(" + ");
}

function principalOptions(
  users: User[],
  apiKeys: ApiKey[],
  serviceAccounts: ServiceAccount[],
  text: Localize,
): PrincipalOption[] {
  return [
    ...users.filter(isActiveUserPrincipal).map((user) => ({
      value: `user:${user.name}`,
      label: `${text("用户", "User")} · ${user.name}`,
      detail: `${text("全局角色", "Global role")} ${user.role}`,
    })),
    ...apiKeys
      .filter((key) => isActiveApiKeyPrincipal(key))
      .map((key) => ({
        value: `api-key:${key.id}`,
        label: `API Key · ${key.name}`,
        detail: `${text("全局角色", "Global role")} ${key.roles.join(", ")}`,
      })),
    ...serviceAccounts
      .filter((account) => account.state === "active")
      .map((account) => ({
        value: `service-account:${account.id}`,
        label: `${text("服务账号", "Service account")} · ${account.name}`,
        detail: text(
          "无全局角色，由仓库授权决定",
          "No global role; repository grants decide access",
        ),
      })),
  ];
}

export function RepositoryGrantsTab({ repo }: { repo: Repository }) {
  const { text } = usePreferences();
  const [grants, setGrants] = useState<Grant[] | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [principalChoices, setPrincipalChoices] = useState<PrincipalOption[]>(
    [],
  );
  const [authorizationRoles, setAuthorizationRoles] = useState<
    AuthorizationRole[]
  >([]);
  const [principalChoicesError, setPrincipalChoicesError] =
    useState<unknown>(null);
  const [version, setVersion] = useState("");
  const editor = useDisclosure();
  const [draft, setDraft] = useState<DraftGrant[]>([]);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<unknown>(null);

  const load = useCallback(async () => {
    setError(null);
    const {
      data,
      error: err,
      response,
    } = await listGrants({ path: { repositoryId: repo.id } });
    if (err) {
      setError(err);
      return;
    }
    setGrants(data ?? []);
    const etag = response?.headers.get("ETag");
    setVersion(etag ? etag.replaceAll('"', "") : repo.version);
  }, [repo.id, repo.version]);

  useEffect(() => {
    void load();
  }, [load]);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      const [usersResult, apiKeysResult, serviceAccountsResult, rolesResult] =
        await Promise.all([
          listUsers(),
          listApiKeys(),
          listServiceAccounts({ query: { pageSize: 200 } }),
          listAuthorizationRoles(),
        ]);
      if (cancelled) return;
      if (
        usersResult.error ||
        apiKeysResult.error ||
        serviceAccountsResult.error ||
        rolesResult.error
      ) {
        setPrincipalChoicesError(
          new Error(
            text(
              "无法加载用户、API Key、服务账号或自定义角色，可继续使用自定义身份和内置权限。",
              "Could not load users, API keys, service accounts, or custom roles. You can still use a custom identity and built-in permissions.",
            ),
          ),
        );
      }
      setPrincipalChoices(
        principalOptions(
          usersResult.data?.items ?? [],
          apiKeysResult.data?.items ?? [],
          serviceAccountsResult.data?.items ?? [],
          text,
        ),
      );
      setAuthorizationRoles(rolesResult.data ?? []);
    })();
    return () => {
      cancelled = true;
    };
  }, [text]);

  const openEditor = () => {
    setDraft(
      grants ? grants.map((g) => ({ ...g, scopes: [...g.scopes] })) : [],
    );
    setSaveError(null);
    editor.show();
  };

  const save = async () => {
    if (
      draft.some(
        (grant) =>
          !grant.principal.trim() || grant.principal === CUSTOM_PRINCIPAL,
      )
    ) {
      setSaveError(
        new Error(
          text(
            "请为每条授权规则选择或填写授权主体；不需要的空行请先移除。",
            "Select or enter a principal for every grant. Remove unused blank rows first.",
          ),
        ),
      );
      return;
    }
    const normalized = draft.map((grant) => ({
      principal:
        grant.principal === CUSTOM_PRINCIPAL ? "" : grant.principal.trim(),
      scopes: [...grant.scopes],
      resourcePrefix: grant.resourcePrefix?.trim() || undefined,
    }));
    const duplicate = new Set<string>();
    for (const grant of normalized) {
      const key = `${grant.principal}\x00${grant.resourcePrefix ?? ""}`;
      if (duplicate.has(key)) {
        setSaveError(
          new Error(
            text(
              "存在重复的授权主体与资源范围，请合并或删除重复规则。",
              "Duplicate principal and resource scope. Merge or remove the duplicate grant.",
            ),
          ),
        );
        return;
      }
      duplicate.add(key);
    }
    setSaving(true);
    setSaveError(null);
    const { error: err } = await replaceGrants({
      path: { repositoryId: repo.id },
      body: normalized,
      headers: { "If-Match": version },
    });
    setSaving(false);
    if (err) {
      setSaveError(err);
      return;
    }
    editor.hide();
    void load();
  };

  if (error !== null)
    return isNotFound(error) ? (
      <RepositoryFeatureUnavailable
        feature={text("访问授权", "Access grants")}
      />
    ) : (
      <ErrorBanner error={error} onRetry={load} />
    );
  if (!grants) return <Loading />;

  const grantColumns: ColumnsType<Grant> = [
    {
      title: text("授权主体", "Principal"),
      dataIndex: "principal",
      key: "principal",
      width: 320,
      render: (value: string) => (
        <div>
          <div className="font-mono text-xs text-zinc-200">
            {principalChoices.find((choice) => choice.value === value)?.label ??
              value}
          </div>
          <div className="mt-0.5 font-mono text-xs text-zinc-600">{value}</div>
        </div>
      ),
    },
    {
      title: text("授予能力", "Granted capabilities"),
      key: "level",
      width: 260,
      render: (_, grant) => (
        <Badge tone={grantTone(grantLevel(grant.scopes))}>
          {grantedCapabilitiesLabel(grant.scopes, text)}
        </Badge>
      ),
    },
    {
      title: text("资源范围", "Resource scope"),
      dataIndex: "resourcePrefix",
      key: "resourcePrefix",
      width: 260,
      render: (value?: string) => (
        <span className="font-mono text-xs text-zinc-500">
          {value || text("整个仓库", "Entire repository")}
        </span>
      ),
    },
  ];

  return (
    <div>
      <div className="mb-4 flex justify-end">
        <Button type="primary" icon={<EditOutlined />} onClick={openEditor}>
          {text("编辑授权", "Edit grants")}
        </Button>
      </div>
      {grants.length === 0 ? (
        <EmptyState
          title={text("暂无授权规则", "No access grants")}
          hint={text(
            "在编辑授权中选择用户、API Key、服务账号，或填写 OIDC subject / 自定义 actor。",
            "Choose a user, API key, or service account in Edit grants, or enter an OIDC subject/custom actor.",
          )}
        />
      ) : (
        <Table<Grant>
          className="ag-console-table"
          rowKey={(grant) => `${grant.principal}-${grant.resourcePrefix ?? ""}`}
          size="middle"
          dataSource={grants}
          columns={grantColumns}
          pagination={false}
          scroll={{ x: 760, y: 380 }}
        />
      )}
      <Modal
        open={editor.open}
        title={text("编辑访问授权", "Edit access grants")}
        onClose={editor.hide}
        wide
        footer={
          <Space>
            <Button onClick={editor.hide}>{text("取消", "Cancel")}</Button>
            <Button type="primary" onClick={save} loading={saving}>
              {text("保存", "Save")}
            </Button>
          </Space>
        }
      >
        <div className="space-y-3">
          {saveError !== null && <ErrorBanner error={saveError} />}
          <Alert
            type="info"
            showIcon
            title={text(
              "仓库规则只会追加权限，不能撤销用户或 API Key 已有的全局角色；服务账号没有全局角色。",
              "Repository rules add permissions; they cannot revoke an existing global user or API key role. Service accounts have no global role.",
            )}
          />
          {principalChoicesError !== null && (
            <Alert
              type="warning"
              showIcon
              title={text(
                "用户、API Key 或服务账号列表暂时不可用；仍可选择“OIDC / 自定义 actor”并填写主体标识。",
                "Users, API keys, or service accounts are temporarily unavailable. You can still choose OIDC/custom actor and enter its identifier.",
              )}
            />
          )}
          <div>
            <div className="grid grid-cols-[minmax(300px,1.35fr)_170px_minmax(360px,1.5fr)_170px_40px] items-center gap-3 px-2 pb-2 text-xs font-medium text-zinc-500">
              <span>{text("主体", "Principal")}</span>
              <span>{text("权限级别", "Permission")}</span>
              <span>{text("资源范围", "Resource scope")}</span>
              <span>{text("本规则授予", "Granted by this rule")}</span>
              <span />
            </div>
            <div className="border-b border-zinc-800/70">
              {draft.map((g, i) => {
                const kind = principalEditorKind(g.principal);
                const selectedChoice = principalChoices.find(
                  (choice) => choice.value === g.principal,
                );
                const level = grantLevel(g.scopes);
                const selectedPermission = permissionSelection(
                  g,
                  authorizationRoles,
                );
                return (
                  <div
                    key={i}
                    className="grid grid-cols-[minmax(300px,1.35fr)_170px_minmax(360px,1.5fr)_170px_40px] items-start gap-3 border-t border-zinc-800/70 px-2 py-3"
                  >
                    <div className="min-w-0">
                      <Select
                        className="w-full"
                        aria-label={text("授权主体", "Principal")}
                        showSearch={{ optionFilterProp: "label" }}
                        value={
                          kind === "custom"
                            ? CUSTOM_PRINCIPAL
                            : g.principal || undefined
                        }
                        placeholder={text(
                          "选择用户、API Key、服务账号或外部身份",
                          "Select a user, API key, service account, or external identity",
                        )}
                        options={[
                          {
                            label: text("用户", "Users"),
                            options: principalChoices
                              .filter((choice) =>
                                choice.value.startsWith("user:"),
                              )
                              .map((choice) => ({
                                value: choice.value,
                                label: `${choice.label} · ${choice.detail}`,
                              })),
                          },
                          {
                            label: "API Keys",
                            options: principalChoices
                              .filter((choice) =>
                                choice.value.startsWith("api-key:"),
                              )
                              .map((choice) => ({
                                value: choice.value,
                                label: `${choice.label} · ${choice.detail}`,
                              })),
                          },
                          {
                            label: text("服务账号", "Service accounts"),
                            options: principalChoices
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
                                label: text(
                                  "OIDC / 自定义 actor",
                                  "OIDC / custom actor",
                                ),
                              },
                            ],
                          },
                        ]}
                        onChange={(value) =>
                          setDraft((d) =>
                            d.map((x, j) =>
                              j === i
                                ? {
                                    ...x,
                                    principal:
                                      value === CUSTOM_PRINCIPAL
                                        ? principalEditorKind(x.principal) ===
                                          "custom"
                                          ? x.principal
                                          : CUSTOM_PRINCIPAL
                                        : value,
                                  }
                                : x,
                            ),
                          )
                        }
                      />
                      {kind === "custom" && (
                        <Input
                          className="mt-2 font-mono"
                          placeholder={text(
                            "完整 actor，例如 oidc:gitlab:team/release",
                            "Complete actor, for example oidc:gitlab:team/release",
                          )}
                          value={
                            g.principal === CUSTOM_PRINCIPAL ? "" : g.principal
                          }
                          onChange={(event) =>
                            setDraft((d) =>
                              d.map((x, j) =>
                                j === i
                                  ? { ...x, principal: event.target.value }
                                  : x,
                              ),
                            )
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
                                label: text(
                                  "读取 · 浏览 / 拉取",
                                  "Read · browse / pull",
                                ),
                              },
                              {
                                value: `${BUILTIN_PERMISSION_PREFIX}write`,
                                label: text(
                                  "写入 · 发布 / 编辑",
                                  "Write · publish / edit",
                                ),
                              },
                              {
                                value: `${BUILTIN_PERMISSION_PREFIX}admin`,
                                label: text(
                                  "管理 · 授权 / 删除",
                                  "Admin · grant / delete",
                                ),
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
                                (item) =>
                                  item.id ===
                                  value.slice(CUSTOM_ROLE_PREFIX.length),
                              )
                            : undefined;
                          const nextScopes = role
                            ? ([...role.scopes] as Grant["scopes"])
                            : scopesForLevel(
                                value.slice(
                                  BUILTIN_PERMISSION_PREFIX.length,
                                ) as GrantLevel,
                              );
                          setDraft((d) =>
                            d.map((x, j) =>
                              j === i
                                ? {
                                    ...x,
                                    scopes: nextScopes,
                                    roleId: role?.id,
                                  }
                                : x,
                            ),
                          );
                        }}
                      />
                    </div>
                    <div className="min-w-0">
                      <ResourcePrefixEditor
                        format={repo.format}
                        value={g.resourcePrefix ?? ""}
                        onChange={(value) =>
                          setDraft((d) =>
                            d.map((x, j) =>
                              j === i ? { ...x, resourcePrefix: value } : x,
                            ),
                          )
                        }
                      />
                    </div>
                    <div className="flex min-h-10 items-center">
                      <Badge tone={grantTone(level)}>
                        {grantedCapabilitiesLabel(g.scopes, text)}
                      </Badge>
                    </div>
                    <Tooltip title={text("移除规则", "Remove rule")}>
                      <Button
                        type="text"
                        danger
                        aria-label={text("移除规则", "Remove rule")}
                        icon={<DeleteOutlined />}
                        onClick={() =>
                          setDraft((d) => d.filter((_, j) => j !== i))
                        }
                      />
                    </Tooltip>
                  </div>
                );
              })}
              {draft.length === 0 && (
                <div className="border-t border-zinc-800/70 px-3 py-8 text-center text-xs text-zinc-600">
                  {text("尚未添加授权规则", "No access grants added")}
                </div>
              )}
            </div>
          </div>
          <Button
            block
            type="dashed"
            icon={<PlusOutlined />}
            onClick={() =>
              setDraft((d) => [
                ...d,
                { principal: "", scopes: ["repositories:read"] },
              ])
            }
          >
            {text("添加授权规则", "Add access grant")}
          </Button>
        </div>
      </Modal>
    </div>
  );
}
