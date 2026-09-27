import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Repository } from "../../client";
import { PreferencesProvider } from "../../lib/preferences";
import { RepositorySettingsTab } from "./RepositorySettingsTab";

vi.mock("../../client", () => ({
  testEgressProxy: vi.fn(),
  updateRepository: vi.fn(),
}));

const proxyRepository: Repository = {
  id: "11111111-1111-4111-8111-111111111111",
  name: "central-proxy",
  format: "maven",
  type: "proxy",
  anonymousRead: false,
  mavenStrictPublication: false,
  state: "active",
  version: "1",
};

afterEach(() => {
  cleanup();
  localStorage.clear();
  vi.clearAllMocks();
});

describe("RepositorySettingsTab", () => {
  it("labels the egress protocol in the active locale", async () => {
    const user = userEvent.setup();
    localStorage.setItem("ag.console.locale", "en-US");

    render(
      <PreferencesProvider>
        <RepositorySettingsTab
          repo={proxyRepository}
          capabilities={null}
          onUpdated={() => {}}
        />
      </PreferencesProvider>,
    );

    await user.click(await screen.findByText("Custom proxy"));

    expect(screen.getByText("HTTP (CONNECT)")).toBeInTheDocument();
    expect(screen.queryByText("HTTP（CONNECT）")).not.toBeInTheDocument();
  });
});
