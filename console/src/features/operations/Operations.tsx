import { Tabs } from "antd";
import { Navigate, useSearchParams } from "react-router-dom";
import { PageHeader } from "../../components/ui/Layout";
import { SectionBoundary } from "../../components/ui/SectionBoundary";
import { usePreferences } from "../../lib/preferences";
import { JobHistoryPanel } from "./JobHistoryPanel";
import { ScheduledTasksPanel } from "./ScheduledTasksPanel";
import { WebhookDeliveriesPanel } from "./WebhookDeliveriesPanel";

/** Operations: scheduled tasks, webhook delivery and the job history. */
export function OperationsPage() {
  const { text } = usePreferences();
  const [searchParams, setSearchParams] = useSearchParams();
  const requestedTab = searchParams.get("tab");
  const activeTab =
    requestedTab && ["schedules", "webhooks", "jobs"].includes(requestedTab)
      ? requestedTab
      : "schedules";

  if (requestedTab === "diagnostics" || requestedTab === "logs") {
    return <Navigate to={`/system?${searchParams.toString()}`} replace />;
  }

  return (
    <div className="ag-page-stack">
      <PageHeader
        title={text("任务中心", "Operations")}
        description={text(
          "配置周期性维护计划，并跨仓库查看后台任务的执行状态。",
          "Schedule recurring maintenance and track background jobs across repositories.",
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
            label: text("计划任务", "Scheduled tasks"),
            children: <ScheduledTasksPanel />,
          },
          {
            key: "webhooks",
            label: text("Webhook 投递", "Webhook delivery"),
            children: <WebhookDeliveriesPanel />,
          },
          {
            key: "jobs",
            label: text("执行记录", "Job history"),
            children: (
              <SectionBoundary resetKeys={[activeTab]}>
                <JobHistoryPanel active={activeTab === "jobs"} />
              </SectionBoundary>
            ),
          },
        ]}
      />
    </div>
  );
}
