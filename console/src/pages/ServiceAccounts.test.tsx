import {
  act,
  cleanup,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  createServiceAccount,
  createServiceAccountCredential,
  listServiceAccountCredentials,
  listServiceAccounts,
  revokeServiceAccountCredential,
  updateServiceAccount,
} from "../client";
import { AntdProvider } from "../app/AntdProvider";
import { PreferencesProvider } from "../lib/preferences";
import { ServiceAccountsPage } from "./ServiceAccounts";

vi.mock("../client", async () => {
  const actual = await vi.importActual<typeof import("../client")>("../client");
  return {
    ...actual,
    createServiceAccount: vi.fn(),
    createServiceAccountCredential: vi.fn(),
    listServiceAccounts: vi.fn(),
    listServiceAccountCredentials: vi.fn(),
    revokeServiceAccountCredential: vi.fn(),
    updateServiceAccount: vi.fn(),
  };
});

const accountId = "11111111-1111-4111-8111-111111111111";
const oldCredentialId = "22222222-2222-4222-8222-222222222222";
const newCredentialId = "33333333-3333-4333-8333-333333333333";
const activeAccount = {
  id: accountId,
  name: "pipeone-ci",
  description: "Publishes PipeOne release artifacts",
  state: "active" as const,
  createdAt: "2026-08-18T00:00:00Z",
  updatedAt: "2026-08-18T00:00:00Z",
  version: "3",
};
const disabledAccount = {
  ...activeAccount,
  state: "disabled" as const,
  version: "4",
};
const secondAccount = {
  ...activeAccount,
  id: "44444444-4444-4444-8444-444444444444",
  name: "release-bot",
};
const oldCredential = {
  id: oldCredentialId,
  serviceAccountId: accountId,
  name: "jenkins-blue",
  createdAt: "2026-08-18T00:00:00Z",
  expiresAt: "2026-11-16T00:00:00Z",
};
const newCredential = {
  id: newCredentialId,
  serviceAccountId: accountId,
  name: "jenkins-green",
  createdAt: "2026-08-18T01:00:00Z",
  expiresAt: "2026-11-16T01:00:00Z",
};

const mockListServiceAccounts = vi.mocked(listServiceAccounts);
const mockListServiceAccountCredentials = vi.mocked(
  listServiceAccountCredentials,
);
const mockCreateServiceAccount = vi.mocked(createServiceAccount);
const mockCreateServiceAccountCredential = vi.mocked(
  createServiceAccountCredential,
);
const mockRevokeServiceAccountCredential = vi.mocked(
  revokeServiceAccountCredential,
);
const mockUpdateServiceAccount = vi.mocked(updateServiceAccount);

function renderPage() {
  return render(
    <PreferencesProvider>
      <AntdProvider>
        <ServiceAccountsPage />
      </AntdProvider>
    </PreferencesProvider>,
  );
}

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("ServiceAccountsPage", () => {
  it("does not present a failed initial request as still loading", async () => {
    mockListServiceAccounts.mockResolvedValue({
      error: { message: "service account inventory unavailable" },
    } as never);

    renderPage();

    expect(await screen.findByText("请求出错")).toBeInTheDocument();
    expect(
      screen.getByText("service account inventory unavailable"),
    ).toBeInTheDocument();
    expect(screen.queryByText("加载服务账号…")).not.toBeInTheDocument();
  });

  it("turns a rejected initial request into a recoverable page error", async () => {
    mockListServiceAccounts.mockRejectedValue(
      new Error("service account request rejected"),
    );

    renderPage();

    expect(await screen.findByText("请求出错")).toBeInTheDocument();
    expect(
      screen.getByText("service account request rejected"),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /重试/ })).toBeInTheDocument();
  });

  it("explains when the connected Gateway does not expose service accounts", async () => {
    mockListServiceAccounts.mockResolvedValue({
      error: "404 page not found",
    } as never);

    renderPage();

    expect(
      await screen.findByText(
        "当前 Gateway 未提供此接口，Console 与 Gateway 版本可能不一致。请更新或重启 Gateway 后重试。",
      ),
    ).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /重试/ })).toBeInTheDocument();
  });

  it("presents one stable machine principal separately from its rotating credentials", async () => {
    mockListServiceAccounts.mockResolvedValue({
      data: { items: [activeAccount] },
    } as never);
    mockListServiceAccountCredentials.mockResolvedValue({
      data: { items: [oldCredential] },
    } as never);

    renderPage();

    expect(await screen.findAllByText("pipeone-ci")).toHaveLength(2);
    expect(screen.getByText("机器身份与凭据分离")).toBeInTheDocument();
    expect(
      screen.getByText(`service-account:${accountId}`),
    ).toBeInTheDocument();
    expect(await screen.findByText("jenkins-blue")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /签发新凭据/ })).toBeEnabled();
  });

  it("loads additional machine principals and credential history through bounded pages", async () => {
    const user = userEvent.setup();
    mockListServiceAccounts
      .mockResolvedValueOnce({
        data: { items: [activeAccount], nextPageToken: "accounts-page-2" },
      } as never)
      .mockResolvedValueOnce({
        data: { items: [secondAccount] },
      } as never);
    mockListServiceAccountCredentials
      .mockResolvedValueOnce({
        data: { items: [oldCredential], nextPageToken: "credentials-page-2" },
      } as never)
      .mockResolvedValueOnce({
        data: { items: [newCredential] },
      } as never);

    renderPage();

    await user.click(
      await screen.findByRole("button", { name: "加载更多账号" }),
    );
    expect(await screen.findByText("release-bot")).toBeInTheDocument();
    expect(mockListServiceAccounts).toHaveBeenLastCalledWith({
      query: { pageSize: 200, pageToken: "accounts-page-2" },
    });

    await user.click(
      await screen.findByRole("button", { name: "加载更多凭据" }),
    );
    expect(await screen.findByText("jenkins-green")).toBeInTheDocument();
    expect(mockListServiceAccountCredentials).toHaveBeenLastCalledWith({
      path: { serviceAccountId: accountId },
      query: { pageSize: 200, pageToken: "credentials-page-2" },
    });
  });

  it("does not replace the selected account with a slower previous credential response", async () => {
    const user = userEvent.setup();
    let resolveFirst!: (value: unknown) => void;
    const firstResponse = new Promise((resolve) => {
      resolveFirst = resolve;
    });
    const secondCredential = {
      ...newCredential,
      serviceAccountId: secondAccount.id,
      name: "release-green",
    };
    mockListServiceAccounts.mockResolvedValue({
      data: { items: [activeAccount, secondAccount] },
    } as never);
    mockListServiceAccountCredentials
      .mockReturnValueOnce(firstResponse as never)
      .mockResolvedValueOnce({
        data: { items: [secondCredential] },
      } as never);

    renderPage();

    await user.click(await screen.findByText("release-bot"));
    expect(await screen.findByText("release-green")).toBeInTheDocument();

    await act(async () => {
      resolveFirst({ data: { items: [oldCredential] } });
      await firstResponse;
    });
    expect(screen.queryByText("jenkins-blue")).not.toBeInTheDocument();
    expect(screen.getByText("release-green")).toBeInTheDocument();
  });

  it("creates the first stable machine identity from the empty state", async () => {
    const user = userEvent.setup();
    mockListServiceAccounts
      .mockResolvedValueOnce({ data: { items: [] } } as never)
      .mockResolvedValue({ data: { items: [activeAccount] } } as never);
    mockListServiceAccountCredentials.mockResolvedValue({
      data: { items: [] },
    } as never);
    mockCreateServiceAccount.mockResolvedValue({
      data: activeAccount,
    } as never);

    renderPage();

    expect(await screen.findByText("暂无服务账号")).toBeInTheDocument();
    await user.click(
      screen.getAllByRole("button", { name: "新建服务账号" }).at(-1)!,
    );
    const dialog = await screen.findByRole("dialog");
    const fields = within(dialog).getAllByRole("textbox");
    await user.type(fields[0], "  pipeone-ci  ");
    await user.type(fields[1], "Publishes PipeOne release artifacts");
    await user.click(within(dialog).getByRole("button", { name: /创\s*建/ }));

    await waitFor(() =>
      expect(mockCreateServiceAccount).toHaveBeenCalledWith({
        body: {
          name: "pipeone-ci",
          description: "Publishes PipeOne release artifacts",
        },
      }),
    );
    expect(
      await screen.findByText(`service-account:${accountId}`),
    ).toBeInTheDocument();
  });

  it("issues, reveals, and revokes credentials without changing the grant principal", async () => {
    const user = userEvent.setup();
    mockListServiceAccounts.mockResolvedValue({
      data: { items: [activeAccount] },
    } as never);
    mockListServiceAccountCredentials
      .mockResolvedValueOnce({ data: { items: [oldCredential] } } as never)
      .mockResolvedValueOnce({
        data: { items: [oldCredential, newCredential] },
      } as never)
      .mockResolvedValue({ data: { items: [newCredential] } } as never);
    mockCreateServiceAccountCredential.mockResolvedValue({
      data: { ...newCredential, token: "agc_once_only_secret" },
    } as never);
    mockRevokeServiceAccountCredential.mockResolvedValue({} as never);

    renderPage();

    await user.click(await screen.findByRole("button", { name: /签发新凭据/ }));
    const issueDialog = await screen.findByRole("dialog");
    await user.type(within(issueDialog).getByRole("textbox"), "jenkins-green");
    await user.click(
      within(issueDialog).getByRole("button", { name: /签\s*发/ }),
    );

    expect(await screen.findByText("agc_once_only_secret")).toBeInTheDocument();
    expect(mockCreateServiceAccountCredential).toHaveBeenCalledWith(
      expect.objectContaining({
        path: { serviceAccountId: accountId },
        body: expect.objectContaining({ name: "jenkins-green" }),
      }),
    );
    await user.click(screen.getByRole("button", { name: /我已安全保存/ }));
    expect(
      screen.getByText(`service-account:${accountId}`),
    ).toBeInTheDocument();

    await user.click(
      await screen.findByRole("button", { name: "吊销 jenkins-blue" }),
    );
    const revokeDialog = await screen.findByRole("dialog");
    await user.click(
      within(revokeDialog).getByRole("button", { name: /确\s*认\s*吊\s*销/ }),
    );

    await waitFor(() =>
      expect(mockRevokeServiceAccountCredential).toHaveBeenCalledWith({
        path: {
          serviceAccountId: accountId,
          credentialId: oldCredentialId,
        },
      }),
    );
    expect(await screen.findByText("jenkins-green")).toBeInTheDocument();
  });

  it("disables and re-enables every credential through the stable account state", async () => {
    const user = userEvent.setup();
    mockListServiceAccounts
      .mockResolvedValueOnce({ data: { items: [activeAccount] } } as never)
      .mockResolvedValueOnce({ data: { items: [disabledAccount] } } as never)
      .mockResolvedValue({ data: { items: [activeAccount] } } as never);
    mockListServiceAccountCredentials.mockResolvedValue({
      data: { items: [oldCredential] },
    } as never);
    mockUpdateServiceAccount.mockResolvedValue({} as never);

    renderPage();

    await user.click(
      await screen.findByRole("button", { name: /禁\s*用\s*账\s*号/ }),
    );
    await user.click(
      within(await screen.findByRole("dialog")).getByRole("button", {
        name: /确\s*认\s*禁\s*用/,
      }),
    );
    await waitFor(() =>
      expect(mockUpdateServiceAccount).toHaveBeenNthCalledWith(1, {
        path: { serviceAccountId: accountId },
        headers: { "If-Match": "3" },
        body: { state: "disabled" },
      }),
    );

    const enable = await screen.findByRole("button", {
      name: /重\s*新\s*启\s*用/,
    });
    expect(screen.getByRole("button", { name: /签发新凭据/ })).toBeDisabled();
    await user.click(enable);
    await user.click(
      within(await screen.findByRole("dialog")).getByRole("button", {
        name: /确\s*认\s*启\s*用/,
      }),
    );
    await waitFor(() =>
      expect(mockUpdateServiceAccount).toHaveBeenNthCalledWith(2, {
        path: { serviceAccountId: accountId },
        headers: { "If-Match": "4" },
        body: { state: "active" },
      }),
    );
    expect(
      await screen.findByRole("button", { name: /禁\s*用\s*账\s*号/ }),
    ).toBeInTheDocument();
  });
});
