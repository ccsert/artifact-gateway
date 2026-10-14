import { useState } from "react";
import { Button, Space } from "antd";
import { replaceGroupMembers } from "../../client";
import type { Group, Member, Repository } from "../../client";
import { ErrorBanner, Loading } from "../../components/ui/Feedback";
import { Modal } from "../../components/ui/Modal";
import { MemberOrderPicker } from "./MemberOrderPicker";
import { usePreferences } from "../../lib/preferences";
import { useGroupMembers } from "./groupQueries";

/**
 * Choose and order a group's member repositories. Each opening reads the
 * current members and their version before the editor becomes available.
 */
export function MembersDialog({
  group,
  open,
  repos,
  onClose,
  afterClose,
  onSaved,
}: {
  group: Group;
  open: boolean;
  repos: Repository[];
  onClose: () => void;
  afterClose: () => void;
  onSaved: () => void;
}) {
  const { t } = usePreferences();
  const membersQuery = useGroupMembers(group.id, open);
  const [draft, setDraft] = useState<{
    memberIds: string[];
    version: string;
  } | null>(null);
  const memberIds =
    draft?.memberIds ??
    [...(membersQuery.data?.members ?? [])]
      .sort((a, b) => a.position - b.position)
      .map((member) => member.repositoryId);
  const version = draft?.version ?? membersQuery.data?.version ?? group.version;
  const ready = membersQuery.isSuccess && !membersQuery.isFetching;
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);

  const candidates = repos.filter(
    (r) => r.format === group.format && r.state === "active",
  );

  const save = async () => {
    if (busy || !ready || !open) return;
    setBusy(true);
    setError(null);
    const members: Member[] = memberIds.map((repositoryId, position) => ({
      repositoryId,
      position,
    }));
    try {
      const { error: err } = await replaceGroupMembers({
        path: { groupId: group.id },
        body: members,
        headers: { "If-Match": version },
      });
      if (err) {
        setError(err);
        return;
      }
      onClose();
      onSaved();
    } catch (nextError) {
      setError(nextError);
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <Modal
        open={open}
        title={t("groups.editMembers2", { name: group.name })}
        onClose={onClose}
        afterClose={afterClose}
        busy={busy}
        footer={
          <Space>
            <Button onClick={onClose} disabled={busy}>
              {t("groups.cancel")}
            </Button>
            <Button
              type="primary"
              onClick={save}
              loading={busy}
              disabled={!ready}
            >
              {t("groups.save")}
            </Button>
          </Space>
        }
      >
        <div className="space-y-3">
          {error !== null && <ErrorBanner error={error} />}
          {membersQuery.error !== null && (
            <ErrorBanner
              error={membersQuery.error}
              onRetry={() => membersQuery.refetch()}
            />
          )}
          <p className="text-xs text-fg-tertiary">
            {t("groups.setTheOrderWithinEachTypeHostedMembers")}
          </p>
          {membersQuery.isFetching ? (
            <Loading />
          ) : ready ? (
            <MemberOrderPicker
              candidates={candidates}
              memberIds={memberIds}
              onChange={(memberIds) => setDraft({ memberIds, version })}
            />
          ) : null}
        </div>
      </Modal>
    </>
  );
}
