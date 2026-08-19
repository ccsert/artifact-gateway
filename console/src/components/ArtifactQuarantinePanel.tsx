import { useCallback, useEffect, useRef, useState } from "react";
import { SafetyCertificateOutlined, UnlockOutlined } from "@ant-design/icons";
import { Alert, Button, Input, Modal } from "antd";
import { getArtifactQuarantine, replaceArtifactQuarantine } from "../client";
import type { ArtifactQuarantine } from "../client";
import { formatDate } from "../lib/format";
import { usePreferences } from "../lib/preferences";
import { ErrorBanner, isNotFound } from "./Feedback";

type QuarantineState = ArtifactQuarantine["state"];

export function ArtifactQuarantinePanel({
  repositoryId,
  coordinate,
  digest,
  canManage = false,
  label,
}: {
  repositoryId?: string;
  coordinate: string;
  digest?: string;
  canManage?: boolean;
  label?: string;
}) {
  const { locale, text } = usePreferences();
  const [record, setRecord] = useState<ArtifactQuarantine | null>(null);
  const [loading, setLoading] = useState(false);
  const [loadError, setLoadError] = useState<unknown>(null);
  const [nextState, setNextState] = useState<QuarantineState | null>(null);
  const [reason, setReason] = useState("");
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<unknown>(null);
  const requestSequence = useRef(0);

  const load = useCallback(async () => {
    if (!repositoryId || !coordinate || !digest) return;
    const requestID = ++requestSequence.current;
    setLoading(true);
    setLoadError(null);
    try {
      const response = await getArtifactQuarantine({
        path: { repositoryId },
        query: { coordinate, digest },
      });
      if (requestID !== requestSequence.current) return;
      if (response.error) {
        if (response.response?.status === 404 || isNotFound(response.error)) {
          setRecord(null);
          return;
        }
        setLoadError(response.error);
        return;
      }
      setRecord(response.data ?? null);
    } catch (error) {
      if (requestID === requestSequence.current) setLoadError(error);
    } finally {
      if (requestID === requestSequence.current) setLoading(false);
    }
  }, [coordinate, digest, repositoryId]);

  useEffect(() => {
    requestSequence.current++;
    setRecord(null);
    setLoadError(null);
    setNextState(null);
    setReason("");
    setSaveError(null);
    void load();
    return () => {
      // This monotonic generation ref invalidates any response that resolves
      // after an identity change or unmount; it is not a rendered-node ref.
      // eslint-disable-next-line react-hooks/exhaustive-deps
      requestSequence.current++;
    };
  }, [load]);

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
      if (response.data) setRecord(response.data);
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
    <div className="col-span-full border-b border-zinc-800/80 pb-3">
      {loadError !== null ? (
        <ErrorBanner error={loadError} onRetry={() => void load()} />
      ) : activeRecord ? (
        <Alert
          type="error"
          showIcon
          title={text("制品已隔离", "Artifact quarantined")}
          description={
            <div className="space-y-1 text-xs">
              {label && (
                <div className="font-mono text-zinc-300" title={label}>
                  {label}
                </div>
              )}
              <div>{activeRecord.reason}</div>
              <div className="text-zinc-500">
                {text(
                  "已阻止晋升和复制；普通读取不受影响。",
                  "Promotion and replication are blocked; ordinary reads are unaffected.",
                )}
              </div>
              <div className="text-zinc-500">
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
                {text("解除隔离", "Release")}
              </Button>
            ) : undefined
          }
        />
      ) : loading || !canManage ? null : (
        <div className="flex flex-wrap items-center justify-between gap-3 text-xs">
          <div>
            <div className="text-zinc-400">
              {label
                ? `${label} · ${text("未隔离", "Not quarantined")}`
                : text("制品未隔离", "Artifact not quarantined")}
            </div>
            {record && (
              <div className="mt-1 text-zinc-600">
                {text("最近解除", "Last released")} · {record.updatedBy} ·{" "}
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
            {text("隔离制品", "Quarantine")}
          </Button>
        </div>
      )}

      <Modal
        open={nextState !== null}
        centered
        destroyOnHidden
        title={
          modalQuarantining
            ? text("隔离制品", "Quarantine artifact")
            : text("解除隔离", "Release artifact")
        }
        okText={
          modalQuarantining
            ? text("确认隔离", "Quarantine")
            : text("确认解除", "Release")
        }
        cancelText={text("取消", "Cancel")}
        confirmLoading={saving}
        okButtonProps={{
          danger: modalQuarantining,
          disabled: !reason.trim(),
        }}
        cancelButtonProps={{ disabled: saving }}
        closable={!saving}
        mask={{ closable: !saving }}
        onCancel={closeTransition}
        onOk={() => void submit()}
      >
        <div className="space-y-3">
          <p className="text-sm leading-6 text-zinc-400">
            {modalQuarantining
              ? text(
                  "隔离后将阻止该制品晋升和复制，但不会阻止普通读取。",
                  "Quarantine blocks promotion and replication for this artifact, but does not block ordinary reads.",
                )
              : text(
                  "解除后将重新允许晋升和复制，其他安全准入规则仍然生效。",
                  "Release permits promotion and replication again; other security admission rules still apply.",
                )}
          </p>
          <div className="rounded border border-zinc-800 px-3 py-2">
            <div
              className="truncate font-mono text-xs text-zinc-300"
              title={coordinate}
            >
              {coordinate}
            </div>
            <div
              className="mt-1 truncate font-mono text-xs text-zinc-600"
              title={digest}
            >
              {digest}
            </div>
          </div>
          <div>
            <label
              className="mb-1.5 block text-xs text-zinc-400"
              htmlFor="artifact-quarantine-reason"
            >
              {modalQuarantining
                ? text("隔离原因", "Quarantine reason")
                : text("解除原因", "Release reason")}
            </label>
            <Input.TextArea
              id="artifact-quarantine-reason"
              value={reason}
              maxLength={1024}
              showCount
              rows={4}
              placeholder={text(
                "请输入可供审计追踪的原因",
                "Enter a reason for the audit trail",
              )}
              onChange={(event) => setReason(event.target.value)}
            />
          </div>
          {saveError !== null && <ErrorBanner error={saveError} />}
        </div>
      </Modal>
    </div>
  );
}
