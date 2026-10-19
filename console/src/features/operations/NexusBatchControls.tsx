import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { Button } from "antd";
import {
  pauseNexusMigrationBatch,
  resumeNexusMigrationBatch,
  retryNexusMigrationBatch,
  cancelNexusMigrationBatch,
  type NexusMigrationBatch,
  type NexusMigrationCapabilities,
} from "../../client";
import { ErrorBanner, Notice } from "../../components/ui/Feedback";
import { ConfirmDialog, Modal } from "../../components/ui/Modal";
import { usePreferences } from "../../lib/preferences";
import {
  batchRequest,
  canCancel,
  canPause,
  canResume,
  canRetry,
  capacityEvidence,
  emptyCapacity,
} from "./nexusBatchModel";
import { batchKeys, batchOptions, useBatchCommand } from "./nexusBatchQueries";
import { NexusCapacityFields } from "./NexusCapacityFields";

export function NexusBatchControls({
  batch,
  binding,
  onRescan,
}: {
  batch: NexusMigrationBatch;
  binding: NexusMigrationCapabilities | undefined;
  onRescan: () => void;
}) {
  const { t } = usePreferences();
  const client = useQueryClient();
  const command = useBatchCommand();
  // The dialog keeps the version it opened with, even as the background poll advances.
  const [control, setControl] = useState<{
    operation: "resume" | "retry" | "cancel";
    batch: NexusMigrationBatch;
  } | null>(null);
  const [capacity, setCapacity] = useState(emptyCapacity);
  let capacityValid = false;
  try {
    capacityEvidence(
      capacity,
      binding,
      control?.batch.capacityEvidence.observedAt,
    );
    capacityValid = true;
  } catch {
    /* Unknown blocks the command. */
  }
  const submit = (
    operation: "pause" | "resume" | "retry" | "cancel",
    snapshot: NexusMigrationBatch,
  ) => {
    void command.run(
      {
        operation,
        batchId: snapshot.id,
        version: snapshot.version,
        ...(["resume", "retry"].includes(operation) ? { capacity } : {}),
      },
      async (key) => {
        const options = {
          path: { batchId: snapshot.id },
          headers: { "If-Match": snapshot.version, "Idempotency-Key": key },
        };
        if (operation === "pause")
          return batchRequest(pauseNexusMigrationBatch(options), true);
        if (operation === "cancel")
          return batchRequest(cancelNexusMigrationBatch(options), true);
        const body = {
          capacityEvidence: capacityEvidence(
            capacity,
            binding,
            snapshot.capacityEvidence.observedAt,
          ),
        };
        return batchRequest(
          operation === "resume"
            ? resumeNexusMigrationBatch({ ...options, body })
            : retryNexusMigrationBatch({ ...options, body }),
          true,
        );
      },
      async () => {
        // An idempotent replay can contain an older version: GET is authoritative.
        await client.fetchQuery(batchOptions(snapshot.id));
        await client.invalidateQueries({ queryKey: batchKeys.list });
        setControl(null);
        setCapacity(emptyCapacity());
      },
      async () => {
        try {
          await client.fetchQuery(batchOptions(snapshot.id));
          await client.invalidateQueries({ queryKey: batchKeys.list });
          await client.invalidateQueries({
            queryKey: batchKeys.entries(snapshot.id),
          });
        } finally {
          // The captured CAS version cannot be reused after a refusal/unknown
          // result. Leave its error visible beside the freshly read controls.
          setControl(null);
          setCapacity(emptyCapacity());
        }
      },
    );
  };
  const close = () => {
    if (!command.busy) setControl(null);
  };
  return (
    <div className="ag-migration-form">
      {!control && command.error !== null && (
        <ErrorBanner
          error={command.error}
          onRetry={() => {
            void client.invalidateQueries({
              queryKey: batchKeys.detail(batch.id),
            });
          }}
        />
      )}
      <div className="ag-migration-actions">
        {canPause(batch) && (
          <Button
            disabled={command.busy}
            loading={command.busy && !control}
            onClick={() => submit("pause", batch)}
          >
            {t("nexusBatch.pause")}
          </Button>
        )}
        {canResume(batch) && (
          <Button
            disabled={command.busy || !binding?.executionEnabled}
            onClick={() => {
              setCapacity(emptyCapacity());
              setControl({ operation: "resume", batch });
            }}
          >
            {t("nexusBatch.resume")}
          </Button>
        )}
        {canRetry(batch) && (
          <Button
            disabled={command.busy || !binding?.executionEnabled}
            onClick={() => {
              setCapacity(emptyCapacity());
              setControl({ operation: "retry", batch });
            }}
          >
            {t("nexusBatch.retry")}
          </Button>
        )}
        {canCancel(batch) && (
          <Button
            danger
            disabled={command.busy}
            onClick={() => setControl({ operation: "cancel", batch })}
          >
            {t("nexusBatch.cancel")}
          </Button>
        )}
        <Button
          disabled={command.busy || !binding?.executionEnabled}
          onClick={onRescan}
        >
          {t("nexusBatch.rescan")}
        </Button>
      </div>
      {control?.operation === "cancel" && (
        <ConfirmDialog
          open
          title={t("nexusBatch.cancelTitle")}
          danger
          busy={command.busy}
          onClose={close}
          confirmLabel={t("nexusBatch.cancel")}
          onConfirm={() => submit("cancel", control.batch)}
          message={
            <div className="ag-migration-form">
              {command.error !== null && <ErrorBanner error={command.error} />}
              <p>{t("nexusBatch.cancelHint")}</p>
            </div>
          }
        />
      )}
      {control && control.operation !== "cancel" && binding && (
        <Modal
          open
          title={t(
            control.operation === "resume"
              ? "nexusBatch.resume"
              : "nexusBatch.retry",
          )}
          busy={command.busy}
          onClose={close}
        >
          <form
            className="ag-migration-form"
            onSubmit={(event) => {
              event.preventDefault();
              if (capacityValid) submit(control.operation, control.batch);
            }}
          >
            {command.error !== null && <ErrorBanner error={command.error} />}
            {control.operation === "retry" && (
              <Notice
                tone="info"
                title={t("nexusBatch.retryHint", {
                  attempts: binding.maxAttempts,
                })}
                closable={false}
              />
            )}
            <NexusCapacityFields
              binding={binding}
              value={capacity}
              onChange={setCapacity}
              previous={control.batch.capacityEvidence.observedAt}
              disabled={command.busy}
            />
            <div className="ag-migration-actions">
              <Button
                type="primary"
                htmlType="submit"
                loading={command.busy}
                disabled={!capacityValid}
              >
                {t(
                  control.operation === "resume"
                    ? "nexusBatch.resume"
                    : "nexusBatch.retry",
                )}
              </Button>
            </div>
          </form>
        </Modal>
      )}
    </div>
  );
}
