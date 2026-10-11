import { useState } from "react";
import {
  DeleteOutlined,
  EditOutlined,
  PlusOutlined,
  ReloadOutlined,
} from "@ant-design/icons";
import { Alert, App, Button, Popconfirm, Space, Tooltip } from "antd";
import { deleteGrant, listGrants } from "../../../client";
import type { RepositoryGrantRecord } from "../../../client";
import { Badge, FormatBadge } from "../../../components/ui/Badge";
import {
  EmptyState,
  ErrorBanner,
  Loading,
  isNotFound,
} from "../../../components/ui/Feedback";
import { usePreferences } from "../../../lib/preferences";
import { accessKeys } from "../../../lib/query/keys";
import { unwrap } from "../../../lib/query/client";
import {
  useAccessRepositories,
  useAuthorizationRoles,
  useInvalidateAccess,
  useRepositoryGrantRecords,
} from "../../access-control/accessQueries";
import {
  grantedCapabilitiesLabel,
  grantLevel,
  grantRowKey,
  grantTone,
} from "../../access-control/grantDraft";
import {
  UserGrantEditorDialog,
  normalizePrefix,
  type GrantTarget,
} from "./UserGrantEditorDialog";

interface UserRepositoryAccessPanelProps {
  username: string;
}

function recordKey(record: RepositoryGrantRecord): string {
  return grantRowKey(record.repositoryId, record.resourcePrefix);
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
  username,
}: UserRepositoryAccessPanelProps) {
  const { message } = App.useApp();
  const { text } = usePreferences();
  const principal = `user:${username}`;
  const invalidate = useInvalidateAccess();
  const grantsQuery = useRepositoryGrantRecords();
  const repositoriesQuery = useAccessRepositories();
  const rolesQuery = useAuthorizationRoles();
  const loading = grantsQuery.isFetching;
  const error = grantsQuery.error;
  const repositoriesError = repositoriesQuery.error;
  const repositories = [...(repositoriesQuery.data?.items ?? [])].sort((a, b) =>
    a.name.localeCompare(b.name),
  );
  const [removing, setRemoving] = useState("");
  const [removeError, setRemoveError] = useState<unknown>(null);
  // Each opening gets a new session so the dialog starts from its target
  // while a closing dialog keeps its content through the exit animation.
  const [editor, setEditor] = useState<{
    session: number;
    open: boolean;
    target: GrantTarget | null;
  } | null>(null);

  const openEditor = (target: GrantTarget | null) =>
    setEditor((current) => ({
      session: (current?.session ?? 0) + 1,
      open: true,
      target,
    }));

  const load = async () => {
    await Promise.all([
      invalidate(accessKeys.grants()),
      invalidate(accessKeys.directory("repositories")),
      invalidate(accessKeys.roles()),
    ]);
  };

  /** Read one repository's current grants right before acting on them. */
  const readRepository = (repositoryId: string) =>
    unwrap(listGrants({ path: { repositoryId } }));

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

  const items = grantsQuery.data ?? null;
  const rows = (items ?? []).filter((record) => record.principal === principal);

  return (
    <section className="space-y-3">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <h2 className="text-sm font-semibold text-fg-strong">
            {text("仓库授权", "Repository access")}
          </h2>
          <p className="mt-1 text-xs text-fg-tertiary">
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
            onClick={() => openEditor(null)}
          >
            {text("添加授权", "Add grant")}
          </Button>
        </Space>
      </div>

      {error !== null ? (
        <ErrorBanner error={error} onRetry={() => void load()} />
      ) : null}
      {repositoriesError !== null ? (
        <Alert
          type="warning"
          showIcon
          title={text(
            "仓库列表暂时不可用，无法新增授权；已有授权仍可查看和移除。",
            "The repository list is unavailable, so new grants cannot be added. Existing grants can still be viewed and removed.",
          )}
        />
      ) : null}
      {removeError !== null ? <ErrorBanner error={removeError} /> : null}

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
          className="divide-y divide-line overflow-hidden rounded-md border border-line bg-surface-translucent"
        >
          {rows.map((record) => (
            <div
              key={recordKey(record)}
              role="listitem"
              className="flex items-start gap-3 px-3 py-2.5"
            >
              <div className="min-w-0 flex-1 py-1">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="truncate text-xs font-medium text-fg">
                    {record.repositoryName}
                  </span>
                  <FormatBadge format={record.format} />
                  <Badge tone={grantTone(grantLevel(record.scopes))}>
                    {grantedCapabilitiesLabel(record.scopes, text)}
                  </Badge>
                </div>
                <div className="mt-1 font-mono text-xs text-fg-tertiary">
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
                    onClick={() =>
                      openEditor({
                        repositoryId: record.repositoryId,
                        prefix: normalizePrefix(record.resourcePrefix) ?? "",
                      })
                    }
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

      {editor && (
        <UserGrantEditorDialog
          key={editor.session}
          open={editor.open}
          username={username}
          target={editor.target}
          repositories={repositories}
          authorizationRoles={rolesQuery.data ?? []}
          rolesUnavailable={rolesQuery.error !== null}
          onClose={() =>
            setEditor((current) => current && { ...current, open: false })
          }
        />
      )}
    </section>
  );
}
