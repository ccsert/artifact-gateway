import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  createRepositoryPromotion,
  evaluateSecurityPolicy,
  listRepositories,
  listRepositoryReplications,
  searchRepositoryArtifacts,
} from "../../client";
import type { Repository } from "../../client";
import { PreferencesProvider } from "../../lib/preferences";
import { RepositoryDistributionTab } from "./RepositoryDistributionTab";

vi.mock("../../client", () => ({
  createRepositoryPromotion: vi.fn(),
  createRepositoryReplication: vi.fn(),
  deleteRepositoryReplication: vi.fn(),
  evaluateSecurityPolicy: vi.fn(),
  getRepositoryReplication: vi.fn(),
  listRepositories: vi.fn(),
  listRepositoryReplications: vi.fn(),
  searchRepositoryArtifacts: vi.fn(),
}));

const mockCreatePromotion = vi.mocked(createRepositoryPromotion);
const mockEvaluateSecurityPolicy = vi.mocked(evaluateSecurityPolicy);
const mockListRepositories = vi.mocked(listRepositories);
const mockListRepositoryReplications = vi.mocked(listRepositoryReplications);
const mockSearchArtifacts = vi.mocked(searchRepositoryArtifacts);
const digestA = `sha256:${"a".repeat(64)}`;
const digestB = `sha256:${"b".repeat(64)}`;
const coordinate = "org.example:widget:1.2.3";

const source: Repository = {
  id: "11111111-1111-4111-8111-111111111111",
  name: "staging",
  format: "maven",
  type: "hosted",
  anonymousRead: false,
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
  mockSearchArtifacts.mockResolvedValue({
    data: {
      items: [{ coordinate, digest: digestA, size: 2048 }],
    },
  } as never);
});

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("RepositoryDistributionTab", () => {
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
      <PreferencesProvider>
        <RepositoryDistributionTab repo={source} />
      </PreferencesProvider>,
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
      <PreferencesProvider>
        <RepositoryDistributionTab repo={source} />
      </PreferencesProvider>,
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
      await screen.findByText("晋升任务已提交，请在「生命周期任务」查看进度"),
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
      <PreferencesProvider>
        <RepositoryDistributionTab repo={source} />
      </PreferencesProvider>,
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
    mockSearchArtifacts.mockResolvedValue({
      data: { items: [{ coordinate, digest: digestB, size: 2048 }] },
    } as never);

    render(
      <PreferencesProvider>
        <RepositoryDistributionTab repo={source} />
      </PreferencesProvider>,
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
    expect(screen.getByText("缺少已验证签名")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /晋\s*升/ })).toBeDisabled();
    expect(screen.getByRole("button", { name: /复\s*制/ })).toBeEnabled();
  });
});
