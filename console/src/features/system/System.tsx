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
  const { t } = usePreferences();
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
        title={t("system.systemRuntime")}
        description={t(
          "system.inspectGatewayNodesDiagnosticsLogsRepositoryQuotaAlerts",
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
            label: t("system.systemDiagnostics"),
            children: (
              <div className="ag-page-stack ag-diagnostics-tab">
                <SystemDiagnosticsPanel />
                <RuntimeNodesPanel />
              </div>
            ),
          },
          {
            key: "logs",
            label: t("system.runtimeLogs"),
            children: <RuntimeLogsPanel />,
          },
          {
            key: "alerts",
            label: t("system.quotaAlerts"),
            children: activeTab === "alerts" ? <QuotaAlertsPanel /> : null,
          },
          {
            key: "email",
            label: t("system.emailNotifications"),
            children:
              activeTab === "email" ? <EmailNotificationsPanel /> : null,
          },
        ]}
      />
    </div>
  );
}
