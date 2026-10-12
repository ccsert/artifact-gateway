import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Alert, App, Button, Popconfirm, Space } from "antd";
import type { ColumnsType } from "antd/es/table";
import { PlusOutlined } from "@ant-design/icons";
import { deleteGrant, upsertGrant } from "../../client";
import type { Grant, Repository } from "../../client";
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
import { accessKeys } from "../../lib/query/keys";
import { useRepositoryGrants } from "../access-control/accessQueries";
import { RepositoryFeatureUnavailable } from "./RepositoryFeatureUnavailable";
import { GrantForm } from "../access-control/GrantForm";
import {
  CUSTOM_PRINCIPAL,
  emptyGrant,
  grantedCapabilitiesLabel,
  grantLevel,
  grantRowKey,
  grantTone,
  type DraftGrant,
} from "../access-control/grantDraft";
import { ConsoleTable } from "../../components/ui/ConsolePrimitives";
import { useGrantPrincipalChoices } from "./useGrantPrincipalChoices";

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
  const { t, text } = usePreferences();
  const queryClient = useQueryClient();
  const grantsQuery = useRepositoryGrants(repo.id);
  const grants = grantsQuery.data ?? null;
  const error = grantsQuery.error;
  const [mutationError, setMutationError] = useState<unknown>(null);
  const { principalChoices, authorizationRoles, principalChoicesError } =
    useGrantPrincipalChoices();
  /** Show a repository's new grant set here and re-read the cross-repository list. */
  const setGrants = (next: (current: Grant[]) => Grant[]) => {
    queryClient.setQueryData<Grant[]>(
      accessKeys.repositoryGrants(repo.id),
      (current) => next(current ?? []),
    );
    void queryClient.invalidateQueries({ queryKey: accessKeys.grants() });
  };
  const load = () => grantsQuery.refetch();
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
        new Error(t("repositoryGrants.selectOrEnterAPrincipalForTheGrant")),
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
              t("repositoryGrants.thePreviousRowCouldNotBeRemovedSoThe"),
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
                t("repositoryGrants.thePreviousRowWasRemovedButSavingTheNew"),
              )
            : err,
        );
        return;
      }
      // The upsert answers with the repository's whole grant set, so the list
      // takes it directly; a second read would flicker over the result.
      if (data) setGrants(() => data);
      else void load();
      editor.hide();
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
      setGrants((current) => current.filter((item) => grantKey(item) !== key));
      message.success(t("repositoryGrants.grantRemoved"));
    } finally {
      setRemoving("");
    }
  };

  if (error !== null)
    return isNotFound(error) ? (
      <RepositoryFeatureUnavailable
        feature={t("repositoryGrants.accessGrants")}
      />
    ) : (
      <ErrorBanner error={error} onRetry={load} />
    );
  if (!grants) return <Loading />;

  // A write lands on one (principal, resource prefix) row. If that row already
  // belongs to someone else's edit target — that is, an existing row that is
  // not the one being edited — saving replaces it, so the dialog says so first.
  const conflictExists =
    draft !== null &&
    grants.some(
      (grant) =>
        grant.principal === draft.principal.trim() &&
        (grant.resourcePrefix ?? "") === (draft.resourcePrefix?.trim() ?? "") &&
        !(
          editingKey !== null &&
          editingKey.principal === grant.principal &&
          editingKey.resourcePrefix === (grant.resourcePrefix ?? "")
        ),
    );
  const principalMissing =
    draft === null ||
    !draft.principal.trim() ||
    draft.principal === CUSTOM_PRINCIPAL;

  const grantColumns: ColumnsType<Grant> = [
    {
      title: t("repositoryGrants.principal"),
      dataIndex: "principal",
      key: "principal",
      width: 320,
      render: (value: string) => (
        <div>
          <div className="font-mono text-xs text-fg">
            {principalChoices.find((choice) => choice.value === value)?.label ??
              value}
          </div>
          <div className="mt-0.5 font-mono text-xs text-fg-disabled">
            {value}
          </div>
        </div>
      ),
    },
    {
      title: t("repositoryGrants.grantedCapabilities"),
      key: "level",
      width: 260,
      render: (_, grant) => (
        <Badge tone={grantTone(grantLevel(grant.scopes))}>
          {grantedCapabilitiesLabel(grant.scopes, text)}
        </Badge>
      ),
    },
    {
      title: t("repositoryGrants.resourceScope"),
      dataIndex: "resourcePrefix",
      key: "resourcePrefix",
      width: 240,
      render: (value?: string) => (
        <span className="font-mono text-xs text-fg-tertiary">
          {value || t("repositoryGrants.entireRepository")}
        </span>
      ),
    },
    {
      title: t("repositoryGrants.actions"),
      key: "actions",
      fixed: "right",
      width: 130,
      render: (_, grant) => (
        <Space size="small">
          <Button type="link" size="small" onClick={() => openEdit(grant)}>
            {t("repositoryGrants.edit")}
          </Button>
          <Popconfirm
            title={t("repositoryGrants.removeThisGrant")}
            description={t(
              "repositoryGrants.onlyThisRowIsRemovedEveryOtherPrincipalKeeps",
            )}
            okText={t("repositoryGrants.remove")}
            cancelText={t("repositoryGrants.cancel")}
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
              aria-label={t("repositoryGrants.removeThisGrantRow")}
            >
              {t("repositoryGrants.remove")}
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
          {t("repositoryGrants.addGrant")}
        </Button>
      </div>
      {mutationError !== null && <ErrorBanner error={mutationError} />}
      {grants.length === 0 ? (
        <EmptyState
          title={t("repositoryGrants.noAccessGrants")}
          hint={t("repositoryGrants.chooseAUserApiKeyOrServiceAccountVia")}
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
            ? t("repositoryGrants.editGrant")
            : t("repositoryGrants.addGrant")
        }
        onClose={editor.hide}
        width={680}
        busy={saving}
        footer={
          <Space>
            <Button onClick={editor.hide} disabled={saving}>
              {t("repositoryGrants.cancel")}
            </Button>
            <Button
              type="primary"
              onClick={() => void save()}
              loading={saving}
              disabled={principalMissing}
            >
              {t("repositoryGrants.save")}
            </Button>
          </Space>
        }
      >
        <div className="space-y-3">
          {saveError !== null && <ErrorBanner error={saveError} />}
          {conflictExists && (
            <Alert
              type="warning"
              showIcon
              title={t(
                "repositoryGrants.thisPrincipalAlreadyHoldsAGrantAtThisResource",
              )}
            />
          )}
          <Notice
            tone="info"
            closable={false}
            title={t(
              "repositoryGrants.repositoryRulesAddPermissionsTheyCannotRevokeAnExisting",
            )}
          />
          {principalChoicesError !== null && (
            <Alert
              type="warning"
              showIcon
              title={t(
                "repositoryGrants.usersApiKeysOrServiceAccountsAreTemporarilyUnavailable",
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
