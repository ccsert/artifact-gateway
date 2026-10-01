import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { PreferencesProvider } from "../../lib/preferences";
import { buildStorageChartData, StorageByFormatChart } from "./DashboardCharts";

const chartSpies = vi.hoisted(() => ({
  pie: vi.fn(),
}));

vi.mock("./dashboard-charts/DashboardPiePlot", () => ({
  default: ({ config }: { config: Record<string, unknown> }) => {
    chartSpies.pie(config);
    return <div data-testid="ant-design-pie" />;
  },
}));

function renderWithPreferences(node: React.ReactNode) {
  return render(<PreferencesProvider>{node}</PreferencesProvider>);
}

describe("DashboardCharts", () => {
  beforeEach(() => {
    localStorage.clear();
    chartSpies.pie.mockClear();
  });

  afterEach(() => cleanup());

  it("orders every supported format and includes APT capacity", () => {
    expect(
      buildStorageChartData({ apt: 512, oci: 1024, future: 256 }).map(
        ({ format, bytes }) => ({ format, bytes }),
      ),
    ).toEqual([
      { format: "oci", bytes: 1024 },
      { format: "apt", bytes: 512 },
      { format: "future", bytes: 256 },
    ]);
  });

  it("renders the capacity empty state without mounting a chart", () => {
    renderWithPreferences(
      <StorageByFormatChart bytesByFormat={null} totalBytes={null} />,
    );

    expect(screen.getByText("容量统计未启用或暂无数据")).toBeVisible();
    expect(chartSpies.pie).not.toHaveBeenCalled();
  });

  it("passes real format data to a lazily loaded Ant Design pie chart", async () => {
    renderWithPreferences(
      <StorageByFormatChart
        bytesByFormat={{ oci: 1536, apt: 512 }}
        totalBytes={2048}
      />,
    );

    expect(await screen.findByTestId("ant-design-pie")).toBeVisible();
    expect(screen.getByRole("img")).toHaveAccessibleName(
      "各制品格式的存储占比，合计 2.0 KiB",
    );
    expect(screen.getByText("APT")).toBeVisible();
    expect(screen.getByText("25%")).toBeVisible();
    expect(chartSpies.pie).toHaveBeenCalledWith(
      expect.objectContaining({
        angleField: "bytes",
        colorField: "format",
        innerRadius: 0.68,
        data: expect.arrayContaining([
          expect.objectContaining({ format: "apt", bytes: 512 }),
        ]),
      }),
    );
  });
});
