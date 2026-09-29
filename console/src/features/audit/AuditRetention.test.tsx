import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { getAuditRetentionPolicy, listAuditRetentionJobs } from "../../client";
import { PreferencesProvider } from "../../lib/preferences";
import { AuditRetentionPage } from "./AuditRetention";

vi.mock("../../client", () => ({
  executeAuditRetention: vi.fn(),
  getAuditRetentionPolicy: vi.fn(),
  listAuditRetentionJobs: vi.fn(),
  replaceAuditRetentionPolicy: vi.fn(),
}));

const mockPolicy = vi.mocked(getAuditRetentionPolicy);
const mockJobs = vi.mocked(listAuditRetentionJobs);

afterEach(() => {
  cleanup();
  localStorage.clear();
  vi.clearAllMocks();
});

function renderPage() {
  return render(
    <PreferencesProvider>
      <AuditRetentionPage />
    </PreferencesProvider>,
  );
}

describe("AuditRetentionPage", () => {
  it("keeps policy controls clear when there are unsaved changes", async () => {
    const user = userEvent.setup();
    mockPolicy.mockResolvedValue({
      data: { version: "3", enabled: true, keepDays: 90 },
    } as never);
    mockJobs.mockResolvedValue({ data: [] } as never);

    renderPage();

    expect(await screen.findByText("策略设置")).toBeInTheDocument();
    expect(screen.getByText("清理任务（0）")).toBeInTheDocument();
    expect(screen.queryByText("清理说明")).not.toBeInTheDocument();
    expect(
      screen.queryByRole("group", { name: "页面摘要" }),
    ).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "保存策略" })).toBeDisabled();

    await user.click(screen.getByRole("switch", { name: /切换自动清理/ }));

    expect(screen.getByRole("button", { name: "保存策略" })).toBeEnabled();
    expect(screen.getByRole("button", { name: "立即执行清理" })).toBeDisabled();
  });
});
