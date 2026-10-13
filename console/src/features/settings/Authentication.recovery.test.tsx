import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useQueryClient } from "@tanstack/react-query";
import { useEffect } from "react";
import { afterEach, expect, it, vi } from "vitest";
import {
  getOidcSettings,
  replaceOidcSettings,
  type OidcSettings,
} from "../../client";
import { PreferencesProvider } from "../../lib/preferences";
import { ConsoleQueryProvider } from "../../lib/query/QueryProvider";
import { AuthenticationPage } from "./Authentication";

const auth = vi.hoisted(() => ({
  token: "admin-token",
  identity: { actor: "user:admin" } as { actor: string } | null,
  handleAuthFailure: vi.fn(),
}));
vi.mock("../../lib/auth", () => ({ useAuth: () => auth }));
vi.mock("../../client", async (original) => ({
  ...(await original<typeof import("../../client")>()),
  getOidcSettings: vi.fn(),
  replaceOidcSettings: vi.fn(),
  testOidcSettings: vi.fn(),
}));
const saved = {
  version: "4",
  source: "database",
  enabled: true,
  issuer: "https://id.example.com/realms/artifacts",
  audience: "gateway-api",
  clientId: "console",
  clientSecretConfigured: true,
  redirectUrl: "https://gateway.example.com/auth/oidc/callback",
  scopes: ["openid"],
  adminSubjects: [],
  memberRoles: [],
  adminRoles: [],
  provisioningMode: "jit",
  emailLinkingEnabled: false,
  jitDefaultRole: "none",
} as OidcSettings;
let queryClient: ReturnType<typeof useQueryClient>;
const oidcKey = ["settings", "oidc"];
function Capture() {
  const client = useQueryClient();
  useEffect(() => {
    queryClient = client;
  }, [client]);
  return null;
}
function Harness({ show = true }: { show?: boolean }) {
  return (
    <ConsoleQueryProvider>
      <Capture />
      <PreferencesProvider>
        {show && <AuthenticationPage />}
      </PreferencesProvider>
    </ConsoleQueryProvider>
  );
}
afterEach(() => {
  cleanup();
  vi.resetAllMocks();
  localStorage.clear();
  auth.token = "admin-token";
  auth.identity = { actor: "user:admin" };
});

it.each([false, true])(
  "ignores a late OIDC save after sign-out (new identity=%s)",
  async (newIdentity) => {
    const user = userEvent.setup();
    let finish!: (result: unknown) => void;
    vi.mocked(getOidcSettings).mockResolvedValue({ data: saved } as never);
    vi.mocked(replaceOidcSettings).mockImplementation(
      () =>
        new Promise<never>((resolve) => {
          finish = (result) => resolve(result as never);
        }),
    );
    const { rerender } = render(<Harness />);
    const audience = await screen.findByDisplayValue("gateway-api");
    await user.clear(audience);
    await user.type(audience, "admin-private-audience");
    await user.click(screen.getByRole("button", { name: /保存并应用/ }));
    await waitFor(() => expect(replaceOidcSettings).toHaveBeenCalledTimes(1));
    auth.token = "";
    auth.identity = null;
    rerender(<Harness show={false} />);
    await waitFor(() =>
      expect(queryClient.getQueryData(oidcKey)).toBeUndefined(),
    );
    const nextSettings = { ...saved, version: "6", audience: "bob-audience" };
    if (newIdentity) {
      auth.token = "bob-token";
      auth.identity = { actor: "user:bob" };
      vi.mocked(getOidcSettings).mockResolvedValue({
        data: nextSettings,
      } as never);
      rerender(<Harness />);
      await screen.findByDisplayValue("bob-audience");
    }
    await act(async () =>
      finish({
        data: { ...saved, version: "5", audience: "admin-private-audience" },
      }),
    );
    expect(queryClient.getQueryData(oidcKey)).toEqual(
      newIdentity ? nextSettings : undefined,
    );
  },
);

it("retains an OIDC draft after a failed background 5xx read", async () => {
  const user = userEvent.setup();
  vi.mocked(getOidcSettings).mockResolvedValue({ data: saved } as never);
  render(<Harness />);
  const audience = await screen.findByDisplayValue("gateway-api");
  await user.clear(audience);
  await user.type(audience, "unsaved-audience");
  vi.mocked(getOidcSettings).mockResolvedValue({
    error: { status: 503, message: "read unavailable" },
  } as never);
  await act(async () => {
    await queryClient.refetchQueries({ queryKey: oidcKey });
  });
  await screen.findByText("read unavailable");
  vi.mocked(getOidcSettings).mockResolvedValue({ data: saved } as never);
  await user.click(screen.getByRole("button", { name: /重试/ }));
  await waitFor(() =>
    expect(queryClient.getQueryState(oidcKey)?.error).toBeNull(),
  );
  expect(screen.getByDisplayValue("unsaved-audience")).toBeInTheDocument();
});
