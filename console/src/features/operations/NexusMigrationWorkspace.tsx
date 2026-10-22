import { useEffect, useState } from "react";
import { Button } from "antd";
import { useSearchParams } from "react-router-dom";
import { usePreferences } from "../../lib/preferences";
import { MigrationSourcesPanel } from "./MigrationSourcesPanel";
import { NexusBatchesPanel } from "./NexusBatchesPanel";
import { NexusSchedulesPanel } from "./NexusSchedulesPanel";
import type { BatchDraft } from "./nexusBatchModel";
import { useMigrationAccess } from "./migrationAccess";

export function NexusMigrationWorkspace() {
  const { t } = usePreferences();
  const [params, setParams] = useSearchParams();
  const batches = params.get("view") === "batches" || params.has("batch");
  const schedules = params.get("view") === "schedules" && !params.has("batch");
  const [draft, setDraft] = useState<BatchDraft | null>(null);
  const access = useMigrationAccess();
  useEffect(() => {
    setDraft(null);
  }, [access.epoch]);
  const show = (view: "sources" | "batches" | "schedules") => {
    const next = new URLSearchParams(params);
    next.set("view", view);
    if (view !== "batches") next.delete("batch");
    setParams(next);
  };
  return (
    <div className="ag-page-stack ag-migration-panel">
      <div
        className="ag-migration-actions"
        role="group"
        aria-label={t("migration.tab")}
      >
        <Button
          type={!batches && !schedules ? "primary" : "default"}
          aria-pressed={!batches && !schedules}
          onClick={() => show("sources")}
        >
          {t("nexusBatch.sourceView")}
        </Button>
        <Button
          type={schedules ? "primary" : "default"}
          aria-pressed={schedules}
          onClick={() => show("schedules")}
        >
          {t("nexusSchedule.title")}
        </Button>
        <Button
          type={batches ? "primary" : "default"}
          aria-pressed={batches}
          onClick={() => show("batches")}
        >
          {t("nexusBatch.batchView")}
        </Button>
      </div>
      {schedules ? (
        <NexusSchedulesPanel />
      ) : batches ? (
        <NexusBatchesPanel
          draft={draft}
          onDraftConsumed={() => setDraft(null)}
        />
      ) : (
        <MigrationSourcesPanel
          onPlanBatch={(value) => {
            setDraft(value);
            show("batches");
          }}
        />
      )}
    </div>
  );
}
