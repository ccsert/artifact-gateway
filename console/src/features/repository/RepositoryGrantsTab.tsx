import { useCallback, useEffect, useState } from "react";
import { Alert, Button, Space, Table } from "antd";
import type { ColumnsType } from "antd/es/table";
import { EditOutlined, PlusOutlined } from "@ant-design/icons";
import {
  listAuthorizationRoles,
  listApiKeys,
  listGrants,
  listServiceAccounts,
  listUsers,
  replaceGrants,
} from "../../client";
import type { AuthorizationRole, Grant, Repository } from "../../client";
import { Badge } from "../../components/ui/Badge";
import {
  EmptyState,
  ErrorBanner,
  Loading,
  isNotFound,
} from "../../components/ui/Feedback";
import { Modal, useDisclosure } from "../../components/ui/Modal";
import { usePreferences } from "../../lib/preferences";
import { RepositoryFeatureUnavailable } from "./RepositoryFeatureUnavailable";
import { GrantRowEditor } from "../access-control/GrantRowEditor";
import {
  CUSTOM_PRINCIPAL,
  emptyGrant,
  grantedCapabilitiesLabel,
  grantLevel,
  grantTone,
  principalOptions,
  type DraftGrant,
  type PrincipalOption,
} from "../access-control/grantDraft";

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
              {draft.map((g, i) => (
                <GrantRowEditor
                  key={i}
                  grant={g}
                  principalOptions={principalChoices}
                  authorizationRoles={authorizationRoles}
                  format={repo.format}
                  onChange={(next) =>
                    setDraft((d) => d.map((x, j) => (j === i ? next : x)))
                  }
                  onRemove={() => setDraft((d) => d.filter((_, j) => j !== i))}
                />
              ))}
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
            onClick={() => setDraft((d) => [...d, emptyGrant()])}
          >
            {text("添加授权规则", "Add access grant")}
          </Button>
        </div>
      </Modal>
    </div>
  );
}
