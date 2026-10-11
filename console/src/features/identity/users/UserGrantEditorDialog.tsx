import { useMemo, useState } from "react";
import { Alert, App, Button, Select, Space, Typography } from "antd";
import { deleteGrant, upsertGrant } from "../../../client";
import type { AuthorizationRole, Repository } from "../../../client";
import {
  ErrorBanner,
  Loading,
  isNotFound,
} from "../../../components/ui/Feedback";
import { Modal } from "../../../components/ui/Modal";
import { usePreferences } from "../../../lib/preferences";
import { accessKeys } from "../../../lib/query/keys";
import { GrantForm } from "../../access-control/GrantForm";
import {
  useInvalidateAccess,
  useRepositoryGrants,
} from "../../access-control/accessQueries";
import {
  emptyGrant,
  type DraftGrant,
  type PrincipalOption,
} from "../../access-control/grantDraft";

/** A blank prefix means repository-wide access, submitted as `undefined`. */
export function normalizePrefix(
  prefix: string | undefined,
): string | undefined {
  return prefix?.trim() || undefined;
}

/** The entry an editor opens on: a repository and the normalized prefix. */
export interface GrantTarget {
  repositoryId: string;
  /** `""` is the repository-wide entry. */
  prefix: string;
}

/**
 * Composes or edits one of the user's grants. Mount it with a new `key` per
 * opening so the draft starts from `target` every time.
 *
 * The chosen repository's grant list is read fresh and keyed by repository,
 * so switching repositories can never prefill from a slower answer for the
 * previous one.
 */
export function UserGrantEditorDialog({
  open,
  username,
  target,
  repositories,
  authorizationRoles,
  rolesUnavailable,
  onClose,
}: {
  open: boolean;
  username: string;
  target: GrantTarget | null;
  repositories: Repository[];
  authorizationRoles: AuthorizationRole[];
  rolesUnavailable: boolean;
  onClose: () => void;
}) {
  const { message } = App.useApp();
  const { t } = usePreferences();
  const invalidate = useInvalidateAccess();
  const principal = `user:${username}`;
  const [composeRepositoryId, setComposeRepositoryId] = useState(
    target?.repositoryId ?? "",
  );
  /**
   * The normalized prefix of the entry the editor acts on, or `null` when
   * composing a brand-new grant. `""` means the repository-wide entry, so the
   * target is a plain string and cannot be confused with "no target".
   */
  const [requestedPrefix, setRequestedPrefix] = useState<string | null>(
    target?.prefix ?? null,
  );
  const [edited, setEdited] = useState<DraftGrant | null>(null);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<unknown>(null);
  const base = useRepositoryGrants(composeRepositoryId);
  const composeLoading = base.isFetching;
  const baseGrants = useMemo(
    () => (composeLoading ? null : (base.data ?? null)),
    [base.data, composeLoading],
  );

  // Editing keys on the entry the user acted on, but only while that entry
  // still exists; a row that vanished meanwhile composes a new grant instead
  // of targeting a different entry.
  const existing =
    requestedPrefix === null
      ? undefined
      : baseGrants?.find(
          (grant) =>
            grant.principal.trim() === principal &&
            (normalizePrefix(grant.resourcePrefix) ?? "") === requestedPrefix,
        );
  const composeTargetPrefix = existing ? requestedPrefix : null;
  const initialDraft = useMemo<DraftGrant | null>(() => {
    if (!baseGrants) return null;
    return existing
      ? { ...existing, scopes: [...existing.scopes] }
      : { ...emptyGrant(), principal };
  }, [baseGrants, existing, principal]);
  const draft = edited ?? initialDraft;
  const baseReady = baseGrants !== null;

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
        label: `${t("identity.user")} · ${username}`,
        detail: t("identity.thisPanelOnlyManagesGrantsForThisUser"),
      },
    ],
    [principal, t, username],
  );
  // Writing a (principal, resource scope) pair that another existing row holds
  // replaces that row, so the dialog warns before the save instead of after.
  const conflictExists =
    draft !== null &&
    (baseGrants ?? []).some(
      (grant) =>
        grant.principal.trim() === principal &&
        (normalizePrefix(grant.resourcePrefix) ?? "") ===
          (normalizePrefix(draft.resourcePrefix) ?? "") &&
        (composeTargetPrefix === null ||
          (normalizePrefix(grant.resourcePrefix) ?? "") !==
            composeTargetPrefix),
    );
  const selectedRepository = repositories.find(
    (repository) => repository.id === composeRepositoryId,
  );

  const chooseRepository = (repositoryId: string) => {
    setComposeRepositoryId(repositoryId);
    setRequestedPrefix(null);
    setEdited(null);
    setSaveError(null);
  };

  const save = async () => {
    if (!draft || !baseReady) {
      setSaveError(
        new Error(t("identity.selectARepositoryAndWaitForItsGrantsTo")),
      );
      return;
    }
    setSaving(true);
    setSaveError(null);
    try {
      const prefix = normalizePrefix(draft.resourcePrefix);
      // An edit that changed the prefix moved the entry: the old prefix is a
      // different row and must go, or the edit would fork one grant into two.
      // It goes first, so a failure here leaves the grant narrower rather than
      // wider than the administrator asked for.
      const moved =
        composeTargetPrefix !== null && composeTargetPrefix !== (prefix ?? "");
      if (moved) {
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
              t("identity.thePreviousResourceScopeCouldNotBeRemovedSo"),
            ),
          );
          return;
        }
      }
      const { error: requestError } = await upsertGrant({
        path: { repositoryId: composeRepositoryId },
        body: { principal, scopes: [...draft.scopes], resourcePrefix: prefix },
      });
      if (requestError) {
        setSaveError(
          moved
            ? new Error(t("identity.thePreviousRowWasRemovedButSavingTheNew"))
            : requestError,
        );
        return;
      }
      void message.success(t("identity.repositoryGrantSaved"));
      onClose();
      await invalidate(accessKeys.grants());
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal
      open={open}
      title={
        (baseReady ? composeTargetPrefix : requestedPrefix) === null
          ? t("identity.addRepositoryGrant")
          : t("identity.editRepositoryGrant")
      }
      onClose={onClose}
      width={680}
      busy={saving}
      footer={
        <Space>
          <Button onClick={onClose} disabled={saving}>
            {t("identity.cancel")}
          </Button>
          <Button
            type="primary"
            loading={saving}
            disabled={!baseReady || draft === null}
            onClick={() => void save()}
          >
            {t("identity.save")}
          </Button>
        </Space>
      }
    >
      <div className="space-y-3">
        {base.error !== null && !composeLoading ? (
          <ErrorBanner error={base.error} onRetry={() => void base.refetch()} />
        ) : null}
        {saveError !== null ? <ErrorBanner error={saveError} /> : null}
        <div>
          <div className="mb-1 text-xs font-medium text-fg-tertiary">
            {t("identity.repository")}
          </div>
          <Select
            className="w-full"
            showSearch={{ optionFilterProp: "label" }}
            placeholder={t("identity.selectARepositoryToGrant")}
            value={composeRepositoryId || undefined}
            loading={composeLoading}
            options={picks}
            onChange={(value: string) => chooseRepository(value)}
          />
        </div>
        {rolesUnavailable ? (
          <Alert
            type="warning"
            showIcon
            title={t(
              "identity.customRolesAreUnavailableBuiltInPermissionsStillWork",
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
              title={t(
                "identity.repositoryRulesAddPermissionsTheyCannotRevokeTheUser",
              )}
            />
            {conflictExists && (
              <Alert
                className="mb-3"
                type="warning"
                showIcon
                title={t("identity.thisUserAlreadyHoldsAGrantAtThisResource")}
              />
            )}
            <GrantForm
              grant={draft}
              principalOptions={principalChoices}
              authorizationRoles={authorizationRoles}
              format={selectedRepository?.format ?? "raw"}
              onChange={(next) => setEdited({ ...next, principal })}
              lockPrincipal
            />
          </div>
        ) : null}
        {!composeLoading && composeRepositoryId === "" ? (
          <Typography.Text type="secondary" className="block text-xs">
            {t("identity.pickARepositoryThenConfigureThisUserSPermission")}
          </Typography.Text>
        ) : null}
      </div>
    </Modal>
  );
}
