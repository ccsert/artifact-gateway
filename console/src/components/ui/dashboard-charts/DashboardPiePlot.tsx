import { Pie } from "@ant-design/plots";
import type { PieConfig } from "@ant-design/plots";

export default function DashboardPiePlot({ config }: { config: PieConfig }) {
  return (
    <div className="min-w-0" data-testid="ant-design-pie-ready">
      <Pie {...config} />
    </div>
  );
}
