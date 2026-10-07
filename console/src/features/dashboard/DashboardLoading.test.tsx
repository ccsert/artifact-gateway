import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
} from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { PreferencesProvider } from "../../lib/preferences";
import { DashboardPage } from "./Dashboard";

const api = vi.hoisted(() => ({
  listGroups: vi.fn(),
  listAudits: vi.fn(),
  getOverviewStatistics: vi.fn(),
}));
vi.mock("../../client", () => api);
vi.mock("../../components/ui/dashboard-charts/loadDashboardPiePlot", () => ({
  preloadDashboardPiePlot: vi.fn(),
}));
vi.mock("../../components/ui/DashboardCharts", () => ({
  StorageByFormatChart: () => <div data-testid="storage-chart" />,
}));

const snapshot = {
  generatedAt: "2026-10-07T00:00:00Z",
  totals: {
    requests: { oneDay: 1, sevenDays: 2, thirtyDays: 3 },
    denied: { oneDay: 0, sevenDays: 0, thirtyDays: 0 },
    usedBytes: 1024,
    objectCount: 1,
  },
  repositories: [
    {
      repositoryId: "synthetic-repository",
      name: "synthetic-raw",
      format: "raw",
      requests: { oneDay: 1, sevenDays: 2, thirtyDays: 3 },
      denied: { oneDay: 0, sevenDays: 0, thirtyDays: 0 },
      usedBytes: 1024,
      objectCount: 1,
    },
  ],
};

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

function renderDashboard() {
  return render(
    <MemoryRouter>
      <PreferencesProvider>
        <DashboardPage />
      </PreferencesProvider>
    </MemoryRouter>,
  );
}

describe("independent dashboard requests", () => {
  beforeEach(() => {
    localStorage.clear();
    vi.resetAllMocks();
    api.listGroups.mockResolvedValue({ data: { items: [] } });
    api.listAudits.mockResolvedValue({ data: [] });
    api.getOverviewStatistics.mockResolvedValue({ data: snapshot });
  });
  afterEach(cleanup);

  it("shows storage while both optional requests are still pending", async () => {
    api.listGroups.mockReturnValue(new Promise(() => {}));
    api.listAudits.mockReturnValue(new Promise(() => {}));
    renderDashboard();
    expect(await screen.findByTestId("storage-chart")).toBeVisible();
    expect(screen.getByText("synthetic-raw")).toBeVisible();
    expect(screen.getByText("正在加载分组…")).toBeVisible();
    expect(screen.getByText("正在加载审计事件…")).toBeVisible();
  });

  it("keeps storage visible and retries only the failed groups request", async () => {
    api.listGroups.mockResolvedValueOnce({
      error: new Error("synthetic group failure"),
    });
    renderDashboard();
    expect(await screen.findByTestId("storage-chart")).toBeVisible();
    expect(screen.getByText("synthetic group failure")).toBeVisible();
    fireEvent.click(screen.getByRole("button", { name: /重试/ }));
    expect(await screen.findByText("共 0 个成员引用")).toBeVisible();
    expect(api.listGroups).toHaveBeenCalledTimes(2);
    expect(api.listAudits).toHaveBeenCalledTimes(1);
    expect(api.getOverviewStatistics).toHaveBeenCalledTimes(1);
  });

  it("shows audit data when statistics reject, then recovers statistics alone", async () => {
    api.getOverviewStatistics.mockRejectedValueOnce(
      new Error("synthetic capacity failure"),
    );
    renderDashboard();
    expect(await screen.findByText("synthetic capacity failure")).toBeVisible();
    expect(screen.getByText("暂无审计记录")).toBeVisible();
    expect(screen.queryByTestId("storage-chart")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /重试/ }));
    expect(await screen.findByTestId("storage-chart")).toBeVisible();
    expect(api.listAudits).toHaveBeenCalledTimes(1);
  });

  it("aborts requests on unmount and ignores their late completion", async () => {
    const late = deferred<{ data: typeof snapshot }>();
    api.getOverviewStatistics.mockReturnValue(late.promise);
    const view = renderDashboard();
    const signal = api.getOverviewStatistics.mock.calls[0][0]
      .signal as AbortSignal;
    expect(signal.aborted).toBe(false);
    view.unmount();
    expect(signal.aborted).toBe(true);
    await act(async () => {
      late.resolve({ data: snapshot });
    });
    expect(screen.queryByTestId("storage-chart")).not.toBeInTheDocument();
  });

  it("preserves the optional endpoint 404 fallback", async () => {
    api.listGroups.mockResolvedValue({ error: { status: 404 } });
    api.listAudits.mockResolvedValue({ error: "404 page not found" });
    renderDashboard();
    expect(await screen.findByTestId("storage-chart")).toBeVisible();
    expect(screen.getByText("共 0 个成员引用")).toBeVisible();
    expect(screen.getByText("暂无审计记录")).toBeVisible();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });
});
