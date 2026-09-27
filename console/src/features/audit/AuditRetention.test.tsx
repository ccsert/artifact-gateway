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
  it("reads the standing cleanup note as information until the policy is dirty", async () => {
    const user = userEvent.setup();
    mockPolicy.mockResolvedValue({
      data: { version: "3", enabled: true, keepDays: 90 },
    } as never);
    mockJobs.mockResolvedValue({ data: [] } as never);

    renderPage();

    const note = await screen.findByText("清理说明");
    const alert = note.closest(".ant-alert");
    expect(alert).toHaveClass("ant-alert-info");
    expect(alert).not.toHaveClass("ant-alert-warning");

    await user.click(screen.getByRole("switch", { name: /切换自动清理/ }));

    expect(alert).toHaveClass("ant-alert-warning");
    expect(alert).not.toHaveClass("ant-alert-info");
  });
});
