import {
  act,
  cleanup,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  createRepositoryPromotion,
  deleteRepositoryReplication,
  evaluateSecurityPolicy,
  listRepositories,
  listRepositoryArtifactIdentities,
  listRepositoryReplications,
} from "../../client";
import type { Repository } from "../../client";
import { PreferencesProvider } from "../../lib/preferences";
import { RepositoryDistributionTab } from "./RepositoryDistributionTab";
import { TestQueryProvider } from "../../test/queryClient";
import { getRepositoryReplication } from "../../client";

vi.mock("../../client", () => ({
  createRepositoryPromotion: vi.fn(),
  createRepositoryReplication: vi.fn(),
  deleteRepositoryReplication: vi.fn(),
  evaluateSecurityPolicy: vi.fn(),
  getRepositoryReplication: vi.fn(),
  listRepositories: vi.fn(),
  listRepositoryArtifactIdentities: vi.fn(),
  listRepositoryReplications: vi.fn(),
}));

const mockCreatePromotion = vi.mocked(createRepositoryPromotion);
const mockEvaluateSecurityPolicy = vi.mocked(evaluateSecurityPolicy);
const mockListRepositories = vi.mocked(listRepositories);
const mockListRepositoryReplications = vi.mocked(listRepositoryReplications);
const mockListArtifactIdentities = vi.mocked(listRepositoryArtifactIdentities);
const digestA = `sha256:${"a".repeat(64)}`;
const digestB = `sha256:${"b".repeat(64)}`;
const coordinate = "org.example:widget:1.2.3";

const source: Repository = {
  id: "11111111-1111-4111-8111-111111111111",
  name: "staging",
  format: "maven",
  type: "hosted",
  anonymousRead: false,
  mavenStrictPublication: false,
  state: "active",
  version: "1",
};

const target: Repository = {
  ...source,
  id: "22222222-2222-4222-8222-222222222222",
  name: "releases",
};
const proxyTarget: Repository = {
  ...target,
  id: "33333333-3333-4333-8333-333333333333",
  name: "central-proxy",
  type: "proxy",
};

beforeEach(() => {
  mockListArtifactIdentities.mockResolvedValue({
    data: {
      items: [
        {
          coordinate,
          digest: digestA,
          size: 2048,
          publishedAt: "2026-08-12T05:00:00Z",
        },
      ],
    },
  } as never);
});

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("RepositoryDistributionTab", () => {
  it.each([
    ["oci", "library/nginx"],
    ["conan", "zlib/1.3.1@company/stable#recipe-revision"],
    ["go", "example.com/team/gateway-widget@v1.2.3"],
  ] as const)(
    "shows a valid %s canonical-coordinate example in advanced input",
    async (format, placeholder) => {
      const user = userEvent.setup();
      const repository = { ...source, format };
      mockListRepositories.mockResolvedValue({
        data: { items: [repository], nextPageToken: "" },
      } as never);
      mockListRepositoryReplications.mockResolvedValue({ data: [] } as never);

      render(
        <TestQueryProvider>
          <PreferencesProvider>
            <RepositoryDistributionTab repo={repository} />
          </PreferencesProvider>
        </TestQueryProvider>,
      );

      await screen.findByText("暂无复制计划");
      await user.click(screen.getByRole("button", { name: "高级手动输入" }));
      expect(screen.getByRole("textbox", { name: "制品坐标" })).toHaveAttribute(
        "placeholder",
        placeholder,
      );
    },
  );

  it("previews target security admission before promotion", async () => {
    const user = userEvent.setup();
    mockListRepositories.mockResolvedValue({
      data: { items: [source, target], nextPageToken: "" },
    } as never);
    mockListRepositoryReplications.mockResolvedValue({ data: [] } as never);
    mockEvaluateSecurityPolicy.mockResolvedValue({
      data: {
        allowed: true,
        enforced: true,
        policyVersion: "3",
        intelligencePresent: true,
        reasons: [],
      },
    } as never);

    render(
      <TestQueryProvider>
        <PreferencesProvider>
          <RepositoryDistributionTab repo={source} />
        </PreferencesProvider>
      </TestQueryProvider>,
    );

    await screen.findByText("暂无复制计划");
    await user.click(screen.getByRole("button", { name: "高级手动输入" }));
    await user.type(
      screen.getByRole("textbox", { name: "制品坐标" }),
      coordinate,
    );
    await user.type(
      screen.getByRole("textbox", { name: "摘要 digest" }),
      digestA,
    );
    await user.click(screen.getByRole("combobox", { name: "选择目标仓库" }));
    await user.click(await screen.findByText("releases"));
    await user.click(screen.getByRole("button", { name: "评估准入" }));

    await waitFor(() => expect(mockEvaluateSecurityPolicy).toHaveBeenCalled());
    expect(mockEvaluateSecurityPolicy).toHaveBeenCalledWith({
      path: { repositoryId: target.id },
      body: {
        sourceRepositoryId: source.id,
        coordinate,
        digest: digestA,
      },
    });
    expect(await screen.findByText("安全策略允许晋升")).toBeInTheDocument();
    expect(screen.getByText("策略版本: 3")).toBeInTheDocument();
  });

  it("promotes a selected artifact without exposing incompatible targets", async () => {
    const user = userEvent.setup();
    mockListRepositories.mockResolvedValue({
      data: { items: [source, target, proxyTarget], nextPageToken: "" },
    } as never);
    mockListRepositoryReplications.mockResolvedValue({ data: [] } as never);
    mockCreatePromotion.mockResolvedValue({ data: {} } as never);

    render(
      <TestQueryProvider>
        <PreferencesProvider>
          <RepositoryDistributionTab repo={source} />
        </PreferencesProvider>
      </TestQueryProvider>,
    );

    await screen.findByText("暂无复制计划");
    await waitFor(() =>
      expect(mockListArtifactIdentities).toHaveBeenCalledWith({
        path: { repositoryId: source.id },
        query: { purpose: "distribution", pageSize: 50 },
        signal: expect.any(AbortSignal),
      }),
    );
    await user.click(
      screen.getByRole("combobox", { name: "搜索并选择源制品" }),
    );
    await user.click(
      await screen.findByText(coordinate, {
        selector: ".ant-select-item-option-content *",
      }),
    );
    await user.click(screen.getByRole("combobox", { name: "选择目标仓库" }));
    expect(await screen.findByText("releases")).toBeInTheDocument();
    expect(screen.queryByText("central-proxy")).not.toBeInTheDocument();
    await user.click(screen.getByText("releases"));
    await user.click(screen.getByRole("button", { name: /晋\s*升/ }));

    await waitFor(() => expect(mockCreatePromotion).toHaveBeenCalledTimes(1));
    expect(mockCreatePromotion).toHaveBeenCalledWith({
      path: { repositoryId: source.id },
      body: {
        targetRepositoryId: target.id,
        coordinate,
        digest: digestA,
      },
      headers: { "Idempotency-Key": expect.any(String) },
    });
    expect(
      await screen.findByText(
        "晋升任务已提交，请在目标仓库的「生命周期任务」查看进度",
      ),
    ).toBeInTheDocument();
  });

  it("blocks promotion and replication when the artifact is quarantined", async () => {
    const user = userEvent.setup();
    mockListRepositories.mockResolvedValue({
      data: { items: [source, target], nextPageToken: "" },
    } as never);
    mockListRepositoryReplications.mockResolvedValue({ data: [] } as never);
    mockEvaluateSecurityPolicy.mockResolvedValue({
      data: {
        allowed: false,
        enforced: true,
        policyVersion: "4",
        intelligencePresent: true,
        reasons: ["artifact_quarantined"],
      },
    } as never);

    render(
      <TestQueryProvider>
        <PreferencesProvider>
          <RepositoryDistributionTab repo={source} />
        </PreferencesProvider>
      </TestQueryProvider>,
    );

    await screen.findByText("暂无复制计划");
    await user.click(
      screen.getByRole("combobox", { name: "搜索并选择源制品" }),
    );
    await user.click(
      await screen.findByText(coordinate, {
        selector: ".ant-select-item-option-content *",
      }),
    );
    await user.click(screen.getByRole("combobox", { name: "选择目标仓库" }));
    await user.click(await screen.findByText("releases"));
    await user.click(screen.getByRole("button", { name: "评估准入" }));

    expect(
      await screen.findByText("制品已隔离，无法晋升或复制"),
    ).toBeInTheDocument();
    // Quarantine is an ongoing state an operation can lift, not a system error.
    const quarantineAlert = screen
      .getByText("制品已隔离，无法晋升或复制")
      .closest(".ant-alert");
    expect(quarantineAlert).toHaveClass("ant-alert-warning");
    expect(quarantineAlert).not.toHaveClass("ant-alert-error");
    expect(
      screen.getByText("请先在制品详情中解除隔离，然后重新评估准入。"),
    ).toBeInTheDocument();
    expect(screen.getByText("制品已隔离；请先解除隔离")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /晋\s*升/ })).toBeDisabled();
    expect(screen.getByRole("button", { name: /复\s*制/ })).toBeDisabled();
  });

  it("keeps ordinary security policy rejection scoped to promotion", async () => {
    const user = userEvent.setup();
    mockListRepositories.mockResolvedValue({
      data: { items: [source, target], nextPageToken: "" },
    } as never);
    mockListRepositoryReplications.mockResolvedValue({ data: [] } as never);
    mockEvaluateSecurityPolicy.mockResolvedValue({
      data: {
        allowed: false,
        enforced: true,
        policyVersion: "5",
        intelligencePresent: true,
        reasons: ["verified_signature_required"],
      },
    } as never);
    mockListArtifactIdentities.mockResolvedValue({
      data: {
        items: [
          {
            coordinate,
            digest: digestB,
            size: 2048,
            publishedAt: "2026-08-12T05:00:00Z",
          },
        ],
      },
    } as never);

    render(
      <TestQueryProvider>
        <PreferencesProvider>
          <RepositoryDistributionTab repo={source} />
        </PreferencesProvider>
      </TestQueryProvider>,
    );

    await screen.findByText("暂无复制计划");
    await user.click(
      screen.getByRole("combobox", { name: "搜索并选择源制品" }),
    );
    await user.click(
      await screen.findByText(coordinate, {
        selector: ".ant-select-item-option-content *",
      }),
    );
    await user.click(screen.getByRole("combobox", { name: "选择目标仓库" }));
    await user.click(await screen.findByText("releases"));
    await user.click(screen.getByRole("button", { name: "评估准入" }));

    expect(await screen.findByText("安全策略阻止晋升")).toBeInTheDocument();
    // A policy judgment is a warning; only a failed request reads as an error.
    const policyAlert = screen
      .getByText("安全策略阻止晋升")
      .closest(".ant-alert");
    expect(policyAlert).toHaveClass("ant-alert-warning");
    expect(policyAlert).not.toHaveClass("ant-alert-error");
    expect(screen.getByText("缺少已验证签名")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /晋\s*升/ })).toBeDisabled();
    expect(screen.getByRole("button", { name: /复\s*制/ })).toBeEnabled();
  });
});

it("uses visible APT packages and requires the target suite, preserving an uncertain request key", async () => {
  const user = userEvent.setup();
  const apt = { ...source, format: "apt" as const };
  const poolPath = "pool/main/w/widget/widget_1.0_amd64.deb";
  mockListRepositories.mockResolvedValue({
    data: { items: [apt, { ...target, format: "apt" }] },
  } as never);
  mockListRepositoryReplications.mockResolvedValue({ data: [] } as never);
  mockCreatePromotion
    .mockRejectedValueOnce(new Error("connection lost"))
    .mockResolvedValueOnce({ data: {} } as never);
  render(
    <TestQueryProvider>
      <PreferencesProvider>
        <RepositoryDistributionTab
          repo={apt}
          aptPackages={[
            {
              publicationSessionId: "session-a",
              component: "main",
              poolPath,
              revision: {
                id: "rev-a",
                repositoryId: apt.id,
                package: "widget",
                version: "1.0",
                architecture: "amd64",
                canonicalIdentity: "widget@1.0#amd64",
                digest: digestA,
                size: 2048,
                objectName: "native/apt/a",
                publisher: "operator",
                createdAt: "2026-01-01T00:00:00Z",
              },
            },
          ]}
        />
      </PreferencesProvider>
    </TestQueryProvider>,
  );
  await screen.findByText("暂无复制计划");
  expect(mockListArtifactIdentities).not.toHaveBeenCalled();
  await user.click(screen.getByRole("combobox", { name: "搜索并选择源制品" }));
  await user.click(await screen.findByText("widget 1.0 · amd64"));
  await user.click(screen.getByRole("combobox", { name: "选择目标仓库" }));
  await user.click(await screen.findByText("releases"));
  expect(screen.getByRole("button", { name: /晋\s*升/ })).toBeDisabled();
  await user.type(
    screen.getByRole("textbox", { name: "目标发行套件" }),
    "bookworm",
  );
  await user.click(screen.getByRole("button", { name: /晋\s*升/ }));
  await screen.findByText("connection lost");
  await user.click(screen.getByRole("button", { name: /晋\s*升/ }));
  await screen.findByText(
    "晋升任务已提交，请在目标仓库的「生命周期任务」查看进度",
  );
  const calls = mockCreatePromotion.mock.calls;
  expect(calls).toHaveLength(2);
  expect(calls[0][0]).toEqual(calls[1][0]);
  expect(calls[0][0]?.body).toEqual({
    targetRepositoryId: target.id,
    coordinate: poolPath,
    digest: digestA,
    aptTargetSuite: "bookworm",
  });
});

it("opens a plan's progress, reports a failed read with Retry, and reads again", async () => {
  const user = userEvent.setup();
  const plan = {
    id: "plan-1",
    sourceRepositoryId: source.id,
    targetRepositoryId: target.id,
    format: "maven" as const,
    coordinate,
    digest: digestA,
    state: "running" as const,
    createdAt: "2026-08-12T05:00:00Z",
  };
  mockListRepositories.mockResolvedValue({
    data: { items: [source, target], nextPageToken: "" },
  } as never);
  mockListRepositoryReplications.mockResolvedValue({ data: [plan] } as never);
  vi.mocked(getRepositoryReplication)
    .mockResolvedValueOnce({
      error: { status: 503, message: "detail unavailable" },
    } as never)
    .mockResolvedValue({
      data: {
        ...plan,
        checkpoints: [
          {
            objectKey: "maven/widget-1.2.3.jar",
            digest: digestA,
            size: 2048,
            byteOffset: 1024,
            state: "copying",
            attempts: 1,
          },
        ],
      },
    } as never);

  render(
    <TestQueryProvider>
      <PreferencesProvider>
        <RepositoryDistributionTab repo={source} />
      </PreferencesProvider>
    </TestQueryProvider>,
  );

  await user.click(await screen.findByRole("button", { name: /进\s*度/ }));
  expect(await screen.findByText("detail unavailable")).toBeVisible();
  expect(screen.queryByRole("dialog")).not.toBeInTheDocument();

  await user.click(screen.getByRole("button", { name: /重\s*试/ }));
  expect(await screen.findByRole("dialog")).toBeInTheDocument();
  expect(await screen.findByText("maven/widget-1.2.3.jar")).toBeInTheDocument();
  expect(screen.queryByText("detail unavailable")).not.toBeInTheDocument();
  expect(getRepositoryReplication).toHaveBeenLastCalledWith(
    expect.objectContaining({
      path: { repositoryId: source.id, replicationPlanId: "plan-1" },
      signal: expect.any(AbortSignal),
    }),
  );
  await user.click(
    within(screen.getByRole("dialog")).getByRole("button", { name: "Close" }),
  );
  await waitFor(() =>
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
  );
  vi.mocked(getRepositoryReplication).mockResolvedValueOnce({
    error: { status: 503, message: "Reopened progress unavailable" },
  } as never);
  await user.click(screen.getByRole("button", { name: /进\s*度/ }));
  const reopened = within(await screen.findByRole("dialog"));
  expect(
    await reopened.findByText("Reopened progress unavailable"),
  ).toBeInTheDocument();
  expect(reopened.getByText("maven/widget-1.2.3.jar")).toBeInTheDocument();
  await user.click(reopened.getByRole("button", { name: /重\s*试/ }));
  await waitFor(() =>
    expect(
      reopened.queryByText("Reopened progress unavailable"),
    ).not.toBeInTheDocument(),
  );
});

it.each(["loaded", "pending"])(
  "refreshes %s progress opened while cancelling a plan after the write completes",
  async (readState) => {
    const user = userEvent.setup();
    const plan = {
      id: "plan-cancel",
      sourceRepositoryId: source.id,
      targetRepositoryId: target.id,
      format: "maven" as const,
      coordinate,
      digest: digestA,
      state: "pending" as const,
      createdAt: "2026-08-12T05:00:00Z",
    };
    mockListRepositories.mockResolvedValue({
      data: { items: [source, target] },
    } as never);
    mockListRepositoryReplications.mockResolvedValue({ data: [plan] } as never);
    let cancelled = false;
    let finish!: (value: never) => void;
    let finishDetail: ((value: never) => void) | undefined;
    vi.mocked(deleteRepositoryReplication).mockImplementationOnce(
      () =>
        new Promise<never>((resolve) => {
          finish = resolve;
        }),
    );
    vi.mocked(getRepositoryReplication).mockImplementation(async () => {
      if (!cancelled && readState === "pending")
        return new Promise<never>((resolve) => {
          finishDetail = resolve;
        });
      return {
        data: {
          ...plan,
          state: cancelled ? "cancelled" : "pending",
          checkpoints: [],
          lastError: cancelled ? "Cancellation confirmed" : undefined,
        },
      } as never;
    });
    render(
      <TestQueryProvider>
        <PreferencesProvider>
          <RepositoryDistributionTab repo={source} />
        </PreferencesProvider>
      </TestQueryProvider>,
    );
    await user.click(await screen.findByRole("button", { name: /取\s*消/ }));
    await user.click(screen.getByRole("button", { name: "确认取消" }));
    await waitFor(() =>
      expect(deleteRepositoryReplication).toHaveBeenCalledTimes(1),
    );
    await user.click(screen.getByRole("button", { name: /进\s*度/ }));
    if (readState === "loaded") await screen.findByRole("dialog");
    else await waitFor(() => expect(finishDetail).toBeDefined());
    cancelled = true;
    await act(async () => {
      finish({} as never);
    });
    const confirmation = await within(
      await screen.findByRole("dialog"),
    ).findByText("Cancellation confirmed");
    await waitFor(() => expect(confirmation).toBeVisible());
    await act(async () => {
      finishDetail?.({ data: { ...plan, checkpoints: [] } } as never);
    });
    expect(confirmation).toBeVisible();
  },
);
