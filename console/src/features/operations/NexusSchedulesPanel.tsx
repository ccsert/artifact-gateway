import { useState } from "react";
import { Button, InputNumber, Popconfirm, Select } from "antd";
import { Link } from "react-router-dom";
import { useQueryClient } from "@tanstack/react-query";
import {
  createNexusMigrationSchedule,
  pauseNexusMigrationSchedule,
  refreshNexusMigrationScheduleCapacity,
  resumeNexusMigrationSchedule,
  stopNexusMigrationSchedule,
  type NexusMigrationCapabilities,
  type NexusMigrationSchedule,
} from "../../client";
import {
  EmptyState,
  ErrorBanner,
  Loading,
  Notice,
} from "../../components/ui/Feedback";
import { Card, Field, Pagination } from "../../components/ui/Layout";
import { Badge } from "../../components/ui/Badge";
import { usePreferences } from "../../lib/preferences";
import {
  batchReadDenied,
  batchReason,
  batchRequest,
  capacityEvidence,
  emptyCapacity,
} from "./nexusBatchModel";
import {
  useBatchCapabilities,
  useBatchCommand,
  useBatchPages,
} from "./nexusBatchQueries";
import { migrationWrite, useMigrationAccess } from "./migrationAccess";
import { NexusCapacityFields } from "./NexusCapacityFields";
import { scheduleKeys, useSchedulePages } from "./nexusScheduleQueries";

export function NexusSchedulesPanel() {
  const access = useMigrationAccess();
  return <NexusSchedulesContent key={access.epoch} />;
}

function NexusSchedulesContent() {
  const { t } = usePreferences();
  const access = useMigrationAccess();
  const list = useSchedulePages();
  const capabilities = useBatchCapabilities();
  const [creating, setCreating] = useState(false);
  const binding = !capabilities.error ? capabilities.data : undefined;
  const plans =
    access.error || batchReadDenied(list.error)
      ? null
      : (list.data?.pages.flatMap((page) => page.items) ?? null);
  const refresh = async () => {
    await list.refetch();
  };
  return (
    <div
      className="ag-page-stack ag-migration-panel"
      data-testid="nexus-schedules-panel"
    >
      <Notice
        tone="info"
        title={t("nexusSchedule.background")}
        description={t("nexusSchedule.backgroundHint")}
        closable={false}
      />
      <Notice
        tone="warning"
        title={t("nexusSchedule.capacityBoundary")}
        description={t("nexusSchedule.capacityBoundaryHint")}
        closable={false}
      />
      {capabilities.error && (
        <ErrorBanner
          error={capabilities.error}
          onRetry={() => {
            void capabilities.refetch();
          }}
        />
      )}
      {(access.error || list.error) && (
        <ErrorBanner
          error={access.error || list.error}
          tone={plans ? "warning" : "error"}
          onRetry={() => {
            void refresh();
          }}
        />
      )}
      <Card>
        <div className="ag-migration-header">
          <h2>{t("nexusSchedule.title")}</h2>
          <div className="ag-migration-actions">
            <Button
              loading={list.isFetching}
              onClick={() => {
                void refresh();
              }}
            >
              {t("nexusSchedule.refresh")}
            </Button>
            <Button
              type="primary"
              disabled={
                !binding?.executionEnabled || Boolean(access.error) || creating
              }
              onClick={() => setCreating(true)}
            >
              {t("nexusSchedule.new")}
            </Button>
          </div>
        </div>
        {plans === null ? (
          !list.error && !access.error && <Loading />
        ) : plans.length === 0 ? (
          <EmptyState
            title={t("nexusSchedule.empty")}
            hint={t("nexusSchedule.emptyHint")}
          />
        ) : null}
        <Pagination
          hasMore={list.hasNextPage}
          loading={list.isFetchingNextPage}
          label={t("nexusBatch.more")}
          onMore={() => {
            void list.fetchNextPage();
          }}
        />
      </Card>
      {creating && binding?.executionEnabled && !access.error && (
        <ScheduleCreate binding={binding} onClose={() => setCreating(false)} />
      )}
      {plans && plans.length > 0 && (
        <div className="ag-page-stack" data-testid="nexus-schedule-list">
          {plans.map((plan) => (
            <ScheduleCard
              key={plan.id}
              plan={plan}
              binding={binding}
              reconcile={refresh}
            />
          ))}
        </div>
      )}
    </div>
  );
}

function ScheduleCreate({
  onClose,
}: {
  binding: NexusMigrationCapabilities;
  onClose: () => void;
}) {
  const { t } = usePreferences();
  const client = useQueryClient();
  const batches = useBatchPages();
  const command = useBatchCommand();
  const [templateBatchId, setTemplate] = useState("");
  const [minutes, setMinutes] = useState<number | null>(60);
  const successful = batchReadDenied(batches.error)
    ? []
    : (batches.data?.pages
        .flatMap((page) => page.items)
        .filter(
          (batch) =>
            batch.state === "completed" &&
            batch.scanComplete &&
            !batch.lastError &&
            batch.counts.failed +
              batch.counts.conflict +
              batch.counts.sourceMissing +
              batch.counts.sourceDeleted ===
              0,
        ) ?? []);
  const selected = successful.find((batch) => batch.id === templateBatchId);
  const valid =
    selected &&
    minutes !== null &&
    Number.isInteger(minutes) &&
    minutes >= 1 &&
    minutes <= 43_200;
  return (
    <Card>
      <div className="ag-migration-header">
        <h2>{t("nexusSchedule.new")}</h2>
        <Button disabled={command.busy} onClick={onClose}>
          {t("nexusBatch.close")}
        </Button>
      </div>
      <form
        className="ag-migration-content"
        data-testid="nexus-schedule-create"
        onSubmit={(event) => {
          event.preventDefault();
          if (!valid || command.busy || batches.error) return;
          const body = { templateBatchId, intervalSeconds: minutes! * 60 };
          void command.run(
            { operation: "create-schedule", body },
            (key) =>
              migrationWrite(client, () =>
                batchRequest(
                  createNexusMigrationSchedule({
                    body,
                    headers: { "Idempotency-Key": key },
                  }),
                  true,
                ),
              ),
            async () => {
              await client.invalidateQueries({ queryKey: scheduleKeys.all });
              onClose();
            },
          );
        }}
      >
        {command.error !== null && <ErrorBanner error={command.error} />}
        {batches.error && (
          <ErrorBanner
            error={batches.error}
            onRetry={() => {
              void batches.refetch();
            }}
          />
        )}
        {!batches.data && !batches.error ? (
          <Loading />
        ) : (
          <>
            <Field
              label={t("nexusSchedule.template")}
              hint={t("nexusSchedule.templateHint")}
            >
              <Select
                className="w-full"
                aria-label={t("nexusSchedule.template")}
                value={templateBatchId || undefined}
                disabled={command.busy}
                options={successful.map((batch) => ({
                  value: batch.id,
                  label: `${batch.mappings.map((mapping) => mapping.sourceRepository).join(", ")} · ${batch.id.slice(0, 8)}`,
                }))}
                onChange={setTemplate}
              />
            </Field>
            {successful.length === 0 && !batches.error && (
              <p>{t("nexusSchedule.emptyHint")}</p>
            )}
            <Pagination
              hasMore={batches.hasNextPage}
              loading={batches.isFetchingNextPage}
              label={t("nexusBatch.more")}
              onMore={() => {
                void batches.fetchNextPage();
              }}
            />
            <Field label={t("nexusSchedule.interval")}>
              <InputNumber
                className="w-full"
                aria-label={t("nexusSchedule.interval")}
                min={1}
                max={43_200}
                precision={0}
                value={minutes}
                disabled={command.busy}
                onChange={setMinutes}
              />
            </Field>
            <p className="text-sm text-fg-secondary">
              {t("nexusSchedule.createHint")}
            </p>
            <div className="ag-migration-actions">
              <Button
                htmlType="submit"
                type="primary"
                loading={command.busy}
                disabled={!valid || Boolean(batches.error)}
              >
                {t("nexusSchedule.new")}
              </Button>
            </div>
          </>
        )}
      </form>
    </Card>
  );
}

function ScheduleCard({
  plan,
  binding,
  reconcile,
}: {
  plan: NexusMigrationSchedule;
  binding?: NexusMigrationCapabilities;
  reconcile: () => Promise<void>;
}) {
  const { t } = usePreferences();
  const client = useQueryClient();
  const command = useBatchCommand();
  const [observation, setObservation] = useState<"capacity" | "resume" | null>(
    null,
  );
  const canResume = plan.state === "paused" || plan.state === "blocked";
  const stopped = plan.state === "stopped";
  const act = (action: "pause" | "stop") =>
    void command.run(
      { id: plan.id, version: plan.version, action },
      (key) =>
        migrationWrite(client, () =>
          batchRequest(
            (action === "pause"
              ? pauseNexusMigrationSchedule
              : stopNexusMigrationSchedule)({
              path: { scheduleId: plan.id },
              headers: { "If-Match": plan.version, "Idempotency-Key": key },
            }),
            true,
          ),
        ),
      async () => {
        await client.invalidateQueries({ queryKey: scheduleKeys.all });
      },
      reconcile,
    );
  return (
    <Card className="ag-nexus-schedule">
      <div className="ag-migration-header">
        <div>
          <h2>
            {plan.mappings
              .map((mapping) => mapping.sourceRepository)
              .join(", ")}
          </h2>
          <p className="text-xs text-fg-secondary">
            {t("nexusSchedule.intervalValue", {
              minutes: plan.intervalSeconds / 60,
            })}
          </p>
        </div>
        <Badge
          tone={
            plan.state === "active"
              ? "info"
              : plan.state === "blocked"
                ? "danger"
                : plan.state === "waiting_capacity"
                  ? "warning"
                  : "neutral"
          }
        >
          {t(`nexusSchedule.${plan.state}`)}
        </Badge>
      </div>
      <div className="ag-migration-content" data-testid="nexus-schedule-card">
        {command.error !== null && <ErrorBanner error={command.error} />}
        <dl className="ag-nexus-schedule-times">
          <div>
            <dt>{t("nexusSchedule.nextRun")}</dt>
            <dd>
              {plan.nextRunAt
                ? new Date(plan.nextRunAt).toLocaleString()
                : t("nexusSchedule.noNext")}
            </dd>
          </div>
          <div>
            <dt>{t("nexusSchedule.lastSuccessful")}</dt>
            <dd>
              {plan.lastSuccessfulAt
                ? new Date(plan.lastSuccessfulAt).toLocaleString()
                : t("nexusSchedule.never")}
            </dd>
          </div>
        </dl>
        <p className="text-xs text-fg-secondary">
          {t("nexusSchedule.successScope")}
        </p>
        {plan.lastError && (
          <Notice
            tone="warning"
            title={batchReason(plan.lastError, t)}
            closable={false}
          />
        )}
        <div className="ag-migration-actions">
          {(plan.activeBatchId || plan.lastBatchId) && (
            <Link
              to={`/operations?tab=migrations&view=batches&batch=${plan.activeBatchId || plan.lastBatchId}`}
            >
              {t(
                plan.activeBatchId
                  ? "nexusSchedule.currentBatch"
                  : "nexusSchedule.lastBatch",
              )}
            </Link>
          )}
          {!stopped && (
            <>
              {plan.state !== "paused" && (
                <Button
                  disabled={command.busy || observation !== null}
                  onClick={() => act("pause")}
                >
                  {t("nexusSchedule.pause")}
                </Button>
              )}
              <Button
                disabled={
                  !binding?.executionEnabled ||
                  command.busy ||
                  observation !== null
                }
                onClick={() =>
                  setObservation(canResume ? "resume" : "capacity")
                }
              >
                {t(
                  canResume ? "nexusSchedule.resume" : "nexusSchedule.capacity",
                )}
              </Button>
              <Popconfirm
                title={t("nexusSchedule.stop")}
                description={t("nexusSchedule.stopConfirm")}
                onConfirm={() => act("stop")}
              >
                <Button danger disabled={command.busy || observation !== null}>
                  {t("nexusSchedule.stop")}
                </Button>
              </Popconfirm>
            </>
          )}
        </div>
        {!stopped && (
          <p className="text-xs text-fg-secondary">
            {t("nexusSchedule.stopHint")}
          </p>
        )}
        {observation && binding && (
          <ScheduleCapacity
            key={plan.id}
            plan={plan}
            binding={binding}
            action={observation}
            onClose={() => setObservation(null)}
            reconcile={reconcile}
          />
        )}
      </div>
    </Card>
  );
}

function ScheduleCapacity({
  plan: currentPlan,
  binding,
  action,
  onClose,
  reconcile,
}: {
  plan: NexusMigrationSchedule;
  binding: NexusMigrationCapabilities;
  action: "capacity" | "resume";
  onClose: () => void;
  reconcile: () => Promise<void>;
}) {
  const { t } = usePreferences();
  const client = useQueryClient();
  const command = useBatchCommand();
  // Keep the reviewed CAS version and user input while background polls run.
  // A stale submission is reconciled read-only and never silently rebased.
  const [plan] = useState(currentPlan);
  const [capacity, setCapacity] = useState(emptyCapacity);
  const previous =
    plan.capacityEvidence?.observedAt ?? plan.consumedObservedAt ?? undefined;
  let valid = false;
  try {
    capacityEvidence(capacity, binding, previous);
    valid = true;
  } catch {
    /* Explicit operator observation is required. */
  }
  return (
    <form
      className="ag-migration-form"
      data-testid="nexus-schedule-capacity"
      onSubmit={(event) => {
        event.preventDefault();
        if (!valid || command.busy) return;
        void command.run(
          { operation: action, id: plan.id, version: plan.version, capacity },
          (key) =>
            migrationWrite(client, () =>
              batchRequest(
                (action === "resume"
                  ? resumeNexusMigrationSchedule
                  : refreshNexusMigrationScheduleCapacity)({
                  path: { scheduleId: plan.id },
                  headers: { "If-Match": plan.version, "Idempotency-Key": key },
                  body: {
                    capacityEvidence: capacityEvidence(
                      capacity,
                      binding,
                      previous,
                    ),
                  },
                }),
                true,
              ),
            ),
          async () => {
            await client.invalidateQueries({ queryKey: scheduleKeys.all });
            onClose();
          },
          reconcile,
        );
      }}
    >
      {command.error !== null && <ErrorBanner error={command.error} />}
      <NexusCapacityFields
        value={capacity}
        onChange={setCapacity}
        binding={binding}
        previous={previous}
        disabled={command.busy}
      />
      <div className="ag-migration-actions">
        <Button
          htmlType="submit"
          type="primary"
          loading={command.busy}
          disabled={!valid}
        >
          {t("nexusSchedule.saveCapacity")}
        </Button>
        <Button disabled={command.busy} onClick={onClose}>
          {t("nexusBatch.close")}
        </Button>
      </div>
    </form>
  );
}
