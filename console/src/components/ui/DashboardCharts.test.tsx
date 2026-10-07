import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { PreferencesProvider } from "../../lib/preferences";
import { buildStorageChartData, StorageByFormatChart } from "./DashboardCharts";
import { loadDashboardPiePlot } from "./dashboard-charts/loadDashboardPiePlot";

const chartSpies = vi.hoisted(() => ({
  pie: vi.fn(),
  fail: false,
}));

vi.mock("./dashboard-charts/DashboardPiePlot", () => ({
  default: ({ config }: { config: Record<string, unknown> }) => {
    if (chartSpies.fail) throw new Error("synthetic plot failure");
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
    chartSpies.fail = false;
  });

  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it("contains a chart failure while keeping the format legend available", async () => {
    chartSpies.fail = true;
    vi.spyOn(console, "error").mockImplementation(() => undefined);
    renderWithPreferences(
      <StorageByFormatChart bytesByFormat={{ oci: 1024 }} totalBytes={1024} />,
    );
    expect(await screen.findByText("存储图表加载失败")).toBeVisible();
    expect(screen.getByRole("list", { name: "格式图例" })).toHaveTextContent(
      "OCI",
    );
    expect(
      screen.getByText("容量数据仍可查看，请刷新页面重试图表。"),
    ).toBeVisible();
  });

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

  it("renders a completed preload immediately without a chart loading fallback", async () => {
    await loadDashboardPiePlot();
    renderWithPreferences(
      <StorageByFormatChart bytesByFormat={{ oci: 1024 }} totalBytes={1024} />,
    );

    expect(screen.getByTestId("ant-design-pie")).toBeVisible();
    expect(screen.queryByText("正在加载图表…")).not.toBeInTheDocument();
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
