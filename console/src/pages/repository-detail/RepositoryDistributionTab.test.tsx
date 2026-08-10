import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  evaluateSecurityPolicy,
  listRepositories,
  listRepositoryReplications,
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
}));

const mockEvaluateSecurityPolicy = vi.mocked(evaluateSecurityPolicy);
const mockListRepositories = vi.mocked(listRepositories);
const mockListRepositoryReplications = vi.mocked(listRepositoryReplications);

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
    await user.click(screen.getByRole("combobox"));
    await user.click(await screen.findByText("releases"));
    await user.type(
      screen.getByPlaceholderText("org.example:gateway-widget:1.2.3"),
      "org.example:widget:1.2.3",
    );
    const digest =
      "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa";
    await user.type(screen.getByPlaceholderText("sha256:…"), digest);
    await user.click(screen.getByRole("button", { name: "评估准入" }));

    await waitFor(() => expect(mockEvaluateSecurityPolicy).toHaveBeenCalled());
    expect(mockEvaluateSecurityPolicy).toHaveBeenCalledWith({
      path: { repositoryId: target.id },
      body: {
        sourceRepositoryId: source.id,
        coordinate: "org.example:widget:1.2.3",
        digest,
      },
    });
    expect(await screen.findByText("安全策略允许晋升")).toBeInTheDocument();
    expect(screen.getByText("策略版本: 3")).toBeInTheDocument();
  });
});
