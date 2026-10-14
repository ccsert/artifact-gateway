import { useState } from "react";
import { PlusOutlined } from "@ant-design/icons";
import { Button, Input, Select, Space, Switch } from "antd";
import { createGroup } from "../../client";
import type { Format, FormatProfile, Member, Repository } from "../../client";
import { Field } from "../../components/ui/Layout";
import { ErrorBanner } from "../../components/ui/Feedback";
import { Modal, useDisclosure } from "../../components/ui/Modal";
import { MemberOrderPicker } from "./MemberOrderPicker";
import { usePreferences } from "../../lib/preferences";
import { groupFormats } from "../../lib/formatProfiles";

/** Create a group of repositories of one format. */
export function CreateGroupDialog({
  repos,
  profiles,
  onCreated,
}: {
  repos: Repository[];
  profiles: FormatProfile[];
  onCreated: () => void;
}) {
  const { t } = usePreferences();
  const dialog = useDisclosure();
  const [name, setName] = useState("");
  const [format, setFormat] = useState<Format>("oci");
  const [memberIds, setMemberIds] = useState<string[]>([]);
  const [anonymousRead, setAnonymousRead] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);

  const formats = groupFormats(profiles);
  const selectedFormat = formats.includes(format)
    ? format
    : (formats[0] ?? format);

  const candidates = repos.filter(
    (r) => r.format === selectedFormat && r.state === "active",
  );

  const submit = async () => {
    setBusy(true);
    setError(null);
    const members: Member[] = memberIds.map((repositoryId, position) => ({
      repositoryId,
      position,
    }));
    try {
      const { error: err } = await createGroup({
        body: {
          name: name.trim(),
          format: selectedFormat,
          anonymousRead,
          members,
        },
        headers: { "Idempotency-Key": crypto.randomUUID() },
      });
      if (err) {
        setError(err);
        return;
      }
      dialog.hide();
      setName("");
      setMemberIds([]);
      setAnonymousRead(false);
      onCreated();
    } catch (nextError) {
      setError(nextError);
    } finally {
      setBusy(false);
    }
  };

  return (
    <>
      <Button
        type="primary"
        icon={<PlusOutlined />}
        disabled={formats.length === 0}
        onClick={() => {
          setError(null);
          dialog.show();
        }}
      >
        {t("groups.newGroup")}
      </Button>
      <Modal
        open={dialog.open}
        title={t("groups.newGroup")}
        onClose={dialog.hide}
        busy={busy}
        footer={
          <Space>
            <Button onClick={dialog.hide} disabled={busy}>
              {t("groups.cancel")}
            </Button>
            <Button
              type="primary"
              onClick={submit}
              loading={busy}
              disabled={
                busy ||
                formats.length === 0 ||
                !name.trim() ||
                memberIds.length === 0
              }
            >
              {t("groups.create")}
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
          <Field label={t("groups.format")} group>
            <Select<Format>
              className="w-full font-mono"
              showSearch={{ optionFilterProp: "label" }}
              value={selectedFormat}
              options={formats.map((candidate) => ({
                value: candidate,
                label: candidate,
              }))}
              onChange={(nextFormat) => {
                setFormat(nextFormat);
                setMemberIds([]);
              }}
            />
          </Field>
          <Field
            label={t("groups.memberRepositories")}
            hint={t("groups.hostedMembersComeFirstMembersOfTheSame")}
            group
          >
            {candidates.length === 0 ? (
              <div className="rounded-lg border border-line px-2 py-3 text-center text-xs text-fg-disabled">
                {t("groups.noActiveRepositoriesForThisFormat")}
              </div>
            ) : (
              <MemberOrderPicker
                candidates={candidates}
                memberIds={memberIds}
                onChange={setMemberIds}
              />
            )}
          </Field>
          <div className="flex items-center justify-between gap-6 rounded-lg border border-line bg-surface-translucent px-3 py-2.5">
            <div>
              <div className="text-sm font-medium text-fg">
                {t("groups.allowAnonymousReads")}
              </div>
              <div className="mt-0.5 text-xs text-fg-tertiary">
                {t("groups.anonymousRequestsResolveAMemberOnlyWhenBoth")}
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
