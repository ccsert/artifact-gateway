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
  const { t, text } = usePreferences();
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
      void message.success(t("identity.repositoryGrantRemoved"));
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
            {t("identity.repositoryAccess")}
          </h2>
          <p className="mt-1 text-xs text-fg-tertiary">
            {t("identity.grantThisUserReadWriteAdminOrArtifactIntelligence")}
          </p>
        </div>
        <Space size={6}>
          <Tooltip title={t("identity.refreshRepositoryGrants")}>
            <Button
              size="small"
              icon={<ReloadOutlined />}
              loading={loading}
              aria-label={t("identity.refreshRepositoryGrants")}
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
            {t("identity.addGrant")}
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
          title={t("identity.theRepositoryListIsUnavailableSoNewGrantsCannot")}
        />
      ) : null}
      {removeError !== null ? <ErrorBanner error={removeError} /> : null}

      {error === null && items === null ? <Loading /> : null}
      {error === null && items !== null && rows.length === 0 ? (
        <EmptyState
          title={t("identity.thisUserHasNoRepositoryGrants")}
          hint={t("identity.useAddGrantToPickARepositoryAndConfigure")}
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
                  {record.resourcePrefix || t("identity.entireRepository")}
                </div>
              </div>
              <Space size={0}>
                <Tooltip title={t("identity.editThisGrant")}>
                  <Button
                    type="text"
                    size="small"
                    icon={<EditOutlined />}
                    aria-label={t("identity.editThisGrant")}
                    onClick={() =>
                      openEditor({
                        repositoryId: record.repositoryId,
                        prefix: normalizePrefix(record.resourcePrefix) ?? "",
                      })
                    }
                  />
                </Tooltip>
                <Popconfirm
                  title={t("identity.removeThisUserSGrant")}
                  description={t(
                    "identity.afterRemovalEveryRepositoryGrantForOnIsDeleted",
                    {
                      username: String(username),
                      repositoryName: String(record.repositoryName),
                    },
                  )}
                  okText={t("identity.remove")}
                  cancelText={t("identity.cancel")}
                  okButtonProps={{ danger: true }}
                  onConfirm={() => void remove(record)}
                >
                  <Tooltip title={t("identity.removeRepositoryGrant")}>
                    <Button
                      danger
                      type="text"
                      size="small"
                      icon={<DeleteOutlined />}
                      loading={removing === recordKey(record)}
                      aria-label={t("identity.removeRepositoryGrant")}
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
