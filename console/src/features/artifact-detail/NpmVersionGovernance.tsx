import { useState } from "react";
import { Alert, Button, Input, Popconfirm, Space } from "antd";
import { ErrorBanner } from "../../components/ui/Feedback";
import { Modal } from "../../components/ui/Modal";
import { useAuth } from "../../lib/auth";
import { usePreferences } from "../../lib/preferences";
import { LocalizedError } from "../../messages/translate";
import type { NpmPackument } from "./protocolDetailQueries";

export function NpmVersionGovernance({
  repoName,
  packument,
  version,
  canWrite,
  canUnpublish,
  onChanged,
  onUnpublished,
}: {
  repoName: string;
  packument: NpmPackument;
  version: string;
  canWrite: boolean;
  canUnpublish: boolean;
  onChanged: () => Promise<unknown>;
  onUnpublished?: () => void;
}) {
  const { t } = usePreferences();
  const { token } = useAuth();
  const deprecated = packument.versions[version]?.deprecated ?? "";
  const [editing, setEditing] = useState(false);
  const [editSnapshot, setEditSnapshot] = useState<{
    repoName: string;
    packument: NpmPackument;
    version: string;
  } | null>(null);
  const [draft, setDraft] = useState(deprecated);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const hosted = Boolean(packument._rev);

  const mutate = async (unpublish: boolean) => {
    setSaving(true);
    setError(null);
    try {
      const source = unpublish
        ? { repoName, packument, version }
        : editSnapshot;
      if (source === null)
        throw new LocalizedError("artifactDetail.npmGovernanceUnknown");
      const document = structuredClone(source.packument);
      let url = `/npm/${encodeURIComponent(source.repoName)}/${encodeURIComponent(document.name)}`;
      if (unpublish) {
        url += `/-rev/${document._rev}`;
        delete document.versions[source.version];
        for (const [tag, target] of Object.entries(document["dist-tags"])) {
          if (target === source.version) delete document["dist-tags"][tag];
        }
      } else {
        document.versions[source.version] = {
          ...document.versions[source.version],
          deprecated: draft,
        };
      }
      const headers = {
        "Content-Type": "application/json",
        ...(token ? { Authorization: `Bearer ${token}` } : {}),
      };
      const response = await (unpublish &&
      Object.keys(document.versions).length === 0
        ? fetch(url, {
            method: "DELETE",
            credentials: "include",
            headers,
          })
        : fetch(url, {
            method: "PUT",
            credentials: "include",
            headers,
            body: JSON.stringify(document),
          }));
      if (!response.ok)
        throw new LocalizedError(
          "artifactDetail.npmGovernanceFailed",
          { status: response.status },
          { status: response.status },
        );
      let result: unknown;
      try {
        result = await response.json();
      } catch {
        throw new LocalizedError("artifactDetail.npmGovernanceUnknown");
      }
      if (
        typeof result !== "object" ||
        result === null ||
        !("ok" in result) ||
        result.ok !== true ||
        !("rev" in result) ||
        typeof result.rev !== "string" ||
        !/^[1-9][0-9]*$/.test(result.rev)
      )
        throw new LocalizedError("artifactDetail.npmGovernanceUnknown");
      setEditing(false);
      if (unpublish) onUnpublished?.();
      await onChanged();
    } catch (failure) {
      setError(failure);
    } finally {
      setSaving(false);
    }
  };

  if (!deprecated && !(hosted && (canWrite || canUnpublish))) return null;
  const draftTooLong = new TextEncoder().encode(draft).length > 4096;
  return (
    <div
      className="ag-page-stack xl:col-span-2"
      data-testid="npm-version-governance"
    >
      {deprecated && (
        <Alert
          type="warning"
          showIcon
          title={t("artifactDetail.npmDeprecated")}
          description={
            <span className="whitespace-pre-wrap break-words [overflow-wrap:anywhere]">
              {deprecated}
            </span>
          }
        />
      )}
      {!editing && error !== null && (
        <ErrorBanner error={error} onRetry={() => void onChanged()} />
      )}
      {hosted && (canWrite || canUnpublish) && (
        <Space wrap>
          {canWrite && (
            <Button
              size="small"
              disabled={saving}
              onClick={() => {
                setDraft(deprecated);
                setEditSnapshot({
                  repoName,
                  packument: structuredClone(packument),
                  version,
                });
                setError(null);
                setEditing(true);
              }}
            >
              {t("artifactDetail.npmEditDeprecation")}
            </Button>
          )}
          {canUnpublish && (
            <Popconfirm
              title={t("artifactDetail.npmUnpublishVersion", { version })}
              description={t("artifactDetail.npmUnpublishExplanation")}
              okText={t("artifactDetail.npmUnpublish")}
              cancelText={t("ui.cancel")}
              onConfirm={() => mutate(true)}
              disabled={saving}
            >
              <Button size="small" danger loading={saving} disabled={saving}>
                {t("artifactDetail.npmUnpublish")}
              </Button>
            </Popconfirm>
          )}
        </Space>
      )}
      <Modal
        open={editing}
        title={t("artifactDetail.npmEditDeprecation")}
        onClose={() => setEditing(false)}
        busy={saving}
        footer={
          <Space>
            <Button disabled={saving} onClick={() => setEditing(false)}>
              {t("ui.cancel")}
            </Button>
            <Button
              type="primary"
              loading={saving}
              disabled={draftTooLong}
              onClick={() => void mutate(false)}
            >
              {t("artifactDetail.npmSaveDeprecation")}
            </Button>
          </Space>
        }
      >
        <div className="ag-page-stack">
          {error !== null && (
            <ErrorBanner error={error} onRetry={() => void onChanged()} />
          )}
          <label className="grid gap-2">
            <span>
              {t("artifactDetail.npmDeprecationMessage", {
                version: editSnapshot?.version ?? version,
              })}
            </span>
            <Input.TextArea
              value={draft}
              onChange={(event) => setDraft(event.target.value)}
              maxLength={4096}
              autoSize={{ minRows: 3, maxRows: 8 }}
              disabled={saving}
            />
          </label>
          <p className="text-sm text-fg-tertiary">
            {t("artifactDetail.npmDeprecationExplanation")}
          </p>
        </div>
      </Modal>
    </div>
  );
}
