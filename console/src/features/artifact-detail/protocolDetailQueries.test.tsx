import { QueryClientProvider } from "@tanstack/react-query";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AuthProvider } from "../../lib/auth";
import { PreferencesProvider } from "../../lib/preferences";
import { createQueryClient } from "../../lib/query/client";
import { CargoCrateDetail } from "./CargoCrateDetail";
import { GoModuleDetail } from "./GoModuleDetail";
import { NpmPackageDetail } from "./NpmPackageDetail";

afterEach(() => {
  cleanup();
  localStorage.clear();
  vi.restoreAllMocks();
});

/** The production query client, with the session hook replaced by a spy. */
function renderWithClient(ui: ReactNode) {
  const onAuthFailure = vi.fn();
  const client = createQueryClient(onAuthFailure);
  client.setDefaultOptions({
    queries: { ...client.getDefaultOptions().queries, retryDelay: 0 },
  });
  const view = render(
    <AuthProvider>
      <QueryClientProvider client={client}>
        <PreferencesProvider>{ui}</PreferencesProvider>
      </QueryClientProvider>
    </AuthProvider>,
  );
  return { ...view, onAuthFailure, client };
}

function packument(name: string) {
  return {
    name,
    "dist-tags": { latest: "1.0.0" },
    versions: { "1.0.0": { name, version: "1.0.0" } },
  };
}

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

describe("protocol detail reads", () => {
  it("never lets a slower answer for the previous package replace the current one", async () => {
    let finishFirst: (response: Response) => void = () => {};
    vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      if (input === "/auth/session") return new Response(null, { status: 401 });
      if (String(input).endsWith("/first"))
        return new Promise<Response>((resolve) => {
          finishFirst = resolve;
        });
      return json(packument("second"));
    });

    const { rerender, client } = renderWithClient(
      <NpmPackageDetail repoName="npm" packageName="first" />,
    );
    await waitFor(() => expect(finishFirst).not.toBe(undefined));
    // Same cache, next package: the first read is superseded, not awaited.
    rerender(
      <AuthProvider>
        <QueryClientProvider client={client}>
          <PreferencesProvider>
            <NpmPackageDetail repoName="npm" packageName="second" />
          </PreferencesProvider>
        </QueryClientProvider>
      </AuthProvider>,
    );
    expect(await screen.findByText("second@1.0.0")).toBeInTheDocument();
    finishFirst(json(packument("first")));
    await new Promise((resolve) => setTimeout(resolve, 20));
    expect(screen.queryByText("first@1.0.0")).not.toBeInTheDocument();
  });

  it("shows a refusal with its status, keeps the session, and retries on request", async () => {
    localStorage.setItem("ag.console.token", "operator-token");
    let refuse = true;
    const fetchMock = vi
      .spyOn(globalThis, "fetch")
      .mockImplementation(async (input) => {
        if (input === "/auth/session")
          return new Response(null, { status: 401 });
        return refuse
          ? new Response("forbidden", { status: 403 })
          : json(packument("private-widget"));
      });

    const { onAuthFailure } = renderWithClient(
      <NpmPackageDetail repoName="npm" packageName="private-widget" />,
    );
    expect(await screen.findByText("读取 npm 包失败 (403)")).toBeVisible();
    // A refusal is final: no second request, and the session stays.
    expect(
      fetchMock.mock.calls.filter(([input]) => input !== "/auth/session"),
    ).toHaveLength(1);
    expect(onAuthFailure).not.toHaveBeenCalled();

    refuse = false;
    await userEvent.click(screen.getByRole("button", { name: /重\s*试/ }));
    expect(await screen.findByText("private-widget@1.0.0")).toBeVisible();
  });

  it("retries a gateway failure once before reporting it", async () => {
    const fetchMock = vi
      .spyOn(globalThis, "fetch")
      .mockImplementation(async (input) => {
        if (input === "/auth/session")
          return new Response(null, { status: 401 });
        return new Response("unavailable", { status: 503 });
      });

    renderWithClient(<NpmPackageDetail repoName="npm" packageName="widget" />);
    expect(await screen.findByText("读取 npm 包失败 (503)")).toBeVisible();
    expect(
      fetchMock.mock.calls.filter(([input]) => input !== "/auth/session"),
    ).toHaveLength(2);
  });

  it("keeps Go versions on screen when one version's info fails", async () => {
    vi.spyOn(globalThis, "fetch").mockImplementation(async (input, init) => {
      const url = String(input);
      if (url === "/auth/session") return new Response(null, { status: 401 });
      if (url.endsWith("/@v/list")) return new Response("v1.0.0\nv1.1.0\n");
      if (init?.method === "HEAD") throw new TypeError("network down");
      return new Response("missing", { status: 404 });
    });

    renderWithClient(
      <GoModuleDetail repoName="go" modulePath="example.com/widget" />,
    );
    expect(await screen.findByText("读取版本信息失败 (404)")).toBeVisible();
    // The digest is optional evidence: its failure adds no error of its own.
    expect(screen.getAllByText(/v1\.1\.0/).length).toBeGreaterThan(0);
  });

  it("reports a failed crate download without losing the index", async () => {
    const user = userEvent.setup();
    vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
      const url = String(input);
      if (url === "/auth/session") return new Response(null, { status: 401 });
      if (url.endsWith("/download"))
        return new Response("gone", { status: 410 });
      return new Response(
        `${JSON.stringify({ name: "demo", vers: "1.0.0", cksum: "a".repeat(64), yanked: false })}\n`,
      );
    });

    renderWithClient(<CargoCrateDetail repoName="cargo" crateName="demo" />);
    await user.click(
      await screen.findByRole("button", { name: /下载 \.crate/ }),
    );
    expect(await screen.findByText("HTTP 410")).toBeVisible();
    expect(screen.getAllByText(/1\.0\.0/).length).toBeGreaterThan(0);
  });
});
