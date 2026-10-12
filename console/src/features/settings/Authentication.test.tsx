import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, vi } from "vitest";
import {
  getOidcSettings,
  replaceOidcSettings,
  type OidcSettings,
} from "../../client";
import { PreferencesProvider } from "../../lib/preferences";
import { TestQueryProvider } from "../../test/queryClient";
import { AuthenticationPage } from "./Authentication";

vi.mock("../../client", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../client")>()),
  getOidcSettings: vi.fn(),
  replaceOidcSettings: vi.fn(),
  testOidcSettings: vi.fn(),
}));

const saved: OidcSettings = {
  version: "4",
  source: "database",
  enabled: true,
  issuer: "https://id.example.com/realms/artifacts",
  audience: "artifact-gateway-api",
  clientId: "artifact-gateway-console",
  clientSecretConfigured: true,
  redirectUrl: "https://gateway.example.com/auth/oidc/callback",
  scopes: ["openid", "profile"],
  adminSubjects: [],
  memberRoles: ["artifact-members"],
  adminRoles: ["artifact-admins"],
  provisioningMode: "jit",
  emailLinkingEnabled: false,
  jitDefaultRole: "none",
  updatedAt: "2026-10-12T08:00:00Z",
} as OidcSettings;

function renderPage() {
  render(
    <TestQueryProvider>
      <PreferencesProvider>
        <AuthenticationPage />
      </PreferencesProvider>
    </TestQueryProvider>,
  );
}

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

it("saves edits against the loaded version and restarts the form on the new one", async () => {
  const user = userEvent.setup();
  vi.mocked(getOidcSettings).mockResolvedValue({ data: saved } as never);
  vi.mocked(replaceOidcSettings).mockResolvedValue({
    data: { ...saved, version: "5", audience: "gateway-api-v2" },
  } as never);
  renderPage();

  const audience = await screen.findByDisplayValue("artifact-gateway-api");
  await user.clear(audience);
  await user.type(audience, "gateway-api-v2");
  await user.click(screen.getByRole("button", { name: /移除密钥/ }));
  await user.click(screen.getByRole("button", { name: /标记移除/ }));
  expect(screen.getByText("等待保存")).toBeInTheDocument();
  await user.click(screen.getByRole("button", { name: /保存并应用/ }));

  await waitFor(() =>
    expect(replaceOidcSettings).toHaveBeenCalledWith({
      body: expect.objectContaining({
        audience: "gateway-api-v2",
        clearClientSecret: true,
      }),
      headers: { "If-Match": "4" },
    }),
  );
  expect(
    await screen.findByText("配置已保存并在当前节点生效"),
  ).toBeInTheDocument();
  expect(screen.getByDisplayValue("gateway-api-v2")).toBeInTheDocument();
  // The new version starts clean: no pending removal and nothing to save.
  expect(screen.queryByText("等待保存")).not.toBeInTheDocument();
  expect(screen.getByRole("button", { name: /配置已同步/ })).toBeDisabled();
});

it("rejects an enabled provider without an issuer before saving", async () => {
  const user = userEvent.setup();
  vi.mocked(getOidcSettings).mockResolvedValue({ data: saved } as never);
  renderPage();

  const issuer = await screen.findByDisplayValue(saved.issuer);
  await user.clear(issuer);
  await user.click(screen.getByRole("button", { name: /保存并应用/ }));

  expect(await screen.findByText("启用 OIDC 时必填")).toBeInTheDocument();
  expect(replaceOidcSettings).not.toHaveBeenCalled();
});
