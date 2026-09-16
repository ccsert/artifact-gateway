import { Line } from "@ant-design/plots";
import type { LineConfig } from "@ant-design/plots";

export default function DashboardLinePlot({ config }: { config: LineConfig }) {
  return (
    <div className="min-w-0" data-testid="ant-design-line-ready">
      <Line {...config} />
    </div>
  );
}
