import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { dryRunRepositoryRetention, getRetentionPolicy } from "../../client";
import type { Repository } from "../../client";
import { PreferencesProvider } from "../../lib/preferences";
import { RepositoryRetentionTab } from "./RepositoryRetentionTab";

vi.mock("../../client", () => ({
  dryRunRepositoryRetention: vi.fn(),
  executeRepositoryRetention: vi.fn(),
  getRetentionPolicy: vi.fn(),
  replaceRetentionPolicy: vi.fn(),
}));

vi.mock("../../lib/auth", () => ({
  useAuth: () => ({ token: "" }),
}));

const mockGetRetentionPolicy = vi.mocked(getRetentionPolicy);
const mockDryRunRepositoryRetention = vi.mocked(dryRunRepositoryRetention);
const repository: Repository = {
  id: "11111111-1111-4111-8111-111111111111",
  name: "packages",
  format: "npm",
  type: "hosted",
  anonymousRead: false,
  state: "active",
  version: "1",
};

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("RepositoryRetentionTab", () => {
  it.each([
    {
      format: "maven" as const,
      age: "Maven 发布版本保留天数",
      match: "只清理匹配 Maven 坐标",
      protect: "保护 Maven 坐标",
    },
    {
      format: "oci" as const,
      age: "镜像版本保留天数",
      match: "只清理匹配镜像",
      protect: "保护镜像版本",
    },
    {
      format: "conan" as const,
      age: "Recipe revision 保留天数",
      match: "只清理匹配 reference",
      protect: "保护 Conan 版本",
    },
    {
      format: "raw" as const,
      age: "资产未更新保留天数",
      match: "只清理匹配路径",
      protect: "保护路径",
    },
  ])("uses $format cleanup-unit terminology", async (expected) => {
    mockGetRetentionPolicy.mockResolvedValue({
      data: {
        version: "1",
        enabled: false,
        keepDays: 30,
        snapshotKeepDays: 14,
        minimumVersions: 1,
        maximumVersions: 0,
        coordinatePatterns: [],
        protectedPatterns: [],
      },
    } as never);

    render(
      <PreferencesProvider>
        <RepositoryRetentionTab
          repo={{ ...repository, format: expected.format }}
        />
      </PreferencesProvider>,
    );

    expect(await screen.findByText(expected.age)).toBeInTheDocument();
    expect(screen.getByText(expected.match)).toBeInTheDocument();
    expect(screen.getByText(expected.protect)).toBeInTheDocument();
  });

  it("uses npm package terminology instead of Maven coordinates", async () => {
    mockGetRetentionPolicy.mockResolvedValue({
      data: {
        version: "1",
        enabled: false,
        keepDays: 30,
        snapshotKeepDays: 30,
        minimumVersions: 1,
        maximumVersions: 0,
        coordinatePatterns: [],
        protectedPatterns: [],
      },
    } as never);

    render(
      <PreferencesProvider>
        <RepositoryRetentionTab repo={repository} />
      </PreferencesProvider>,
    );

    expect(await screen.findByText("npm 包版本保留天数")).toBeInTheDocument();
    expect(screen.getByText("每个 npm 包最少保留版本")).toBeInTheDocument();
    expect(screen.getByText("只清理匹配 npm 包")).toBeInTheDocument();
    expect(screen.getByText("保护 npm 包版本")).toBeInTheDocument();
    expect(screen.queryByText("保护 Maven 坐标")).not.toBeInTheDocument();
    expect(screen.queryByText(/groupId:artifactId/)).not.toBeInTheDocument();
  });

  it("uses PyPI project terminology instead of Maven coordinates", async () => {
    mockGetRetentionPolicy.mockResolvedValue({
      data: {
        version: "1",
        enabled: false,
        keepDays: 30,
        snapshotKeepDays: 30,
        minimumVersions: 1,
        maximumVersions: 0,
        coordinatePatterns: [],
        protectedPatterns: [],
      },
    } as never);

    render(
      <PreferencesProvider>
        <RepositoryRetentionTab repo={{ ...repository, format: "pypi" }} />
      </PreferencesProvider>,
    );

    expect(
      await screen.findByText("PyPI 项目版本保留天数"),
    ).toBeInTheDocument();
    expect(screen.getByText("每个 PyPI 项目最少保留版本")).toBeInTheDocument();
    expect(screen.getByText("只清理匹配 PyPI 项目")).toBeInTheDocument();
    expect(screen.getByText("保护 PyPI 项目版本")).toBeInTheDocument();
    expect(screen.queryByText("保护 Maven 坐标")).not.toBeInTheDocument();
  });

  it("labels npm dry-run candidates as package versions", async () => {
    const user = userEvent.setup();
    mockGetRetentionPolicy.mockResolvedValue({
      data: {
        version: "3",
        enabled: true,
        keepDays: 30,
        snapshotKeepDays: 30,
        minimumVersions: 1,
        maximumVersions: 0,
        coordinatePatterns: [],
        protectedPatterns: [],
      },
    } as never);
    mockDryRunRepositoryRetention.mockResolvedValue({
      data: {
        policyVersion: "3",
        totalCandidates: 1,
        summary: {
          reasonCounts: { age: 1, maximumVersions: 0 },
          versionTypeCounts: {
            release: 0,
            snapshot: 0,
            version: 1,
            asset: 0,
          },
          oldestCandidateAt: "2026-07-01T00:00:00Z",
        },
        candidates: [
          {
            format: "npm",
            coordinate: "@team/widget@1.0.0",
            digest: `sha256:${"a".repeat(64)}`,
            createdAt: "2026-07-01T00:00:00Z",
            reasons: ["age"],
            ageDays: 42,
            versionType: "version",
          },
        ],
      },
    } as never);

    render(
      <PreferencesProvider>
        <RepositoryRetentionTab repo={repository} />
      </PreferencesProvider>,
    );

    await user.click(await screen.findByRole("button", { name: "试运行" }));

    expect(await screen.findByText("npm 包版本")).toBeInTheDocument();
    expect(screen.queryByText("Recipe revision")).not.toBeInTheDocument();
  });
});
