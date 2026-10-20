import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Repository } from "../../client";
import { testEgressProxy, updateRepository } from "../../client";
import { PreferencesProvider } from "../../lib/preferences";
import { RepositorySettingsTab } from "./RepositorySettingsTab";

vi.mock("../../client", () => ({
  testEgressProxy: vi.fn(),
  updateRepository: vi.fn(),
}));

const ociProxy: Repository = {
  id: "33333333-3333-4333-8333-333333333333",
  name: "private-registry",
  format: "oci",
  type: "proxy",
  endpoint: "https://registry.example.test",
  allowedHosts: ["registry.example.test"],
  anonymousRead: false,
  mavenStrictPublication: false,
  state: "active",
  version: "7",
  upstreamAuth: { scheme: "bearer", credentialsConfigured: true },
  ociBearer: {
    realm: "https://issuer.example.test/token",
    service: "private-registry",
  },
};
const mockUpdate = vi.mocked(updateRepository);

afterEach(() => {
  cleanup();
  localStorage.clear();
  sessionStorage.clear();
  vi.resetAllMocks();
});

function showSettings(repo: Repository = ociProxy, locale = "en-US") {
  localStorage.setItem("ag.console.locale", locale);
  const client = new QueryClient();
  client.setQueryData(["repository", repo.id], repo);
  const updated = vi.fn();
  render(
    <QueryClientProvider client={client}>
      <PreferencesProvider>
        <RepositorySettingsTab
          repo={repo}
          capabilities={null}
          onUpdated={updated}
        />
      </PreferencesProvider>
    </QueryClientProvider>,
  );
  return { client, updated };
}

describe("OCI upstream token service settings", () => {
  it("loads and edits the bound issuer without changing the egress allowlist", async () => {
    const user = userEvent.setup();
    mockUpdate.mockResolvedValue({ data: ociProxy } as never);
    showSettings();

    expect(
      screen.getByRole("switch", { name: "OCI token exchange" }),
    ).toBeChecked();
    expect(screen.getByLabelText("Token service URL")).toHaveValue(
      ociProxy.ociBearer?.realm,
    );
    expect(screen.getByLabelText("Token audience")).toHaveValue(
      "private-registry",
    );
    expect(
      screen.getByText(/does not grant network access/),
    ).toBeInTheDocument();
    expect(screen.getByLabelText("Upstream token")).toHaveValue("");
    await user.clear(screen.getByLabelText("Token service URL"));
    await user.type(
      screen.getByLabelText("Token service URL"),
      "https://new-issuer.example.test/token",
    );
    await user.clear(screen.getByLabelText("Token audience"));
    await user.type(screen.getByLabelText("Token audience"), "new-registry");
    await user.type(
      screen.getByLabelText("Upstream token"),
      "synthetic-new-binding-secret",
    );
    await user.click(screen.getByRole("button", { name: "Save changes" }));

    await waitFor(() =>
      expect(mockUpdate).toHaveBeenCalledWith(
        expect.objectContaining({
          headers: { "If-Match": "7" },
          body: expect.objectContaining({
            allowedHosts: ["registry.example.test"],
            upstreamAuth: {
              scheme: "bearer",
              secret: "synthetic-new-binding-secret",
            },
            ociBearer: {
              realm: "https://new-issuer.example.test/token",
              service: "new-registry",
            },
          }),
        }),
      ),
    );
  });

  it("sends null and an explicit credential clear when disabling a saved issuer", async () => {
    const user = userEvent.setup();
    mockUpdate.mockResolvedValue({ data: ociProxy } as never);
    showSettings();
    await user.click(
      screen.getByRole("switch", { name: "OCI token exchange" }),
    );
    expect(
      screen.queryByLabelText("Token service URL"),
    ).not.toBeInTheDocument();
    await user.click(screen.getByRole("radio", { name: /Anonymous/ }));
    await user.click(screen.getByRole("button", { name: "Save changes" }));
    await waitFor(() =>
      expect(mockUpdate).toHaveBeenCalledWith(
        expect.objectContaining({
          body: expect.objectContaining({
            ociBearer: null,
            upstreamAuth: { scheme: "none" },
          }),
        }),
      ),
    );
  });

  it("enables a new issuer and resets to the saved binding", async () => {
    const user = userEvent.setup();
    showSettings({ ...ociProxy, ociBearer: undefined });
    expect(screen.queryByLabelText("Token audience")).not.toBeInTheDocument();
    await user.click(
      screen.getByRole("switch", { name: "OCI token exchange" }),
    );
    await user.type(
      screen.getByLabelText("Token service URL"),
      "https://new.example.test/token",
    );
    await user.type(screen.getByLabelText("Token audience"), "new-service");
    await user.click(screen.getByRole("button", { name: "Reset" }));
    expect(
      screen.getByRole("switch", { name: "OCI token exchange" }),
    ).not.toBeChecked();
    expect(
      screen.queryByLabelText("Token service URL"),
    ).not.toBeInTheDocument();
  });

  it("submits an explicitly enabled issuer with a fresh credential", async () => {
    const user = userEvent.setup();
    mockUpdate.mockResolvedValue({ data: ociProxy } as never);
    showSettings({ ...ociProxy, ociBearer: undefined });
    await user.click(
      screen.getByRole("switch", { name: "OCI token exchange" }),
    );
    await user.type(
      screen.getByLabelText("Token service URL"),
      "https://new.example.test/token",
    );
    await user.type(screen.getByLabelText("Token audience"), "new-service");
    await user.type(
      screen.getByLabelText("Upstream token"),
      "synthetic-new-issuer-secret",
    );
    await user.click(screen.getByRole("button", { name: "Save changes" }));
    await waitFor(() =>
      expect(mockUpdate).toHaveBeenCalledWith(
        expect.objectContaining({
          body: expect.objectContaining({
            ociBearer: {
              realm: "https://new.example.test/token",
              service: "new-service",
            },
            upstreamAuth: {
              scheme: "bearer",
              secret: "synthetic-new-issuer-secret",
            },
          }),
        }),
      ),
    );
    expect(screen.getByLabelText("Upstream token")).toHaveValue("");
  });

  it.each(["go", "maven"] as const)(
    "never submits issuer configuration for %s",
    async (format) => {
      const user = userEvent.setup();
      mockUpdate.mockResolvedValue({ data: ociProxy } as never);
      showSettings({ ...ociProxy, format });
      expect(
        screen.queryByRole("switch", { name: "OCI token exchange" }),
      ).not.toBeInTheDocument();
      await user.click(screen.getByRole("button", { name: "Save changes" }));
      await waitFor(() => expect(mockUpdate).toHaveBeenCalledOnce());
      expect(mockUpdate.mock.calls[0]?.[0].body).not.toHaveProperty(
        "ociBearer",
      );
    },
  );

  it.each([
    {
      locale: "en-US",
      realm: "Token service URL",
      secret: "Upstream token",
      save: "Save changes",
      message: /The upstream endpoint or OCI token binding changed/,
    },
    {
      locale: "zh-CN",
      realm: "令牌服务地址",
      secret: "上游令牌",
      save: "保存更改",
      message: /上游地址或 OCI 令牌绑定已更改/,
    },
  ])(
    "explains a rejected rebind in $locale and retains only the public draft",
    async ({ locale, realm, secret, save, message }) => {
      const user = userEvent.setup();
      mockUpdate.mockResolvedValueOnce({
        error: {
          code: "upstream_credential_rebind_required",
          message:
            "changing the upstream endpoint or OCI issuer binding requires a new secret or clearing upstream authentication",
        },
      } as never);
      mockUpdate.mockResolvedValueOnce({ data: ociProxy } as never);
      const { updated } = showSettings(ociProxy, locale);
      await user.clear(screen.getByLabelText(realm));
      await user.type(
        screen.getByLabelText(realm),
        "https://new-issuer.example.test/token",
      );
      await user.click(screen.getByRole("button", { name: save }));
      await waitFor(() =>
        expect(screen.getByText(message)).toBeInTheDocument(),
      );
      expect(screen.getByLabelText(realm)).toHaveValue(
        "https://new-issuer.example.test/token",
      );
      expect(screen.getByLabelText(secret)).toHaveValue("");
      expect(updated).not.toHaveBeenCalled();
      await user.type(
        screen.getByLabelText(secret),
        "synthetic-new-binding-secret",
      );
      await user.click(screen.getByRole("button", { name: save }));
      await waitFor(() => expect(updated).toHaveBeenCalledOnce());
      expect(screen.getByLabelText(secret)).toHaveValue("");
    },
  );
});

describe("proxy write-only credentials", () => {
  it.each([
    { scheme: "basic", secretLabel: "Upstream password", outcome: "success" },
    { scheme: "basic", secretLabel: "Upstream password", outcome: "problem" },
    { scheme: "basic", secretLabel: "Upstream password", outcome: "rejection" },
    { scheme: "bearer", secretLabel: "Upstream token", outcome: "success" },
    { scheme: "bearer", secretLabel: "Upstream token", outcome: "problem" },
    { scheme: "bearer", secretLabel: "Upstream token", outcome: "rejection" },
  ] as const)(
    "clears $scheme and egress secrets after a $outcome and keeps public drafts",
    async ({ scheme, secretLabel, outcome }) => {
      const user = userEvent.setup();
      if (outcome === "success")
        mockUpdate.mockResolvedValue({ data: ociProxy } as never);
      else if (outcome === "problem")
        mockUpdate.mockResolvedValue({
          error: { message: "Invalid issuer binding" },
        } as never);
      else mockUpdate.mockRejectedValue(new Error("Connection unavailable"));
      const { client, updated } = showSettings({
        ...ociProxy,
        upstreamAuth: {
          scheme,
          ...(scheme === "basic" ? { username: "source-reader" } : {}),
          credentialsConfigured: true,
        },
        egressProxy: {
          mode: "custom",
          protocol: "http",
          host: "proxy.example.test",
          port: 8080,
          credentialsConfigured: true,
        },
      });
      const sourceSecret = "synthetic-source-private-value";
      const proxySecret = "synthetic-egress-private-value";
      await user.type(screen.getByLabelText(secretLabel), sourceSecret);
      await user.type(
        screen.getByLabelText("Proxy password (optional)"),
        proxySecret,
      );
      await user.click(screen.getByRole("button", { name: "Save changes" }));

      await waitFor(() => expect(mockUpdate).toHaveBeenCalledOnce());
      expect(mockUpdate.mock.calls[0]?.[0].body).toEqual(
        expect.objectContaining({
          upstreamAuth: {
            scheme,
            ...(scheme === "basic" ? { username: "source-reader" } : {}),
            secret: sourceSecret,
          },
          egressProxy: expect.objectContaining({ password: proxySecret }),
        }),
      );
      await waitFor(() => {
        expect(screen.getByLabelText(secretLabel)).toHaveValue("");
        expect(screen.getByLabelText("Proxy password (optional)")).toHaveValue(
          "",
        );
      });
      expect(screen.getByLabelText("Token service URL")).toHaveValue(
        ociProxy.ociBearer?.realm,
      );
      expect(screen.getByLabelText("Token audience")).toHaveValue(
        "private-registry",
      );
      expect(updated).toHaveBeenCalledTimes(outcome === "success" ? 1 : 0);
      for (const secret of [sourceSecret, proxySecret]) {
        expect(screen.queryByDisplayValue(secret)).not.toBeInTheDocument();
        expect(document.body.textContent).not.toContain(secret);
        expect(window.location.href).not.toContain(secret);
        expect(JSON.stringify(localStorage)).not.toContain(secret);
        expect(JSON.stringify(sessionStorage)).not.toContain(secret);
        expect(
          JSON.stringify(
            client
              .getQueryCache()
              .getAll()
              .map((query) => query.state.data),
          ),
        ).not.toContain(secret);
      }
      if (outcome !== "success") {
        await user.click(screen.getByRole("button", { name: "Save changes" }));
        await waitFor(() => expect(mockUpdate).toHaveBeenCalledTimes(2));
        expect(
          mockUpdate.mock.calls[1]?.[0].body?.upstreamAuth,
        ).not.toHaveProperty("secret");
        expect(
          mockUpdate.mock.calls[1]?.[0].body?.egressProxy,
        ).not.toHaveProperty("password");
      }
    },
  );

  it("discards an entered source secret when changing authentication schemes", async () => {
    const user = userEvent.setup();
    showSettings();
    await user.type(
      screen.getByLabelText("Upstream token"),
      "synthetic-do-not-reuse",
    );
    await user.click(screen.getByRole("radio", { name: /Basic/ }));
    expect(screen.getByLabelText("Upstream password")).toHaveValue("");
    await user.click(screen.getByRole("radio", { name: /Bearer token/ }));
    expect(screen.getByLabelText("Upstream token")).toHaveValue("");
  });

  it("allows retrying a connection test after a network rejection", async () => {
    const user = userEvent.setup();
    const testConnection = vi.mocked(testEgressProxy);
    testConnection.mockRejectedValueOnce(new Error("Connection unavailable"));
    testConnection.mockResolvedValueOnce({
      data: { reachable: true, latencyMs: 5, upstreamStatus: 200 },
    } as never);
    showSettings();
    const button = screen.getByRole("button", { name: "Test connection" });
    await user.click(button);
    await waitFor(() =>
      expect(screen.getByText("Connection unavailable")).toBeInTheDocument(),
    );
    await waitFor(() => expect(button).not.toHaveClass("ant-btn-loading"));
    await user.click(button);
    await waitFor(() => expect(testConnection).toHaveBeenCalledTimes(2));
    expect(await screen.findByText(/Proxy reachable/)).toBeInTheDocument();
    expect(
      screen.queryByText("Connection unavailable"),
    ).not.toBeInTheDocument();
  });
});
