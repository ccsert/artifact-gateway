import { useState } from "react";
import { EditOutlined } from "@ant-design/icons";
import { Button, Space } from "antd";
import { listGroupMembers, replaceGroupMembers } from "../../client";
import type { Group, Member, Repository } from "../../client";
import { ErrorBanner } from "../../components/ui/Feedback";
import { Modal, useDisclosure } from "../../components/ui/Modal";
import { MemberOrderPicker } from "./MemberOrderPicker";
import { usePreferences } from "../../lib/preferences";

/** Choose and order a group's member repositories. */
export function MembersDialog({
  group,
  repos,
  onSaved,
}: {
  group: Group;
  repos: Repository[];
  onSaved: () => void;
}) {
  const { text } = usePreferences();
  const dialog = useDisclosure();
  const [memberIds, setMemberIds] = useState<string[]>([]);
  const [version, setVersion] = useState(group.version);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);

  const candidates = repos.filter(
    (r) => r.format === group.format && r.state === "active",
  );

  const open = async () => {
    setError(null);
    dialog.show();
    const { data, response } = await listGroupMembers({
      path: { groupId: group.id },
    });
    if (data) {
      const sorted = [...data].sort((a, b) => a.position - b.position);
      setMemberIds(sorted.map((m) => m.repositoryId));
    }
    const etag = response?.headers.get("ETag");
    if (etag) setVersion(etag.replaceAll('"', ""));
  };

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
      dialog.hide();
      onSaved();
    } catch (nextError) {
      setError(nextError);
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <Button size="small" icon={<EditOutlined />} onClick={open}>
        {text("编辑成员", "Edit members")}
      </Button>
      <Modal
        open={dialog.open}
        title={text(`编辑成员：${group.name}`, `Edit members: ${group.name}`)}
        onClose={dialog.hide}
        busy={busy}
        footer={
          <Space>
            <Button onClick={dialog.hide} disabled={busy}>
              {text("取消", "Cancel")}
            </Button>
            <Button type="primary" onClick={save} loading={busy}>
              {text("保存", "Save")}
            </Button>
          </Space>
        }
      >
        <div className="space-y-3">
          {error !== null && <ErrorBanner error={error} />}
          <p className="text-xs text-fg-tertiary">
            {text(
              "配置顺序在同类型成员内生效；Hosted 始终优先于 Proxy。",
              "Set the order within each type; Hosted members are tried before Proxy members.",
            )}
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
