import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AuthProvider } from "../../lib/auth";
import { PreferencesProvider } from "../../lib/preferences";
import { NpmVersionGovernance } from "./NpmVersionGovernance";
import type { NpmPackument } from "./protocolDetailQueries";

const packageDocument = (): NpmPackument => ({
  _id: "@scope/widget",
  _rev: "7",
  name: "@scope/widget",
  "dist-tags": { latest: "1.0.0", next: "1.1.0" },
  versions: {
    "1.0.0": {
      name: "@scope/widget",
      version: "1.0.0",
      deprecated: "Use 1.1.0 <script>literal</script>",
      dist: { integrity: "sha512-first" },
    },
    "1.1.0": {
      name: "@scope/widget",
      version: "1.1.0",
      dist: { integrity: "sha512-second" },
    },
  },
});

function renderGovernance(
  options: {
    canWrite?: boolean;
    canUnpublish?: boolean;
    packument?: NpmPackument;
  } = {},
) {
  const onChanged = vi.fn().mockResolvedValue(undefined);
  const onUnpublished = vi.fn();
  const viewFor = (packument: NpmPackument) => (
    <AuthProvider>
      <PreferencesProvider>
        <NpmVersionGovernance
          repoName="npm-hosted"
          packument={packument}
          version="1.0.0"
          canWrite={options.canWrite ?? true}
          canUnpublish={options.canUnpublish ?? true}
          onChanged={onChanged}
          onUnpublished={onUnpublished}
        />
      </PreferencesProvider>
    </AuthProvider>
  );
  const view = render(viewFor(options.packument ?? packageDocument()));
  return {
    onChanged,
    onUnpublished,
    rerender: (packument: NpmPackument) => view.rerender(viewFor(packument)),
  };
}

function mockMutation(response: () => Promise<Response>) {
  return vi.spyOn(globalThis, "fetch").mockImplementation(async (input) => {
    if (input === "/auth/session") return new Response(null, { status: 401 });
    return response();
  });
}

afterEach(() => {
  cleanup();
  localStorage.clear();
  vi.restoreAllMocks();
});

describe("npm version governance", () => {
  it("shows untrusted warning as text and respects read-only and Proxy views", () => {
    mockMutation(async () => new Response(null, { status: 500 }));
    const packument = packageDocument();
    delete packument._rev;
    renderGovernance({ canWrite: true, canUnpublish: true, packument });
    expect(
      screen.getByText("Use 1.1.0 <script>literal</script>"),
    ).toBeVisible();
    expect(document.querySelector("script")).toBeNull();
    expect(
      screen.queryByRole("button", { name: "编辑弃用提示" }),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: /^撤\s*包$/ }),
    ).not.toBeInTheDocument();
  });

  it("updates one version while preserving the complete immutable packument", async () => {
    const fetchMock = mockMutation(async () =>
      Response.json({ ok: true, rev: "8" }),
    );
    const { onChanged } = renderGovernance({ canUnpublish: false });
    const user = userEvent.setup();
    expect(
      screen.queryByRole("button", { name: /^撤\s*包$/ }),
    ).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "编辑弃用提示" }));
    await user.clear(screen.getByRole("textbox"));
    await user.type(screen.getByRole("textbox"), "new warning");
    await user.click(screen.getByRole("button", { name: "保存提示" }));
    await waitFor(() => expect(onChanged).toHaveBeenCalledOnce());
    const call = fetchMock.mock.calls.find(([url]) => url !== "/auth/session");
    expect(call?.[0]).toBe("/npm/npm-hosted/%40scope%2Fwidget");
    const document = JSON.parse(String(call?.[1]?.body)) as NpmPackument;
    expect(document._rev).toBe("7");
    expect(document.versions["1.0.0"].deprecated).toBe("new warning");
    expect(document.versions["1.0.0"].dist).toEqual(
      packageDocument().versions["1.0.0"].dist,
    );
    expect(document.versions["1.1.0"]).toEqual(
      packageDocument().versions["1.1.0"],
    );
    expect(document["dist-tags"]).toEqual(packageDocument()["dist-tags"]);
  });

  it.each([409, 403, 503])(
    "keeps the draft and detail on HTTP %s without replaying the mutation",
    async (status) => {
      const fetchMock = mockMutation(
        async () => new Response(null, { status }),
      );
      const { onChanged } = renderGovernance();
      const user = userEvent.setup();
      await user.click(screen.getByRole("button", { name: "编辑弃用提示" }));
      await user.clear(screen.getByRole("textbox"));
      await user.type(screen.getByRole("textbox"), "retained draft");
      await user.click(screen.getByRole("button", { name: "保存提示" }));
      expect(
        await screen.findByText(new RegExp(`HTTP ${status}`)),
      ).toBeVisible();
      expect(screen.getByRole("textbox")).toHaveValue("retained draft");
      expect(
        screen.getByText("Use 1.1.0 <script>literal</script>"),
      ).toBeVisible();
      await user.click(screen.getByRole("button", { name: /重试/ }));
      await waitFor(() => expect(onChanged).toHaveBeenCalledOnce());
      expect(
        fetchMock.mock.calls.filter(([url]) => url !== "/auth/session"),
      ).toHaveLength(1);
      expect(
        screen.getByRole("button", { name: "保存提示" }),
      ).not.toBeDisabled();
    },
  );

  it("keeps the opened revision when a refetch updates props during editing", async () => {
    const fetchMock = mockMutation(
      async () => new Response(null, { status: 409 }),
    );
    const { rerender, onChanged } = renderGovernance();
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "编辑弃用提示" }));
    await user.clear(screen.getByRole("textbox"));
    await user.type(screen.getByRole("textbox"), "my unsaved warning");
    const refreshed = packageDocument();
    refreshed._rev = "8";
    refreshed.versions["1.0.0"].deprecated = "another writer's update";
    refreshed["dist-tags"].next = "1.0.0";
    rerender(refreshed);
    expect(screen.getByText("another writer's update")).toBeVisible();
    await user.click(screen.getByRole("button", { name: "保存提示" }));
    expect(await screen.findByText(/HTTP 409/)).toBeVisible();
    const calls = fetchMock.mock.calls.filter(
      ([url]) => url !== "/auth/session",
    );
    expect(calls).toHaveLength(1);
    const sent = JSON.parse(String(calls[0][1]?.body)) as NpmPackument;
    expect(sent._rev).toBe("7");
    expect(sent["dist-tags"]).toEqual(packageDocument()["dist-tags"]);
    expect(sent.versions["1.0.0"].deprecated).toBe("my unsaved warning");
    expect(screen.getByRole("textbox")).toHaveValue("my unsaved warning");
    expect(onChanged).not.toHaveBeenCalled();
  });

  it("treats an incomplete successful response as unknown and keeps the draft", async () => {
    mockMutation(async () => Response.json({ ok: true }));
    const { onChanged } = renderGovernance();
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "编辑弃用提示" }));
    await user.click(screen.getByRole("button", { name: "保存提示" }));
    expect(await screen.findByText(/无法确认更新结果/)).toBeVisible();
    expect(screen.getByRole("textbox")).toBeVisible();
    expect(onChanged).not.toHaveBeenCalled();
  });

  it("unpublishes through the native revision, removing only tags naming the selected version", async () => {
    const fetchMock = mockMutation(async () =>
      Response.json({ ok: true, rev: "8" }),
    );
    const { onChanged, onUnpublished } = renderGovernance();
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: /^撤\s*包$/ }));
    await user.click(
      screen.getAllByRole("button", { name: /^撤\s*包$/ }).at(-1)!,
    );
    await waitFor(() => expect(onChanged).toHaveBeenCalledOnce());
    expect(onUnpublished).toHaveBeenCalledOnce();
    const call = fetchMock.mock.calls.find(([url]) => url !== "/auth/session");
    expect(call?.[0]).toBe("/npm/npm-hosted/%40scope%2Fwidget/-rev/7");
    const document = JSON.parse(String(call?.[1]?.body)) as NpmPackument;
    expect(document.versions["1.0.0"]).toBeUndefined();
    expect(document.versions["1.1.0"]).toEqual(
      packageDocument().versions["1.1.0"],
    );
    expect(document["dist-tags"]).toEqual({ next: "1.1.0" });
  });
});
