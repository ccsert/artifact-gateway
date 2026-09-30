import { Tabs } from "antd";
import { useSearchParams } from "react-router-dom";
import { PageHeader } from "../../components/ui/Layout";
import { usePreferences } from "../../lib/preferences";
import { RuntimeLogsPanel } from "../operations/RuntimeLogsPanel";
import { RuntimeNodesPanel } from "../operations/RuntimeNodesPanel";
import { SystemDiagnosticsPanel } from "../operations/SystemDiagnosticsPanel";

export function SystemPage() {
  const { text } = usePreferences();
  const [searchParams, setSearchParams] = useSearchParams();
  const activeTab = searchParams.get("tab") === "logs" ? "logs" : "diagnostics";

  return (
    <div className="ag-page-stack">
      <PageHeader
        title={text("系统运行", "System runtime")}
        description={text(
          "查看 Gateway 节点状态、依赖诊断和运行日志。",
          "Inspect Gateway node health, dependency diagnostics, and runtime logs.",
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
        ]}
      />
    </div>
  );
}
