import { Tabs } from "antd";
import { Navigate, useSearchParams } from "react-router-dom";
import { PageHeader } from "../../components/ui/Layout";
import { SectionBoundary } from "../../components/ui/SectionBoundary";
import { usePreferences } from "../../lib/preferences";
import { JobHistoryPanel } from "./JobHistoryPanel";
import { ScheduledTasksPanel } from "./ScheduledTasksPanel";
import { WebhookDeliveriesPanel } from "./WebhookDeliveriesPanel";
import { MigrationSourcesPanel } from "./MigrationSourcesPanel";

/** Operations: scheduled tasks, webhook delivery and the job history. */
export function OperationsPage() {
  const { t } = usePreferences();
  const [searchParams, setSearchParams] = useSearchParams();
  const requestedTab = searchParams.get("tab");
  const activeTab =
    requestedTab &&
    ["schedules", "webhooks", "jobs", "migrations"].includes(requestedTab)
      ? requestedTab
      : "schedules";

  if (requestedTab === "diagnostics" || requestedTab === "logs") {
    return <Navigate to={`/system?${searchParams.toString()}`} replace />;
  }

  return (
    <div className="ag-page-stack">
      <PageHeader
        title={t("operations.operations")}
        description={t(
          "operations.scheduleRecurringMaintenanceAndTrackBackgroundJobsAcross",
        )}
      />
      <Tabs
        className="ag-compact-tabs"
        tabBarGutter={24}
        activeKey={activeTab}
        onChange={(key) => {
          const nextParams = new URLSearchParams(searchParams);
          nextParams.set("tab", key);
          setSearchParams(nextParams);
        }}
        items={[
          {
            key: "schedules",
            label: t("operations.scheduledTasks"),
            children: <ScheduledTasksPanel />,
          },
          {
            key: "webhooks",
            label: t("operations.webhookDelivery"),
            children: <WebhookDeliveriesPanel />,
          },
          {
            key: "jobs",
            label: t("operations.jobHistory"),
            children: (
              <SectionBoundary resetKeys={[activeTab]}>
                <JobHistoryPanel active={activeTab === "jobs"} />
              </SectionBoundary>
            ),
          },
          {
            key: "migrations",
            label: t("migration.tab"),
            children:
              activeTab === "migrations" ? <MigrationSourcesPanel /> : null,
          },
        ]}
      />
    </div>
  );
}
