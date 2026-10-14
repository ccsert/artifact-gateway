import { useState } from "react";
import { SafetyCertificateOutlined, UnlockOutlined } from "@ant-design/icons";
import { Alert, Button, Input, Space } from "antd";
import { replaceArtifactQuarantine } from "../../client";
import type { ArtifactQuarantine } from "../../client";
import { formatDate } from "../../lib/format";
import { usePreferences } from "../../lib/preferences";
import { ErrorBanner } from "../../components/ui/Feedback";
import { Modal } from "../../components/ui/Modal";
import { useQueryClient } from "@tanstack/react-query";
import {
  artifactQuarantineKey,
  useArtifactQuarantine,
} from "./artifactDetailQueries";

type QuarantineState = ArtifactQuarantine["state"];

type QuarantinePanelProps = {
  repositoryId?: string;
  coordinate: string;
  digest?: string;
  canManage?: boolean;
  label?: string;
};

export function ArtifactQuarantinePanel(props: QuarantinePanelProps) {
  // A transition draft belongs to one artifact; another one starts afresh.
  return (
    <QuarantinePanel
      key={`${props.repositoryId}:${props.coordinate}:${props.digest}`}
      {...props}
    />
  );
}

function QuarantinePanel({
  repositoryId,
  coordinate,
  digest,
  canManage = false,
  label,
}: QuarantinePanelProps) {
  const { locale, t } = usePreferences();
  const queryClient = useQueryClient();
  const quarantine = useArtifactQuarantine(repositoryId, coordinate, digest);
  const record = quarantine.data ?? null;
  const loading = quarantine.isFetching;
  const loadError = quarantine.error;
  const [nextState, setNextState] = useState<QuarantineState | null>(null);
  const [reason, setReason] = useState("");
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<unknown>(null);
  const load = () => quarantine.refetch();

  if (!repositoryId || !coordinate || !digest) return null;

  const openTransition = (state: QuarantineState) => {
    setNextState(state);
    setReason("");
    setSaveError(null);
  };

  const closeTransition = () => {
    if (saving) return;
    setNextState(null);
    setReason("");
    setSaveError(null);
  };

  const submit = async () => {
    const trimmedReason = reason.trim();
    if (!nextState || !trimmedReason) return;
    setSaving(true);
    setSaveError(null);
    try {
      const response = await replaceArtifactQuarantine({
        path: { repositoryId },
        query: { coordinate, digest },
        headers: { "If-Match": record?.version ?? "0" },
        body: { state: nextState, reason: trimmedReason },
      });
      if (response.error) {
        setSaveError(response.error);
        return;
      }
      if (response.data)
        queryClient.setQueryData(
          artifactQuarantineKey(repositoryId, coordinate, digest),
          response.data,
        );
      setNextState(null);
      setReason("");
      await load();
    } catch (error) {
      setSaveError(error);
    } finally {
      setSaving(false);
    }
  };

  const activeRecord = record?.state === "quarantined" ? record : null;
  const modalQuarantining = nextState === "quarantined";
  const updatedAt = activeRecord
    ? (activeRecord.quarantinedAt ?? activeRecord.updatedAt)
    : (record?.releasedAt ?? record?.updatedAt);

  return (
    <div className="col-span-full border-b border-line pb-3">
      {loadError !== null ? (
        <ErrorBanner error={loadError} onRetry={() => void load()} />
      ) : activeRecord ? (
        <Alert
          type="warning"
          showIcon
          title={t("artifactDetail.artifactQuarantined")}
          description={
            <div className="space-y-1 text-xs">
              {label && (
                <div className="font-mono text-fg-secondary" title={label}>
                  {label}
                </div>
              )}
              <div>{activeRecord.reason}</div>
              <div className="text-fg-tertiary">
                {t(
                  "artifactDetail.promotionAndReplicationAreBlockedOrdinaryReadsAre",
                )}
              </div>
              <div className="text-fg-tertiary">
                {activeRecord.updatedBy} · {formatDate(updatedAt, locale)}
              </div>
            </div>
          }
          action={
            canManage ? (
              <Button
                size="small"
                icon={<UnlockOutlined />}
                onClick={() => openTransition("released")}
              >
                {t("artifactDetail.release")}
              </Button>
            ) : undefined
          }
        />
      ) : loading || !canManage ? null : (
        <div className="flex flex-wrap items-center justify-between gap-3 text-xs">
          <div>
            <div className="text-fg-secondary">
              {label
                ? `${label} · ${t("artifactDetail.notQuarantined")}`
                : t("artifactDetail.artifactNotQuarantined")}
            </div>
            {record && (
              <div className="mt-1 text-fg-disabled">
                {t("artifactDetail.lastReleased")} · {record.updatedBy} ·{" "}
                {formatDate(updatedAt, locale)}
              </div>
            )}
          </div>
          <Button
            danger
            size="small"
            icon={<SafetyCertificateOutlined />}
            onClick={() => openTransition("quarantined")}
          >
            {t("artifactDetail.quarantine")}
          </Button>
        </div>
      )}

      <Modal
        open={nextState !== null}
        title={
          modalQuarantining
            ? t("artifactDetail.quarantineArtifact")
            : t("artifactDetail.releaseArtifact")
        }
        onClose={closeTransition}
        busy={saving}
        footer={
          <Space>
            <Button onClick={closeTransition} disabled={saving}>
              {t("artifactDetail.cancel")}
            </Button>
            <Button
              type="primary"
              danger={modalQuarantining}
              disabled={!reason.trim()}
              loading={saving}
              onClick={() => void submit()}
            >
              {modalQuarantining
                ? t("artifactDetail.quarantine2")
                : t("artifactDetail.release2")}
            </Button>
          </Space>
        }
      >
        <div className="space-y-3">
          <p className="text-sm leading-6 text-fg-secondary">
            {modalQuarantining
              ? t(
                  "artifactDetail.quarantineBlocksPromotionAndReplicationForThisArtifact",
                )
              : t(
                  "artifactDetail.releasePermitsPromotionAndReplicationAgainOtherSecurity",
                )}
          </p>
          <div className="rounded border border-line px-3 py-2">
            <div
              className="truncate font-mono text-xs text-fg-secondary"
              title={coordinate}
            >
              {coordinate}
            </div>
            <div
              className="mt-1 truncate font-mono text-xs text-fg-disabled"
              title={digest}
            >
              {digest}
            </div>
          </div>
          <div>
            <label
              className="mb-1.5 block text-xs text-fg-secondary"
              htmlFor="artifact-quarantine-reason"
            >
              {modalQuarantining
                ? t("artifactDetail.quarantineReason")
                : t("artifactDetail.releaseReason")}
            </label>
            <Input.TextArea
              id="artifact-quarantine-reason"
              value={reason}
              maxLength={1024}
              showCount
              rows={4}
              placeholder={t("artifactDetail.enterAReasonForTheAuditTrail")}
              onChange={(event) => setReason(event.target.value)}
            />
          </div>
          {saveError !== null && <ErrorBanner error={saveError} />}
        </div>
      </Modal>
    </div>
  );
}
