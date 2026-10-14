import { useEffect, useState } from "react";
import { Button, Space } from "antd";
import { listGroupMembers, replaceGroupMembers } from "../../client";
import type { Group, Member, Repository } from "../../client";
import { ErrorBanner } from "../../components/ui/Feedback";
import { Modal } from "../../components/ui/Modal";
import { MemberOrderPicker } from "./MemberOrderPicker";
import { usePreferences } from "../../lib/preferences";

/**
 * Choose and order a group's member repositories. Mounted while open: it
 * reads the current members and their version when it appears.
 */
export function MembersDialog({
  group,
  repos,
  onClose,
  onSaved,
}: {
  group: Group;
  repos: Repository[];
  onClose: () => void;
  onSaved: () => void;
}) {
  const { t } = usePreferences();
  const [memberIds, setMemberIds] = useState<string[]>([]);
  const [version, setVersion] = useState(group.version);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);

  const candidates = repos.filter(
    (r) => r.format === group.format && r.state === "active",
  );

  useEffect(() => {
    let current = true;
    void listGroupMembers({ path: { groupId: group.id } }).then(
      ({ data, response }) => {
        if (!current) return;
        if (data) {
          const sorted = [...data].sort((a, b) => a.position - b.position);
          setMemberIds(sorted.map((m) => m.repositoryId));
        }
        const etag = response?.headers.get("ETag");
        if (etag) setVersion(etag.replaceAll('"', ""));
      },
    );
    return () => {
      current = false;
    };
  }, [group.id]);

  const save = async () => {
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
        open
        title={t("groups.editMembers2", { name: group.name })}
        onClose={onClose}
        busy={busy}
        footer={
          <Space>
            <Button onClick={onClose} disabled={busy}>
              {t("groups.cancel")}
            </Button>
            <Button type="primary" onClick={save} loading={busy}>
              {t("groups.save")}
            </Button>
          </Space>
        }
      >
        <div className="space-y-3">
          {error !== null && <ErrorBanner error={error} />}
          <p className="text-xs text-fg-tertiary">
            {t("groups.setTheOrderWithinEachTypeHostedMembers")}
          </p>
          <MemberOrderPicker
            candidates={candidates}
            memberIds={memberIds}
            onChange={setMemberIds}
          />
        </div>
      </Modal>
    </>
  );
}
