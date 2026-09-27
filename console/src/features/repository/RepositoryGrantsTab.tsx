import { useCallback, useEffect, useState } from "react";
import { Alert, App, Button, Popconfirm, Space } from "antd";
import type { ColumnsType } from "antd/es/table";
import { PlusOutlined } from "@ant-design/icons";
import {
  deleteGrant,
  listAuthorizationRoles,
  listApiKeys,
  listGrants,
  listServiceAccounts,
  listUsers,
  upsertGrant,
} from "../../client";
import type { AuthorizationRole, Grant, Repository } from "../../client";
import { Badge } from "../../components/ui/Badge";
import {
  EmptyState,
  ErrorBanner,
  Loading,
  Notice,
  isNotFound,
} from "../../components/ui/Feedback";
import { Modal, useDisclosure } from "../../components/ui/Modal";
import { usePreferences } from "../../lib/preferences";
import { RepositoryFeatureUnavailable } from "./RepositoryFeatureUnavailable";
import { GrantForm } from "../access-control/GrantForm";
import {
  CUSTOM_PRINCIPAL,
  emptyGrant,
  grantedCapabilitiesLabel,
  grantLevel,
  grantRowKey,
  grantTone,
  principalOptions,
  type DraftGrant,
  type PrincipalOption,
} from "../access-control/grantDraft";
import { ConsoleTable } from "../../components/ui/ConsolePrimitives";

function grantKey(grant: Pick<Grant, "principal" | "resourcePrefix">): string {
  return grantRowKey(grant.principal, grant.resourcePrefix);
}

/**
 * The repository's grants, managed row by row. Every write calls the
 * single-grant endpoints: `upsertGrant` creates or replaces exactly the
 * (principal, resource prefix) row the editor was opened for, and `deleteGrant`
 * removes exactly one row, so an administrator editing one principal can never
 * clobber a concurrent change to another — the failure mode of the retired
 * whole-list editor. Editing a row's principal or resource prefix is an upsert
 * of the new key followed by a delete of the old one.
 */
export function RepositoryGrantsTab({ repo }: { repo: Repository }) {
  const { message } = App.useApp();
  const { text } = usePreferences();
  const [grants, setGrants] = useState<Grant[] | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [mutationError, setMutationError] = useState<unknown>(null);
  const [principalChoices, setPrincipalChoices] = useState<PrincipalOption[]>(
    [],
  );
  const [authorizationRoles, setAuthorizationRoles] = useState<
    AuthorizationRole[]
  >([]);
  const [principalChoicesError, setPrincipalChoicesError] =
    useState<unknown>(null);
  const editor = useDisclosure();
  const [draft, setDraft] = useState<DraftGrant | null>(null);
  /**
   * The (principal, resource prefix) key the open editor acts on, or `null`
   * when composing a brand-new grant. Saving with a changed key upserts the
   * new row and then deletes the old one.
   */
  const [editingKey, setEditingKey] = useState<{
    principal: string;
    resourcePrefix: string;
  } | null>(null);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<unknown>(null);
  const [removing, setRemoving] = useState("");

  const load = useCallback(async () => {
    setError(null);
    const { data, error: err } = await listGrants({
      path: { repositoryId: repo.id },
    });
    if (err) {
      setError(err);
      return;
    }
    setGrants(data ?? []);
  }, [repo.id]);

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

  const openAdd = () => {
    setDraft(emptyGrant());
    setEditingKey(null);
    setSaveError(null);
    editor.show();
  };

  const openEdit = (grant: Grant) => {
    setDraft({ ...grant, scopes: [...grant.scopes] });
    setEditingKey({
      principal: grant.principal,
      resourcePrefix: grant.resourcePrefix ?? "",
    });
    setSaveError(null);
    editor.show();
  };

  const save = async () => {
    if (!draft) return;
    if (!draft.principal.trim() || draft.principal === CUSTOM_PRINCIPAL) {
      setSaveError(
        new Error(
          text(
            "请选择或填写授权主体。",
            "Select or enter a principal for the grant.",
          ),
        ),
      );
      return;
    }
    const normalized = {
      principal: draft.principal.trim(),
      scopes: [...draft.scopes],
      resourcePrefix: draft.resourcePrefix?.trim() || undefined,
    };
    setSaving(true);
    setSaveError(null);
    try {
      // The row's key changed: the old key is a different row and must go, or
      // the edit would fork one grant into two. Removing it first means a
      // failure leaves the grant narrower (or absent) rather than wider than
      // the administrator asked for, and a save that fails here never wrote
      // the new row.
      const keyChanged =
        editingKey !== null &&
        (editingKey.principal !== normalized.principal ||
          editingKey.resourcePrefix !== (normalized.resourcePrefix ?? ""));
      if (editingKey && keyChanged) {
        const { error: deleteError } = await deleteGrant({
          path: { repositoryId: repo.id },
          query: {
            principal: editingKey.principal,
            resourcePrefix: editingKey.resourcePrefix || undefined,
          },
        });
        if (deleteError && !isNotFound(deleteError)) {
          setSaveError(
            new Error(
              text(
                "旧授权删除失败，新授权未保存，请重试。",
                "The previous row could not be removed, so the new grant was not saved. Please retry.",
              ),
            ),
          );
          return;
        }
      }
      const { data, error: err } = await upsertGrant({
        path: { repositoryId: repo.id },
        body: normalized,
      });
      if (err) {
        setSaveError(
          keyChanged
            ? new Error(
                text(
                  "旧行已移除，但新授权保存失败，请再次保存以写入新授权。",
                  "The previous row was removed, but saving the new grant failed. Save again to write it.",
                ),
              )
            : err,
        );
        return;
      }
      setGrants(data ?? null);
      editor.hide();
      void load();
    } finally {
      setSaving(false);
    }
  };

  const remove = async (grant: Grant) => {
    const key = grantKey(grant);
    setRemoving(key);
    setMutationError(null);
    try {
      const { error: err } = await deleteGrant({
        path: { repositoryId: repo.id },
        query: {
          principal: grant.principal,
          resourcePrefix: grant.resourcePrefix || undefined,
        },
      });
      if (err && !isNotFound(err)) {
        setMutationError(err);
        return;
      }
      setGrants((current) =>
        current ? current.filter((item) => grantKey(item) !== key) : current,
      );
      message.success(text("授权已移除", "Grant removed"));
    } finally {
      setRemoving("");
    }
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

  const draftKeyExists =
    draft !== null &&
    editingKey === null &&
    grants.some(
      (grant) =>
        grant.principal === draft.principal.trim() &&
        (grant.resourcePrefix ?? "") === (draft.resourcePrefix?.trim() ?? ""),
    );

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
      width: 240,
      render: (value?: string) => (
        <span className="font-mono text-xs text-zinc-500">
          {value || text("整个仓库", "Entire repository")}
        </span>
      ),
    },
    {
      title: text("操作", "Actions"),
      key: "actions",
      fixed: "right",
      width: 130,
      render: (_, grant) => (
        <Space size="small">
          <Button type="link" size="small" onClick={() => openEdit(grant)}>
            {text("编辑", "Edit")}
          </Button>
          <Popconfirm
            title={text("移除这条授权？", "Remove this grant?")}
            description={text(
              "只移除当前行，其他主体的授权不受影响。",
              "Only this row is removed; every other principal keeps its grants.",
            )}
            okText={text("移除", "Remove")}
            cancelText={text("取消", "Cancel")}
            okButtonProps={{
              danger: true,
              loading: removing === grantKey(grant),
            }}
            onConfirm={() => void remove(grant)}
          >
            <Button
              type="link"
              size="small"
              danger
              loading={removing === grantKey(grant)}
              aria-label={text("移除该行授权", "Remove this grant row")}
            >
              {text("移除", "Remove")}
            </Button>
          </Popconfirm>
        </Space>
      ),
    },
  ];

  return (
    <div className="space-y-4">
      <div className="flex justify-end">
        <Button type="primary" icon={<PlusOutlined />} onClick={openAdd}>
          {text("添加授权", "Add grant")}
        </Button>
      </div>
      {mutationError !== null && <ErrorBanner error={mutationError} />}
      {grants.length === 0 ? (
        <EmptyState
          title={text("暂无授权规则", "No access grants")}
          hint={text(
            "点击「添加授权」选择用户、API Key、服务账号，或填写 OIDC subject / 自定义 actor。",
            "Choose a user, API key, or service account via Add grant, or enter an OIDC subject/custom actor.",
          )}
        />
      ) : (
        <ConsoleTable<Grant>
          rowKey={(grant) => grantKey(grant)}
          dataSource={grants}
          columns={grantColumns}
          pagination={false}
          scroll={{ x: 890, y: 380 }}
        />
      )}
      <Modal
        open={editor.open}
        title={
          editingKey
            ? text("编辑授权", "Edit grant")
            : text("添加授权", "Add grant")
        }
        onClose={editor.hide}
        width={680}
        footer={
          <Space>
            <Button onClick={editor.hide}>{text("取消", "Cancel")}</Button>
            <Button type="primary" onClick={() => void save()} loading={saving}>
              {text("保存", "Save")}
            </Button>
          </Space>
        }
      >
        <div className="space-y-3">
          {saveError !== null && <ErrorBanner error={saveError} />}
          {draftKeyExists && (
            <Alert
              type="warning"
              showIcon
              title={text(
                "该主体在此资源范围已有授权，保存将替换原有规则。",
                "This principal already holds a grant at this resource scope; saving replaces it.",
              )}
            />
          )}
          <Notice
            tone="info"
            closable={false}
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
          {draft !== null && (
            <GrantForm
              grant={draft}
              principalOptions={principalChoices}
              authorizationRoles={authorizationRoles}
              format={repo.format}
              onChange={setDraft}
            />
          )}
        </div>
      </Modal>
    </div>
  );
}
