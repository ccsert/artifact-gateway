import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { clearProxyNegativeCache, invalidateProxyCache } from "../../client";
import { PreferencesProvider } from "../../lib/preferences";
import { TestQueryProvider } from "../../test/queryClient";
import { ProxyMavenInvalidateCard } from "./ProxyMavenInvalidateCard";
import { ProxyMavenWarmCard } from "./ProxyMavenWarmCard";

vi.mock("../../client", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../client")>()),
  clearProxyNegativeCache: vi.fn(),
  invalidateProxyCache: vi.fn(),
  refreshProxyCache: vi.fn(),
}));

const repoId = "00000000-0000-4000-8000-000000000001";

function renderCard(card: React.ReactNode) {
  return render(
    <TestQueryProvider>
      <PreferencesProvider>{card}</PreferencesProvider>
    </TestQueryProvider>,
  );
}

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
  vi.restoreAllMocks();
});

describe("Maven proxy cache cards", () => {
  it("warms a GAV through the repository's Maven path with the session token", async () => {
    const fetchMock = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValue(new Response("", { status: 200 }));
    const onWarmed = vi.fn();
    renderCard(
      <ProxyMavenWarmCard
        repoId={repoId}
        repoName="maven-central"
        token="session-token"
        onWarmed={onWarmed}
      />,
    );

    // The field starts with an example coordinate; replace it.
    const input = screen.getByPlaceholderText(
      "org.springframework.boot:spring-boot:3.4.4:pom",
    );
    await userEvent.clear(input);
    await userEvent.type(input, "org.example:demo:1.0.0");
    await userEvent.click(screen.getByRole("button", { name: /^预\s*热$/ }));

    await waitFor(() => expect(onWarmed).toHaveBeenCalledTimes(1));
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe(
      "/maven/maven-central/org/example/demo/1.0.0/demo-1.0.0.jar",
    );
    expect(init?.headers).toEqual({ Authorization: "Bearer session-token" });
  });

  it("invalidates a path and clears the negative cache, reloading the list each time", async () => {
    vi.mocked(invalidateProxyCache).mockResolvedValue({
      data: { invalidated: 3 },
    } as never);
    vi.mocked(clearProxyNegativeCache).mockResolvedValue({
      data: { cleared: 2 },
    } as never);
    const onWarmed = vi.fn();
    renderCard(
      <ProxyMavenInvalidateCard repoId={repoId} onWarmed={onWarmed} />,
    );

    await userEvent.type(screen.getByRole("textbox"), "org/example/demo/");
    await userEvent.click(screen.getByRole("button", { name: /^失\s*效$/ }));
    await waitFor(() =>
      expect(invalidateProxyCache).toHaveBeenCalledWith({
        path: { repositoryId: repoId },
        body: expect.objectContaining({ path: "org/example/demo/" }),
      }),
    );

    await userEvent.click(screen.getByRole("button", { name: "清理负缓存" }));
    await waitFor(() => expect(clearProxyNegativeCache).toHaveBeenCalled());
    expect(onWarmed).toHaveBeenCalledTimes(2);
  });
});
