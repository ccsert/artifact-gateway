import { act, cleanup, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { DiagnosticHttpErrorRate } from "../../client";
import { PreferencesProvider } from "../../lib/preferences";
import { HTTPErrorRatePanel } from "./HTTPErrorRatePanel";

afterEach(() => {
  cleanup();
  vi.useRealTimers();
});
beforeEach(() => {
  vi.useFakeTimers();
  vi.setSystemTime(new Date("2026-10-03T12:05:00Z"));
});

const snapshot: DiagnosticHttpErrorRate = {
  checkedAt: "2026-10-03T12:05:00Z",
  sampleAt: "2026-10-03T12:05:00Z",
  windowStart: "2026-10-03T12:00:00Z",
  windowEnd: "2026-10-03T12:05:00Z",
  instanceId: "synthetic-node",
  sessionId: "synthetic-session",
  source: "artifact_gateway_http_requests_total",
  scope: "responding_process_business_http",
  state: "available",
  windowSeconds: 300,
  minimumRequests: 20,
  sampleIntervalSeconds: 15,
  maxSampleAgeSeconds: 30,
  coverageSeconds: 300,
  requests: 20,
  errors: 2,
  ratio: 0.1,
};

function panel(rate?: DiagnosticHttpErrorRate) {
  return (
    <PreferencesProvider>
      <HTTPErrorRatePanel rate={rate} />
    </PreferencesProvider>
  );
}

describe("HTTPErrorRatePanel", () => {
  it("rejects a delayed response at receipt and a future sample clock", () => {
    vi.setSystemTime(new Date("2026-10-03T12:05:31Z"));
    const view = render(panel(snapshot));
    expect(screen.getByText("样本已过期，比例未知")).toBeInTheDocument();
    expect(screen.getByTestId("http-error-rate-ratio")).toHaveTextContent("—");
    vi.setSystemTime(new Date("2026-10-03T12:04:59Z"));
    view.rerender(panel({ ...snapshot }));
    expect(screen.getByText("采样时钟异常，比例未知")).toBeInTheDocument();
  });
  it("shows safe counts, node, actual window and true zero without a health judgment", () => {
    render(panel({ ...snapshot, errors: 0, ratio: 0 }));
    expect(screen.getByTestId("http-error-rate-ratio")).toHaveTextContent("0%");
    expect(screen.getByText("20")).toBeInTheDocument();
    expect(screen.getByText(/synthetic-node/)).toBeInTheDocument();
    expect(screen.getByText(/此比例不表示服务健康/)).toBeInTheDocument();
    expect(screen.getByText(/300s/)).toBeInTheDocument();
  });

  it.each([
    ["warming_up", "窗口预热，数据不足", 20],
    ["low_sample", "少于 20 个请求，数据不足", 19],
    ["no_traffic", "窗口内无请求，比例未知", 0],
    ["session_changed", "进程会话已切换，窗口重新预热", 1],
    ["counter_reset", "计数器已重置，窗口重新预热", 1],
  ] as const)(
    "keeps safe counts and hides the ratio for %s",
    (reason, message, requests) => {
      render(
        panel({
          ...snapshot,
          state: "unknown",
          reason,
          requests,
          errors: 0,
          ratio: undefined,
        }),
      );
      expect(screen.getByText(message)).toBeInTheDocument();
      expect(screen.getByTestId("http-error-rate-ratio")).toHaveTextContent(
        "—",
      );
      expect(screen.getAllByText(String(requests)).length).toBeGreaterThan(0);
    },
  );

  it("expires a displayed sample without another diagnostics request and retains old counts", () => {
    vi.useFakeTimers();
    const view = render(panel(snapshot));
    expect(screen.getByTestId("http-error-rate-ratio")).toHaveTextContent(
      "10%",
    );
    act(() => {
      vi.advanceTimersByTime(31_000);
    });
    expect(screen.getByTestId("http-error-rate-ratio")).toHaveTextContent("—");
    expect(screen.getByText("旧快照总请求")).toBeInTheDocument();
    expect(screen.getByText("20")).toBeInTheDocument();
    // A repeated response with an old sample stays stale, even after receipt resets.
    view.rerender(panel({ ...snapshot, checkedAt: "2026-10-03T12:05:31Z" }));
    expect(screen.getByText("样本已过期，比例未知")).toBeInTheDocument();
  });

  it("rejects unsafe or inconsistent values and does not round a positive ratio to zero", () => {
    const view = render(
      panel({ ...snapshot, requests: Number.MAX_SAFE_INTEGER + 1 }),
    );
    expect(screen.getByTestId("http-error-rate-ratio")).toHaveTextContent("—");
    expect(screen.queryByText(/9,007/)).not.toBeInTheDocument();
    view.rerender(
      panel({ ...snapshot, requests: 100_000, errors: 1, ratio: 0.00001 }),
    );
    expect(screen.getByTestId("http-error-rate-ratio")).toHaveTextContent(
      "<0.01%",
    );
    view.rerender(panel());
    expect(screen.getByText("样本未报告或无效，比例未知")).toBeInTheDocument();
    expect(screen.queryByText("0%")).not.toBeInTheDocument();
  });
});
