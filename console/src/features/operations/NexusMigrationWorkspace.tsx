import { useState } from "react";
import { Button } from "antd";
import { useSearchParams } from "react-router-dom";
import { usePreferences } from "../../lib/preferences";
import { MigrationSourcesPanel } from "./MigrationSourcesPanel";
import { NexusBatchesPanel } from "./NexusBatchesPanel";
import type { BatchDraft } from "./nexusBatchModel";

export function NexusMigrationWorkspace() {
  const { t } = usePreferences();
  const [params, setParams] = useSearchParams();
  const batches = params.get("view") === "batches" || params.has("batch");
  const [draft, setDraft] = useState<BatchDraft | null>(null);
  const show = (view: "sources" | "batches") => {
    const next = new URLSearchParams(params);
    next.set("view", view);
    if (view === "sources") next.delete("batch");
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
          type={!batches ? "primary" : "default"}
          aria-pressed={!batches}
          onClick={() => show("sources")}
        >
          {t("nexusBatch.sourceView")}
        </Button>
        <Button
          type={batches ? "primary" : "default"}
          aria-pressed={batches}
          onClick={() => show("batches")}
        >
          {t("nexusBatch.batchView")}
        </Button>
      </div>
      {batches ? (
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
