import { useState } from "react";
import { SettingOutlined } from "@ant-design/icons";
import { Button, Input, Space, Switch } from "antd";
import { replaceGroup } from "../../client";
import type { Group } from "../../client";
import { Field } from "../../components/ui/Layout";
import { ErrorBanner } from "../../components/ui/Feedback";
import { Modal, useDisclosure } from "../../components/ui/Modal";
import { usePreferences } from "../../lib/preferences";

/** Rename a group and change its anonymous read access. */
export function RenameGroupDialog({
  group,
  onSaved,
}: {
  group: Group;
  onSaved: () => void;
}) {
  const { text } = usePreferences();
  const dialog = useDisclosure();
  const [name, setName] = useState(group.name);
  const [anonymousRead, setAnonymousRead] = useState(group.anonymousRead);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);

  const save = async () => {
    setBusy(true);
    setError(null);
    try {
      const { error: err } = await replaceGroup({
        path: { groupId: group.id },
        body: { ...group, name: name.trim(), anonymousRead },
        headers: { "If-Match": group.version },
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
      <Button
        size="small"
        icon={<SettingOutlined />}
        onClick={() => {
          setName(group.name);
          setAnonymousRead(group.anonymousRead);
          setError(null);
          dialog.show();
        }}
      >
        {text("设置", "Settings")}
      </Button>
      <Modal
        open={dialog.open}
        title={text(`设置分组：${group.name}`, `Group settings: ${group.name}`)}
        onClose={dialog.hide}
        busy={busy}
        footer={
          <Space>
            <Button onClick={dialog.hide} disabled={busy}>
              {text("取消", "Cancel")}
            </Button>
            <Button
              type="primary"
              onClick={save}
              loading={busy}
              disabled={!name.trim()}
            >
              {text("保存", "Save")}
            </Button>
          </Space>
        }
      >
        <div className="space-y-4">
          {error !== null && <ErrorBanner error={error} />}
          <Field label={text("分组名称", "Group name")}>
            <Input
              className="font-mono"
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
          </Field>
          <div className="flex items-center justify-between gap-6 rounded-lg border border-line bg-surface-translucent px-3 py-2.5">
            <div>
              <div className="text-sm font-medium text-fg">
                {text("允许匿名读取", "Allow anonymous reads")}
              </div>
              <div className="mt-0.5 text-xs text-fg-tertiary">
                {text(
                  "仍需成员 Repository 自身允许匿名读取。",
                  "Each member repository must also allow anonymous reads.",
                )}
              </div>
            </div>
            <Switch
              checked={anonymousRead}
              onChange={setAnonymousRead}
              aria-label={text(
                "允许分组匿名读取",
                "Allow anonymous group reads",
              )}
            />
          </div>
        </div>
      </Modal>
    </>
  );
}
