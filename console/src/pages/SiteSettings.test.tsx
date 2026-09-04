import {
  cleanup,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  deleteConsoleThemePackage,
  getSiteSettings,
  installConsoleThemePackage,
  replaceConsoleThemePackage,
  replaceSiteSettings,
  validateConsoleThemePackage,
  type ConsoleTheme,
  type ConsoleThemePackage,
  type SiteSettings,
} from "../client";
import { AntdProvider } from "../app/AntdProvider";
import { SiteName } from "../components/SiteBrand";
import { defaultConsoleThemes } from "../lib/consoleTheme";
import { PreferencesProvider } from "../lib/preferences";
import { SiteSettingsProvider } from "../lib/siteSettings";
import { SiteSettingsPage } from "./SiteSettings";

vi.mock("../client", () => ({
  deleteConsoleThemePackage: vi.fn(),
  getSiteSettings: vi.fn(),
  installConsoleThemePackage: vi.fn(),
  replaceConsoleThemePackage: vi.fn(),
  replaceSiteSettings: vi.fn(),
  validateConsoleThemePackage: vi.fn(),
}));

const mockDeleteConsoleThemePackage = vi.mocked(deleteConsoleThemePackage);
const mockGetSiteSettings = vi.mocked(getSiteSettings);
const mockInstallConsoleThemePackage = vi.mocked(installConsoleThemePackage);
const mockReplaceConsoleThemePackage = vi.mocked(replaceConsoleThemePackage);
const mockReplaceSiteSettings = vi.mocked(replaceSiteSettings);
const mockValidateConsoleThemePackage = vi.mocked(validateConsoleThemePackage);

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

const uploadedThemePackage: ConsoleThemePackage = {
  $schema: "https://artifact-gateway.dev/schemas/console-theme-v1.json",
  schemaVersion: 1,
  id: "twilight-lab",
  name: "Twilight Lab",
  description: "A quiet violet workspace",
  mode: "dark",
  token: {
    colorPrimary: "#8b7cf6",
    colorSuccess: "#4bc58b",
    colorWarning: "#e6a85c",
    colorError: "#ea6c7b",
    colorInfo: "#70a5eb",
    colorTextBase: "#ececf2",
    colorBgBase: "#13131a",
  },
};

const uploadedTheme: ConsoleTheme = {
  ...uploadedThemePackage,
  source: "managed",
  version: "1",
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

  it("validates, previews, installs, and stages an uploaded theme for enabling", async () => {
    mockGetSiteSettings.mockResolvedValue({ data: initial } as never);
    mockValidateConsoleThemePackage.mockResolvedValue({
      data: { theme: uploadedThemePackage, status: "available" },
    } as never);
    mockInstallConsoleThemePackage.mockResolvedValue({
      data: uploadedTheme,
    } as never);
    mockReplaceSiteSettings.mockResolvedValue({
      data: {
        ...initial,
        version: "4",
        enabledThemeIds: [...initial.enabledThemeIds, uploadedTheme.id],
        availableThemes: [...initial.availableThemes, uploadedTheme],
      },
    } as never);
    const user = userEvent.setup();
    const { container } = renderPage();

    await screen.findByText("Console 主题");
    const input = container.querySelector<HTMLInputElement>(
      'input[type="file"][accept*="json"]',
    );
    expect(input).not.toBeNull();
    const file = new File(
      [JSON.stringify(uploadedThemePackage)],
      "twilight-lab.theme.json",
      { type: "application/json" },
    );
    await user.upload(input!, file);

    expect(await screen.findByText("校验通过，可以安装")).toBeInTheDocument();
    expect(screen.getByText("A quiet violet workspace")).toBeInTheDocument();
    expect(mockValidateConsoleThemePackage).toHaveBeenCalledWith({
      body: uploadedThemePackage,
    });

    await user.click(screen.getByRole("button", { name: "安装并启用" }));
    await waitFor(() =>
      expect(mockInstallConsoleThemePackage).toHaveBeenCalledWith({
        body: uploadedThemePackage,
      }),
    );
    expect(
      await screen.findByRole("checkbox", { name: /Twilight Lab/ }),
    ).toBeChecked();

    await user.click(screen.getByRole("button", { name: "保存并应用" }));
    await waitFor(() =>
      expect(mockReplaceSiteSettings).toHaveBeenCalledWith({
        body: {
          siteName: "Artifact Gateway",
          logoUrl: "",
          brandMark: "AG",
          enabledThemeIds: [...initial.enabledThemeIds, uploadedTheme.id],
          defaultThemeId: "gateway-dark",
        },
        headers: { "If-Match": "3" },
      }),
    );
  });

  it("replaces an uploaded theme with optimistic concurrency", async () => {
    const existingTheme = { ...uploadedTheme, version: "7" };
    mockGetSiteSettings.mockResolvedValue({
      data: {
        ...initial,
        availableThemes: [...initial.availableThemes, existingTheme],
      },
    } as never);
    mockValidateConsoleThemePackage.mockResolvedValue({
      data: {
        theme: uploadedThemePackage,
        status: "replaceable",
        existingSource: "managed",
        existingVersion: "7",
      },
    } as never);
    mockReplaceConsoleThemePackage.mockResolvedValue({
      data: { ...uploadedTheme, version: "8" },
    } as never);
    const user = userEvent.setup();
    const { container } = renderPage();

    await screen.findByText("Console 主题");
    const input = container.querySelector<HTMLInputElement>(
      'input[type="file"][accept*="json"]',
    );
    await user.upload(
      input!,
      new File(
        [JSON.stringify(uploadedThemePackage)],
        "twilight-lab.theme.json",
        { type: "application/json" },
      ),
    );
    await user.click(await screen.findByRole("button", { name: "确认替换" }));

    await waitFor(() =>
      expect(mockReplaceConsoleThemePackage).toHaveBeenCalledWith({
        body: uploadedThemePackage,
        path: { themeId: uploadedTheme.id },
        headers: { "If-Match": "7" },
      }),
    );
  });

  it("deletes only a disabled uploaded theme", async () => {
    mockGetSiteSettings.mockResolvedValue({
      data: {
        ...initial,
        availableThemes: [...initial.availableThemes, uploadedTheme],
      },
    } as never);
    mockDeleteConsoleThemePackage.mockResolvedValue({} as never);
    const user = userEvent.setup();
    renderPage();

    await screen.findByRole("checkbox", { name: /Twilight Lab/ });
    await user.click(screen.getByRole("button", { name: "删除" }));
    const popoverTitle = await screen.findByText("删除这个上传主题？");
    const popover = popoverTitle.closest(".ant-popover");
    expect(popover).not.toBeNull();
    await user.click(
      within(popover as HTMLElement).getByRole("button", {
        name: /删\s*除/,
      }),
    );

    await waitFor(() =>
      expect(mockDeleteConsoleThemePackage).toHaveBeenCalledWith({
        path: { themeId: uploadedTheme.id },
        headers: { "If-Match": "1" },
      }),
    );
    expect(
      screen.queryByRole("checkbox", { name: /Twilight Lab/ }),
    ).not.toBeInTheDocument();
  });
});
