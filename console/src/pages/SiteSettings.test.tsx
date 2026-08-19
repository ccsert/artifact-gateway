import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  getSiteSettings,
  replaceSiteSettings,
  type SiteSettings,
} from "../client";
import { AntdProvider } from "../app/AntdProvider";
import { SiteName } from "../components/SiteBrand";
import { defaultConsoleThemes } from "../lib/consoleTheme";
import { PreferencesProvider } from "../lib/preferences";
import { SiteSettingsProvider } from "../lib/siteSettings";
import { SiteSettingsPage } from "./SiteSettings";

vi.mock("../client", () => ({
  getSiteSettings: vi.fn(),
  replaceSiteSettings: vi.fn(),
}));

const mockGetSiteSettings = vi.mocked(getSiteSettings);
const mockReplaceSiteSettings = vi.mocked(replaceSiteSettings);

const initial: SiteSettings = {
  version: "3",
  siteName: "Artifact Gateway",
  logoUrl: "",
  brandMark: "AG",
  enabledThemeIds: [
    "gateway-dark",
    "gateway-light",
    "aerok-dark",
    "aerok-light",
  ],
  defaultThemeId: "gateway-dark",
  availableThemes: defaultConsoleThemes,
  updatedAt: "2026-08-19T01:00:00Z",
};

function renderPage() {
  return render(
    <SiteSettingsProvider>
      <PreferencesProvider>
        <AntdProvider>
          <div data-testid="global-site-name">
            <SiteName />
          </div>
          <SiteSettingsPage />
        </AntdProvider>
      </PreferencesProvider>
    </SiteSettingsProvider>,
  );
}

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
  localStorage.clear();
});

describe("SiteSettingsPage", () => {
  it("saves a versioned identity and applies it to the whole Console", async () => {
    mockGetSiteSettings.mockResolvedValue({ data: initial } as never);
    mockReplaceSiteSettings.mockResolvedValue({
      data: { ...initial, version: "4", siteName: "Acme Packages" },
    } as never);
    const user = userEvent.setup();
    renderPage();

    const name = await screen.findByLabelText("站点名称");
    expect(screen.getByTestId("global-site-name")).toHaveTextContent(
      "Artifact Gateway",
    );
    await user.clear(name);
    await user.type(name, "Acme Packages");
    await user.click(screen.getByRole("button", { name: "保存并应用" }));

    await waitFor(() =>
      expect(mockReplaceSiteSettings).toHaveBeenCalledWith({
        body: {
          siteName: "Acme Packages",
          logoUrl: "",
          brandMark: "AG",
          enabledThemeIds: [
            "gateway-dark",
            "gateway-light",
            "aerok-dark",
            "aerok-light",
          ],
          defaultThemeId: "gateway-dark",
        },
        headers: { "If-Match": "3" },
      }),
    );
    expect(screen.getByTestId("global-site-name")).toHaveTextContent(
      "Acme Packages",
    );
    expect(document.title).toBe("Acme Packages Console");
  });

  it("selects the enabled theme set and deployment default", async () => {
    mockGetSiteSettings.mockResolvedValue({ data: initial } as never);
    mockReplaceSiteSettings.mockResolvedValue({
      data: {
        ...initial,
        version: "4",
        enabledThemeIds: ["aerok-light"],
        defaultThemeId: "aerok-light",
      },
    } as never);
    const user = userEvent.setup();
    renderPage();

    for (const name of [/Gateway Dark/, /Gateway Light/, /Aerok Dark/]) {
      await user.click(await screen.findByRole("checkbox", { name }));
    }
    await user.click(screen.getByRole("button", { name: "保存并应用" }));

    await waitFor(() =>
      expect(mockReplaceSiteSettings).toHaveBeenCalledWith({
        body: {
          siteName: "Artifact Gateway",
          logoUrl: "",
          brandMark: "AG",
          enabledThemeIds: ["aerok-light"],
          defaultThemeId: "aerok-light",
        },
        headers: { "If-Match": "3" },
      }),
    );
  });

  it("renders an initial failure without leaving a loading state underneath", async () => {
    mockGetSiteSettings.mockResolvedValue({
      error: { code: "site_settings_unavailable", message: "offline" },
    } as never);
    renderPage();

    expect(await screen.findByText("请求出错")).toBeInTheDocument();
    expect(screen.queryByText("加载中…")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: /重试/ })).toBeInTheDocument();
  });
});
