import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { searchRepositoryArtifacts } from "../../client";
import type { Repository } from "../../client";
import { PreferencesProvider } from "../../lib/preferences";
import { RepositoryArtifactsTab } from "./RepositoryArtifactsTab";

vi.mock("../../client", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../client")>()),
  searchRepositoryArtifacts: vi.fn(),
}));

vi.mock("../../lib/auth", () => ({
  useAuth: () => ({ token: "resolver-secret" }),
}));

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("Cargo Hosted artifact view", () => {
  it("opens a crate from repository search and keeps an exact version link", async () => {
    vi.mocked(searchRepositoryArtifacts).mockResolvedValue({
      data: {
        items: [
          { coordinate: "demo-crate", version: "1.2.3", versionCount: 2 },
        ],
      },
    } as never);
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(
        JSON.stringify({
          name: "demo-crate",
          vers: "1.2.3",
          cksum: "a".repeat(64),
          yanked: true,
        }) + "\n",
        { status: 200 },
      ),
    );
    const repository: Repository = {
      id: "11111111-1111-4111-8111-111111111111",
      name: "cargo-internal",
      format: "cargo" as Repository["format"],
      type: "hosted",
      anonymousRead: false,
      mavenStrictPublication: false,
      state: "active",
      version: "1",
    };
    render(
      <PreferencesProvider>
        <RepositoryArtifactsTab
          repo={repository}
          canWrite={false}
          artifactTarget="demo-crate"
          versionTarget="1.2.3"
        />
      </PreferencesProvider>,
    );

    expect(await screen.findByText("demo-crate")).toBeInTheDocument();
    expect(await screen.findByText("yanked")).toBeInTheDocument();
    expect(fetchMock).toHaveBeenCalledWith(
      "/cargo/cargo-internal/de/mo/demo-crate",
      expect.any(Object),
    );
  });
});
