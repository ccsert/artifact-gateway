import { cleanup, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, expect, it, vi } from "vitest";
import { searchArtifacts } from "../client";
import { PreferencesProvider } from "../lib/preferences";
import { SearchPage } from "./Search";

vi.mock("../client", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../client")>()),
  searchArtifacts: vi.fn(),
}));

vi.mock("../lib/auth", () => ({
  useAuth: () => ({ token: "operator-token" }),
}));

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

it("presents Raw global-search hits with a readable file path", async () => {
  vi.mocked(searchArtifacts).mockResolvedValue({
    data: {
      searchedRepositories: 1,
      items: [
        {
          repositoryId: "33333333-3333-4333-8333-333333333333",
          repositoryName: "raw-releases",
          format: "raw",
          matchKind: "coordinate",
          coordinate: "ChatGPT%20Image%20%282%29.png",
          digest: `sha256:${"c".repeat(64)}`,
          size: 1024,
        },
      ],
    },
  } as never);

  render(
    <MemoryRouter initialEntries={["/search?q=ChatGPT%20Image"]}>
      <PreferencesProvider>
        <SearchPage />
      </PreferencesProvider>
    </MemoryRouter>,
  );

  expect(await screen.findByText("ChatGPT Image (2).png")).toBeInTheDocument();
  expect(
    screen.queryByText("ChatGPT%20Image%20%282%29.png"),
  ).not.toBeInTheDocument();
});
