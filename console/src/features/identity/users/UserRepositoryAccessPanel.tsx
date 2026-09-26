import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  DeleteOutlined,
  EditOutlined,
  PlusOutlined,
  ReloadOutlined,
} from "@ant-design/icons";
import {
  Alert,
  App,
  Button,
  Popconfirm,
  Select,
  Space,
  Tooltip,
  Typography,
} from "antd";
import {
  deleteGrant,
  listAuthorizationRoles,
  listGrants,
  listRepositories,
  listRepositoryGrants,
  upsertGrant,
} from "../../../client";
import type {
  AuthorizationRole,
  Grant,
  Repository,
  RepositoryGrantRecord,
} from "../../../client";
import { Badge, FormatBadge } from "../../../components/ui/Badge";
import {
  EmptyState,
  ErrorBanner,
  Loading,
  isNotFound,
} from "../../../components/ui/Feedback";
import { Modal, useDisclosure } from "../../../components/ui/Modal";
import { usePreferences } from "../../../lib/preferences";
import { GrantRowEditor } from "../../access-control/GrantRowEditor";
import {
  emptyGrant,
  grantedCapabilitiesLabel,
  grantLevel,
  grantTone,
  type DraftGrant,
  type PrincipalOption,
} from "../../access-control/grantDraft";

/** A blank prefix means repository-wide access, submitted as `undefined`. */
function normalizePrefix(prefix: string | undefined): string | undefined {
  return prefix?.trim() || undefined;
}

interface UserRepositoryAccessPanelProps {
  userId: string;
  username: string;
  refreshKey?: number;
}

/** Grants loaded for one account, so a switch cannot show another user's rows. */
type GrantSnapshot = {
  userId: string;
  items: RepositoryGrantRecord[];
};

/** The repository grant list the open editor was prefilled from. */
type ComposeBase = {
  repositoryId: string;
  grants: Grant[];
};

function recordKey(record: RepositoryGrantRecord): string {
  return `${record.repositoryId}\x00${record.resourcePrefix ?? ""}`;
}

/**
 * One account's per-repository grants, reachable from the user drawer instead
 * of visiting every repository page.
 *
 * Every write goes through the single-grant endpoints: saving a composed grant
 * upserts exactly its (principal, resource prefix) row, and removing a row
 * deletes the account's entries one by one, so this panel never submits the
 * repository's whole grant set and a concurrent edit to another principal
 * cannot be clobbered. An edit that changes the entry's prefix is an upsert
 * of the new prefix followed by a delete of the old one.
 *
 * The list below shows one row per stored entry, so an account holding several
 * resource prefixes on one repository stays visible and each row can be
 * edited or removed on its own.
 */
export function UserRepositoryAccessPanel({
  userId,
  username,
  refreshKey = 0,
}: UserRepositoryAccessPanelProps) {
  const { message } = App.useApp();
  const { text } = usePreferences();
  const editor = useDisclosure();
  const principal = `user:${username}`;
  const [snapshot, setSnapshot] = useState<GrantSnapshot | null>(null);
  const [repositories, setRepositories] = useState<Repository[]>([]);
  const [authorizationRoles, setAuthorizationRoles] = useState<
    AuthorizationRole[]
  >([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<unknown>(null);
  const [repositoriesError, setRepositoriesError] = useState<unknown>(null);
  const [rolesError, setRolesError] = useState<unknown>(null);
  const [removing, setRemoving] = useState("");
  const [removeError, setRemoveError] = useState<unknown>(null);
  const [composeRepositoryId, setComposeRepositoryId] = useState("");
  /**
   * The normalized prefix of the entry the open editor acts on, or `null` when
   * composing a brand-new grant. `""` means the repository-wide entry, so the
   * target is a plain string and cannot be confused with "no target".
   */
  const [composeTargetPrefix, setComposeTargetPrefix] = useState<string | null>(
    null,
  );
  const [composeBase, setComposeBase] = useState<ComposeBase | null>(null);
  const [composeLoading, setComposeLoading] = useState(false);
  const [composeError, setComposeError] = useState<unknown>(null);
  const [draft, setDraft] = useState<DraftGrant | null>(null);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<unknown>(null);
  const composeRequest = useRef(0);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const [grantsResult, repositoriesResult, rolesResult] = await Promise.all(
        [
          listRepositoryGrants(),
          listRepositories({ query: { pageSize: 100 } }),
          listAuthorizationRoles(),
        ],
      );
      if (grantsResult.error || !grantsResult.data) {
        throw (
          grantsResult.error ??
          new Error(
            text("加载仓库授权失败", "Failed to load repository grants"),
          )
        );
      }
      setSnapshot({ userId, items: grantsResult.data });
      if (repositoriesResult.error || !repositoriesResult.data) {
        setRepositories([]);
        setRepositoriesError(
          repositoriesResult.error ??
            new Error(text("加载仓库列表失败", "Failed to load repositories")),
        );
      } else {
        setRepositories(
          [...repositoriesResult.data.items].sort((a, b) =>
            a.name.localeCompare(b.name),
          ),
        );
        setRepositoriesError(null);
      }
      if (rolesResult.error || !rolesResult.data) {
        setAuthorizationRoles([]);
        setRolesError(
          rolesResult.error ??
            new Error(
              text("加载自定义角色失败", "Failed to load custom roles"),
            ),
        );
      } else {
        setAuthorizationRoles(rolesResult.data);
        setRolesError(null);
      }
    } catch (caught) {
      setError(caught);
    } finally {
      setLoading(false);
    }
  }, [text, userId]);

  useEffect(() => {
    void load();
  }, [load, refreshKey]);

  /** Read one repository's current grants to prefill or target the editor. */
  const readRepository = useCallback(
    async (repositoryId: string): Promise<Grant[]> => {
      const { data, error: requestError } = await listGrants({
        path: { repositoryId },
      });
      if (requestError || !data) {
        throw (
          requestError ??
          new Error(
            text("加载仓库授权失败", "Failed to load repository grants"),
          )
        );
      }
      return data;
    },
    [text],
  );

  /**
   * Read the repository's grants and prefill the editor. `targetPrefix` is the
   * normalized prefix of the entry the user acted on (the empty string means
   * repository-wide), or `null` to compose a new grant. The target is
   * remembered, so an edit keeps pointing at that one entry even while the
   * draft is edited.
   */
  const loadCompose = async (
    repositoryId: string,
    targetPrefix: string | null,
  ) => {
    const requestId = composeRequest.current + 1;
    composeRequest.current = requestId;
    setComposeLoading(true);
    setComposeError(null);
    try {
      const grants = await readRepository(repositoryId);
      if (requestId !== composeRequest.current) return;
      setComposeBase({ repositoryId, grants });
      const existing =
        targetPrefix === null
          ? undefined
          : grants.find(
              (grant) =>
                grant.principal.trim() === principal &&
                (normalizePrefix(grant.resourcePrefix) ?? "") === targetPrefix,
            );
      // Editing keys on the entry the user acted on, but only while that
      // entry still exists; a row that vanished meanwhile composes a new
      // grant instead of targeting a different entry.
      setComposeTargetPrefix(existing ? targetPrefix : null);
      setDraft(
        existing
          ? { ...existing, scopes: [...existing.scopes] }
          : { ...emptyGrant(), principal },
      );
    } catch (caught) {
      if (requestId !== composeRequest.current) return;
      setComposeBase(null);
      setDraft(null);
      setComposeError(caught);
    } finally {
      if (requestId === composeRequest.current) setComposeLoading(false);
    }
  };

  const resetCompose = () => {
    composeRequest.current += 1;
    setComposeRepositoryId("");
    setComposeTargetPrefix(null);
    setComposeBase(null);
    setDraft(null);
    setComposeError(null);
    setSaveError(null);
  };

  const openCompose = () => {
    resetCompose();
    editor.show();
  };

  /** Edit one existing entry; that row's prefix becomes the edit target. */
  const openEdit = (record: RepositoryGrantRecord) => {
    const targetPrefix = normalizePrefix(record.resourcePrefix) ?? "";
    resetCompose();
    setComposeTargetPrefix(targetPrefix);
    setComposeRepositoryId(record.repositoryId);
    editor.show();
    void loadCompose(record.repositoryId, targetPrefix);
  };

  const closeCompose = () => {
    resetCompose();
    editor.hide();
  };

  const chooseRepository = (repositoryId: string) => {
    resetCompose();
    setComposeRepositoryId(repositoryId);
    void loadCompose(repositoryId, null);
  };

  const save = async () => {
    if (
      !draft ||
      !composeBase ||
      composeBase.repositoryId !== composeRepositoryId
    ) {
      setSaveError(
        new Error(
          text(
            "请先选择仓库并等待其授权加载完成",
            "Select a repository and wait for its grants to load",
          ),
        ),
      );
      return;
    }
    setSaving(true);
    setSaveError(null);
    try {
      const prefix = normalizePrefix(draft.resourcePrefix);
      const { error: requestError } = await upsertGrant({
        path: { repositoryId: composeRepositoryId },
        body: { principal, scopes: [...draft.scopes], resourcePrefix: prefix },
      });
      if (requestError) {
        setSaveError(requestError);
        return;
      }
      // An edit that changed the prefix moved the entry: the old prefix is a
      // different row and must go, or the edit would fork one grant into two.
      if (
        composeTargetPrefix !== null &&
        composeTargetPrefix !== (prefix ?? "")
      ) {
        const { error: deleteError } = await deleteGrant({
          path: { repositoryId: composeRepositoryId },
          query: {
            principal,
            resourcePrefix: composeTargetPrefix || undefined,
          },
        });
        if (deleteError && !isNotFound(deleteError)) {
          setSaveError(
            new Error(
              text(
                "新授权已保存，但原资源范围的授权删除失败，请手动移除旧行。",
                "The updated grant was saved, but removing the previous resource scope failed. Remove it manually.",
              ),
            ),
          );
          await load();
          return;
        }
      }
      void message.success(text("仓库授权已保存", "Repository grant saved"));
      closeCompose();
      await load();
    } finally {
      setSaving(false);
    }
  };

  const remove = async (record: RepositoryGrantRecord) => {
    // Deliberately bulk: this action is "remove this user's access to this
    // repository", so every entry of the principal goes, whatever its prefix.
    // Each row is deleted individually; a row already removed by someone else
    // is skipped, and other principals are never touched.
    setRemoving(recordKey(record));
    setRemoveError(null);
    try {
      const grants = await readRepository(record.repositoryId);
      for (const grant of grants.filter(
        (entry) => entry.principal.trim() === principal,
      )) {
        const { error: requestError } = await deleteGrant({
          path: { repositoryId: record.repositoryId },
          query: {
            principal: grant.principal,
            resourcePrefix: normalizePrefix(grant.resourcePrefix),
          },
        });
        if (requestError && !isNotFound(requestError)) throw requestError;
      }
      void message.success(text("仓库授权已移除", "Repository grant removed"));
      await load();
    } catch (caught) {
      setRemoveError(caught);
      await load();
    } finally {
      setRemoving("");
    }
  };

  const items = snapshot && snapshot.userId === userId ? snapshot.items : null;
  const rows = (items ?? []).filter((record) => record.principal === principal);
  const picks = useMemo(
    () =>
      repositories
        .filter((repository) => repository.state === "active")
        .map((repository) => ({
          value: repository.id,
          label: `${repository.name} · ${repository.format}`,
        })),
    [repositories],
  );
  const principalChoices = useMemo<PrincipalOption[]>(
    () => [
      {
        value: principal,
        label: `${text("用户", "User")} · ${username}`,
        detail: text(
          "本面板只管理该用户的仓库授权",
          "This panel only manages grants for this user",
        ),
      },
    ],
    [principal, text, username],
  );
  const selectedRepository = repositories.find(
    (repository) => repository.id === composeRepositoryId,
  );
  const baseReady =
    composeBase !== null && composeBase.repositoryId === composeRepositoryId;

  return (
    <section>
      <div className="mb-3 flex items-start justify-between gap-3">
        <div className="min-w-0">
          <h2 className="text-sm font-semibold text-zinc-100">
            {text("仓库授权", "Repository access")}
          </h2>
          <p className="mt-1 text-xs text-zinc-500">
            {text(
              "单独授予该用户仓库的读取、写入、管理或制品情报权限，可限定资源范围；全局角色不受影响。",
              "Grant this user read, write, admin, or artifact intelligence access to individual repositories, optionally limited to a resource scope. Global roles are unaffected.",
            )}
          </p>
        </div>
        <Space size={6}>
          <Tooltip title={text("刷新仓库授权", "Refresh repository grants")}>
            <Button
              size="small"
              icon={<ReloadOutlined />}
              loading={loading}
              aria-label={text("刷新仓库授权", "Refresh repository grants")}
              onClick={() => void load()}
            />
          </Tooltip>
          <Button
            size="small"
            type="primary"
            icon={<PlusOutlined />}
            disabled={repositoriesError !== null}
            onClick={openCompose}
          >
            {text("添加授权", "Add grant")}
          </Button>
        </Space>
      </div>

      {error !== null ? (
        <ErrorBanner error={error} onRetry={() => void load()} />
      ) : null}
      {repositoriesError !== null ? (
        <div className="mb-3">
          <Alert
            type="warning"
            showIcon
            title={text(
              "仓库列表暂时不可用，无法新增授权；已有授权仍可查看和移除。",
              "The repository list is unavailable, so new grants cannot be added. Existing grants can still be viewed and removed.",
            )}
          />
        </div>
      ) : null}
      {removeError !== null ? (
        <div className="mb-3">
          <ErrorBanner error={removeError} />
        </div>
      ) : null}

      {error === null && items === null ? <Loading /> : null}
      {error === null && items !== null && rows.length === 0 ? (
        <EmptyState
          title={text(
            "该用户暂无仓库授权",
            "This user has no repository grants",
          )}
          hint={text(
            "点击「添加授权」选择一个仓库并配置权限；授权会立即生效。",
            "Use Add grant to pick a repository and configure permissions. The grant takes effect immediately.",
          )}
        />
      ) : null}
      {rows.length > 0 ? (
        <div
          role="list"
          aria-busy={loading}
          className="divide-y divide-zinc-800/80 overflow-hidden rounded-md border border-zinc-800/80 bg-zinc-950/20"
        >
          {rows.map((record) => (
            <div
              key={recordKey(record)}
              role="listitem"
              className="flex items-start gap-3 px-3 py-2.5"
            >
              <div className="min-w-0 flex-1 py-1">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="truncate text-xs font-medium text-zinc-200">
                    {record.repositoryName}
                  </span>
                  <FormatBadge format={record.format} />
                  <Badge tone={grantTone(grantLevel(record.scopes))}>
                    {grantedCapabilitiesLabel(record.scopes, text)}
                  </Badge>
                </div>
                <div className="mt-1 font-mono text-xs text-zinc-500">
                  {record.resourcePrefix ||
                    text("整个仓库", "Entire repository")}
                </div>
              </div>
              <Space size={0}>
                <Tooltip title={text("编辑该条授权", "Edit this grant")}>
                  <Button
                    type="text"
                    size="small"
                    icon={<EditOutlined />}
                    aria-label={text("编辑该条授权", "Edit this grant")}
                    onClick={() => openEdit(record)}
                  />
                </Tooltip>
                <Popconfirm
                  title={text(
                    "移除该用户的仓库授权？",
                    "Remove this user's grant?",
                  )}
                  description={text(
                    `移除后，${username} 在「${record.repositoryName}」上的全部仓库授权都会被删除，其他用户不受影响。`,
                    `After removal, every repository grant for ${username} on "${record.repositoryName}" is deleted. Other users are unaffected.`,
                  )}
                  okText={text("移除", "Remove")}
                  cancelText={text("取消", "Cancel")}
                  okButtonProps={{ danger: true }}
                  onConfirm={() => void remove(record)}
                >
                  <Tooltip
                    title={text("移除仓库授权", "Remove repository grant")}
                  >
                    <Button
                      danger
                      type="text"
                      size="small"
                      icon={<DeleteOutlined />}
                      loading={removing === recordKey(record)}
                      aria-label={text(
                        "移除仓库授权",
                        "Remove repository grant",
                      )}
                    />
                  </Tooltip>
                </Popconfirm>
              </Space>
            </div>
          ))}
        </div>
      ) : null}

      <Modal
        open={editor.open}
        title={
          composeTargetPrefix === null
            ? text("添加仓库授权", "Add repository grant")
            : text("编辑仓库授权", "Edit repository grant")
        }
        onClose={closeCompose}
        wide
        footer={
          <Space>
            <Button onClick={closeCompose}>{text("取消", "Cancel")}</Button>
            <Button
              type="primary"
              loading={saving}
              disabled={!baseReady || draft === null}
              onClick={() => void save()}
            >
              {text("保存", "Save")}
            </Button>
          </Space>
        }
      >
        <div className="space-y-3">
          {composeError !== null ? (
            <ErrorBanner
              error={composeError}
              onRetry={() =>
                void loadCompose(composeRepositoryId, composeTargetPrefix)
              }
            />
          ) : null}
          {saveError !== null ? <ErrorBanner error={saveError} /> : null}
          <div>
            <div className="mb-1 text-xs font-medium text-zinc-500">
              {text("仓库", "Repository")}
            </div>
            <Select
              className="w-full"
              showSearch={{ optionFilterProp: "label" }}
              placeholder={text(
                "选择要授权的仓库",
                "Select a repository to grant",
              )}
              value={composeRepositoryId || undefined}
              loading={composeLoading}
              options={picks}
              onChange={(value: string) => chooseRepository(value)}
            />
          </div>
          {rolesError !== null ? (
            <Alert
              type="warning"
              showIcon
              title={text(
                "自定义角色暂时不可用，仍可使用内置权限。",
                "Custom roles are unavailable; built-in permissions still work.",
              )}
            />
          ) : null}
          {composeLoading ? <Loading /> : null}
          {!composeLoading && baseReady && draft !== null ? (
            <div>
              <Alert
                className="mb-3"
                type="info"
                showIcon
                title={text(
                  "仓库规则只会追加权限，不能撤销用户已有的全局角色。同一主体、同一资源范围的授权会被本条替换；该用户在其他资源范围的授权与其他主体的授权不受影响。",
                  "Repository rules add permissions; they cannot revoke the user's existing global role. A grant for the same principal and resource scope is replaced by this one; the user's other resource scopes and other principals keep theirs.",
                )}
              />
              <div className="grid grid-cols-[minmax(300px,1.35fr)_170px_minmax(360px,1.5fr)_170px_40px] items-center gap-3 px-2 pb-2 text-xs font-medium text-zinc-500">
                <span>{text("主体", "Principal")}</span>
                <span>{text("权限级别", "Permission")}</span>
                <span>{text("资源范围", "Resource scope")}</span>
                <span>{text("本规则授予", "Granted by this rule")}</span>
                <span />
              </div>
              <GrantRowEditor
                grant={draft}
                principalOptions={principalChoices}
                authorizationRoles={authorizationRoles}
                format={selectedRepository?.format ?? "raw"}
                onChange={(next) => setDraft({ ...next, principal })}
              />
            </div>
          ) : null}
          {!composeLoading && composeRepositoryId === "" ? (
            <Typography.Text type="secondary" className="block text-xs">
              {text(
                "选择仓库后配置该用户的权限级别与资源范围。",
                "Pick a repository, then configure this user's permission level and resource scope.",
              )}
            </Typography.Text>
          ) : null}
        </div>
      </Modal>
    </section>
  );
}
