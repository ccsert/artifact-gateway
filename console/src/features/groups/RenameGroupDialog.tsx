import { useState } from "react";
import { Button, Input, Space, Switch } from "antd";
import { replaceGroup } from "../../client";
import type { Group } from "../../client";
import { Field } from "../../components/ui/Layout";
import { ErrorBanner } from "../../components/ui/Feedback";
import { Modal } from "../../components/ui/Modal";
import { usePreferences } from "../../lib/preferences";

/** Rename a group and change its anonymous read access. */
export function RenameGroupDialog({
  group,
  onClose,
  onSaved,
}: {
  group: Group;
  onClose: () => void;
  onSaved: () => void;
}) {
  const { t } = usePreferences();
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
        title={t("groups.groupSettings", { name: group.name })}
        onClose={onClose}
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
              disabled={!name.trim()}
            >
              {t("groups.save")}
            </Button>
          </Space>
        }
      >
        <div className="space-y-4">
          {error !== null && <ErrorBanner error={error} />}
          <Field label={t("groups.groupName")}>
            <Input
              className="font-mono"
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
          </Field>
          <div className="flex items-center justify-between gap-6 rounded-lg border border-line bg-surface-translucent px-3 py-2.5">
            <div>
              <div className="text-sm font-medium text-fg">
                {t("groups.allowAnonymousReads")}
              </div>
              <div className="mt-0.5 text-xs text-fg-tertiary">
                {t("groups.eachMemberRepositoryMustAlsoAllowAnonymousReads")}
              </div>
            </div>
            <Switch
              checked={anonymousRead}
              onChange={setAnonymousRead}
              aria-label={t("groups.allowAnonymousGroupReads")}
            />
          </div>
        </div>
      </Modal>
    </>
  );
}
