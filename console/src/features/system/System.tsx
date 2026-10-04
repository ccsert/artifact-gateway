import { Tabs } from "antd";
import { useSearchParams } from "react-router-dom";
import { PageHeader } from "../../components/ui/Layout";
import { usePreferences } from "../../lib/preferences";
import { RuntimeLogsPanel } from "../operations/RuntimeLogsPanel";
import { RuntimeNodesPanel } from "../operations/RuntimeNodesPanel";
import { SystemDiagnosticsPanel } from "../operations/SystemDiagnosticsPanel";
import { QuotaAlertsPanel } from "./QuotaAlertsPanel";
import { EmailNotificationsPanel } from "./EmailNotificationsPanel";

export function SystemPage() {
  const { text } = usePreferences();
  const [searchParams, setSearchParams] = useSearchParams();
  const requestedTab = searchParams.get("tab");
  const activeTab =
    requestedTab === "logs" ||
    requestedTab === "alerts" ||
    requestedTab === "email"
      ? requestedTab
      : "diagnostics";

  return (
    <div className="ag-page-stack">
      <PageHeader
        title={text("系统运行", "System runtime")}
        description={text(
          "查看 Gateway 节点、诊断、日志、仓库配额告警与邮件通知。",
          "Inspect Gateway nodes, diagnostics, logs, repository quota alerts, and email notifications.",
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
            key: "diagnostics",
            label: text("系统诊断", "System diagnostics"),
            children: (
              <div className="ag-page-stack ag-diagnostics-tab">
                <SystemDiagnosticsPanel />
                <RuntimeNodesPanel />
              </div>
            ),
          },
          {
            key: "logs",
            label: text("运行日志", "Runtime logs"),
            children: <RuntimeLogsPanel />,
          },
          {
            key: "alerts",
            label: text("配额告警", "Quota alerts"),
            children: activeTab === "alerts" ? <QuotaAlertsPanel /> : null,
          },
          {
            key: "email",
            label: text("邮件通知", "Email notifications"),
            children:
              activeTab === "email" ? <EmailNotificationsPanel /> : null,
          },
        ]}
      />
    </div>
  );
}
