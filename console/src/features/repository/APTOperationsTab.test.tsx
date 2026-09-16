import { StrictMode } from "react";
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
  applyAptLifecycle,
  exportAptRepositorySnapshot,
  getAptLifecycleState,
  previewAptLifecycle,
  pruneAptSnapshots,
  restoreAptRepositorySnapshot,
} from "../../client";
import type {
  AptLifecyclePackage,
  AptLifecycleState,
  Repository,
} from "../../client";
import { PreferencesProvider } from "../../lib/preferences";
import { APTOperationsTab } from "./APTOperationsTab";

vi.mock("../../client", () => ({
  applyAptLifecycle: vi.fn(),
  exportAptRepositorySnapshot: vi.fn(),
  getAptLifecycleState: vi.fn(),
  previewAptLifecycle: vi.fn(),
  pruneAptSnapshots: vi.fn(),
  restoreAptRepositorySnapshot: vi.fn(),
}));
vi.mock("./RepositoryDistributionTab", () => ({
  RepositoryDistributionTab: () => <div>Distribution</div>,
}));
const repo: Repository = {
  id: "repo-apt",
  name: "debian-staging",
  format: "apt",
  type: "hosted",
  state: "active",
  version: "1",
  anonymousRead: false,
  mavenStrictPublication: false,
};
const pkg: AptLifecyclePackage = {
  publicationSessionId: "session-a",
  component: "main",
  poolPath: "pool/main/w/widget/widget_1.0_amd64.deb",
  revision: {
    id: "revision-a",
    repositoryId: repo.id,
    package: "widget",
    version: "1.0",
    architecture: "amd64",
    canonicalIdentity: "widget@1.0#amd64",
    digest: `sha256:${"a".repeat(64)}`,
    size: 2048,
    objectName: "native/apt/a",
    publisher: "operator",
    createdAt: "2026-01-01T00:00:00Z",
  },
};
const snapshot = {
  id: "snapshot-current",
  repositoryId: repo.id,
  suite: "stable",
  sequence: 2,
  state: "visible" as const,
  releaseDigest: "sha256:release",
  inReleaseDigest: "sha256:inrelease",
  signerIdentity: "test-signer",
  keyFingerprint: "A".repeat(40),
  signatureAlgorithm: "openpgp",
  createdAt: "2026-01-01T00:00:00Z",
  publishedAt: "2026-01-01T00:00:00Z",
};
const state: AptLifecycleState = {
  snapshots: [
    { snapshot },
    {
      snapshot: {
        ...snapshot,
        id: "snapshot-old",
        sequence: 1,
        state: "retired",
      },
      prunableAfter: "2026-01-02T00:00:00Z",
    },
  ],
  packages: [pkg],
  deletions: [
    {
      id: "deletion-a",
      package: { ...pkg, publicationSessionId: "session-old" },
      state: "recoverable",
      deletedAt: "2026-01-01T00:00:00Z",
      restoreUntil: "2099-01-01T00:00:00Z",
    },
    {
      id: "deletion-expired",
      package: pkg,
      state: "expired",
      deletedAt: "2020-01-01T00:00:00Z",
      restoreUntil: "2020-01-08T00:00:00Z",
    },
  ],
};
const plan = {
  expectedSnapshotId: snapshot.id,
  removeSessionIds: [pkg.publicationSessionId],
  restoreIds: [],
  remainingPackages: 0,
  recoveryDays: 7,
};
function mount(canAdmin = true) {
  return render(
    <PreferencesProvider>
      <APTOperationsTab repo={repo} canAdmin={canAdmin} />
    </PreferencesProvider>,
  );
}
beforeEach(() => {
  vi.mocked(getAptLifecycleState).mockResolvedValue({ data: state } as never);
  vi.mocked(previewAptLifecycle).mockResolvedValue({ data: plan } as never);
  vi.mocked(applyAptLifecycle).mockResolvedValue({
    data: { ...snapshot, sequence: 3 },
  } as never);
  vi.mocked(pruneAptSnapshots).mockResolvedValue({} as never);
});
afterEach(() => {
  cleanup();
  vi.resetAllMocks();
});

describe("APT snapshot operations", () => {
  it("does not fetch administrator-only data for readers", () => {
    mount(false);
    expect(screen.getByText("需要仓库管理权限")).toBeInTheDocument();
    expect(getAptLifecycleState).not.toHaveBeenCalled();
  });
  it("reviews exact retention candidates and reuses the key after a lost response", async () => {
    const user = userEvent.setup();
    mount();
    await screen.findByText("当前快照 #2");
    vi.mocked(applyAptLifecycle).mockRejectedValueOnce(
      new Error("connection lost"),
    );
    await user.click(screen.getByRole("button", { name: "预览保留清理" }));
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText(/移除 1 个/)).toBeInTheDocument();
    expect(applyAptLifecycle).not.toHaveBeenCalled();
    await user.click(
      within(dialog).getByRole("button", { name: "应用并签署新快照" }),
    );
    await within(dialog).findByText("connection lost");
    await user.click(
      within(dialog).getByRole("button", { name: "应用并签署新快照" }),
    );
    await screen.findByText("已发布 stable 快照 #3");
    const calls = vi.mocked(applyAptLifecycle).mock.calls;
    expect(calls).toHaveLength(2);
    expect(calls[0][0]).toEqual(calls[1][0]);
    expect(calls[0][0]?.body).toEqual({
      suite: "stable",
      action: "retention",
      expectedSnapshotId: snapshot.id,
      keepLatest: 1,
      olderThanDays: 30,
      publicationSessionIds: [pkg.publicationSessionId],
    });
    expect(calls[0][0]?.headers?.["Idempotency-Key"]).toBeTruthy();
  });
  it("previews a selected package deletion and invalidates a conflicted preview", async () => {
    const user = userEvent.setup();
    mount();
    await screen.findByText("当前快照 #2");
    await user.click(screen.getAllByRole("checkbox")[1]);
    await user.click(screen.getByRole("button", { name: "预览删除" }));
    expect(previewAptLifecycle).toHaveBeenCalledWith(
      expect.objectContaining({
        body: {
          suite: "stable",
          action: "delete",
          expectedSnapshotId: snapshot.id,
          publicationSessionIds: [pkg.publicationSessionId],
        },
      }),
    );
    vi.mocked(applyAptLifecycle).mockResolvedValueOnce({
      error: { status: 409, code: "snapshot_conflict" },
    } as never);
    await user.click(
      await screen.findByRole("button", { name: "应用并签署新快照" }),
    );
    await screen.findByText(/快照或候选集已变化/);
    await waitFor(() =>
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
    );
    expect(applyAptLifecycle).toHaveBeenCalledTimes(1);
  });
  it("previews recovery by deletion ID and disables expired records", async () => {
    const user = userEvent.setup();
    mount();
    await screen.findByText("当前快照 #2");
    await user.click(screen.getByRole("tab", { name: "恢复记录 (2)" }));
    const buttons = screen.getAllByRole("button", { name: "预览恢复" });
    expect(buttons[1]).toBeDisabled();
    vi.mocked(previewAptLifecycle).mockResolvedValueOnce({
      data: {
        ...plan,
        removeSessionIds: [],
        restoreIds: ["deletion-a"],
        remainingPackages: 2,
      },
    } as never);
    await user.click(buttons[0]);
    expect(previewAptLifecycle).toHaveBeenCalledWith(
      expect.objectContaining({
        body: {
          suite: "stable",
          expectedSnapshotId: snapshot.id,
          action: "restore",
          deletionIds: ["deletion-a"],
        },
      }),
    );
    await user.click(
      await screen.findByRole("button", { name: "应用并签署新快照" }),
    );
    await screen.findByText("已发布 stable 快照 #3");
  });
  it("disables empty retention plans and clears failed preview loading", async () => {
    const user = userEvent.setup();
    mount();
    await screen.findByText("当前快照 #2");
    vi.mocked(previewAptLifecycle).mockRejectedValueOnce(
      new Error("preview failed"),
    );
    await user.click(screen.getByRole("button", { name: "预览保留清理" }));
    await screen.findByText("preview failed");
    vi.mocked(previewAptLifecycle).mockResolvedValueOnce({
      data: { ...plan, removeSessionIds: [] },
    } as never);
    await user.click(screen.getByRole("button", { name: "预览保留清理" }));
    expect(
      await screen.findByRole("button", { name: "应用并签署新快照" }),
    ).toBeDisabled();
    await user.click(
      within(screen.getByRole("dialog")).getByRole("button", {
        name: /取\s*消/,
      }),
    );
  });
  it("keeps stale snapshot evidence but disables actions after a refresh failure", async () => {
    const user = userEvent.setup();
    mount();
    await screen.findByText("当前快照 #2");
    vi.mocked(getAptLifecycleState).mockRejectedValueOnce(
      new Error("refresh failed"),
    );
    await user.click(screen.getByRole("button", { name: "刷新快照" }));
    await screen.findByText("refresh failed");
    expect(screen.getByText("当前快照 #2")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "预览保留清理" })).toBeDisabled();
    await user.click(screen.getByRole("button", { name: /重\s*试/ }));
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "预览保留清理" }),
      ).toBeEnabled(),
    );
  });
  it("ignores a late response after switching suites", async () => {
    let resolve!: (result: never) => void;
    vi.mocked(getAptLifecycleState).mockImplementationOnce(
      () =>
        new Promise<never>((r) => {
          resolve = r;
        }),
    );
    const user = userEvent.setup();
    mount();
    const input = screen.getByRole("searchbox", { name: "发行套件" });
    await user.clear(input);
    await user.type(input, "bookworm");
    await user.click(screen.getByRole("button", { name: /查\s*看/ }));
    await screen.findByText("当前快照 #2");
    await act(async () =>
      resolve({
        data: {
          ...state,
          snapshots: [{ snapshot: { ...snapshot, sequence: 99 } }],
        },
      } as never),
    );
    expect(screen.queryByText("当前快照 #99")).not.toBeInTheDocument();
  });
  it("requires confirmation to prune retired references and protects the visible snapshot", async () => {
    const user = userEvent.setup();
    mount();
    await screen.findByText("当前快照 #2");
    await user.click(screen.getByRole("tab", { name: "快照历史 (2)" }));
    const buttons = screen.getAllByRole("button", { name: "清理引用" });
    expect(buttons[0]).toBeDisabled();
    await user.click(buttons[1]);
    expect(pruneAptSnapshots).not.toHaveBeenCalled();
    await user.click(
      screen.getAllByRole("button", { name: "清理引用" }).at(-1)!,
    );
    await screen.findByText("快照引用已清理；无引用对象由后台异步回收。");
    expect(pruneAptSnapshots).toHaveBeenCalledWith({
      path: { repositoryId: repo.id },
      body: { snapshotIds: ["snapshot-old"] },
    });
  });
});

it("does not accept a retired StrictMode mount request after a later refresh", async () => {
  let resolve!: (result: never) => void;
  vi.mocked(getAptLifecycleState).mockImplementationOnce(
    () =>
      new Promise<never>((r) => {
        resolve = r;
      }),
  );
  const user = userEvent.setup();
  render(
    <StrictMode>
      <PreferencesProvider>
        <APTOperationsTab repo={repo} canAdmin />
      </PreferencesProvider>
    </StrictMode>,
  );
  await screen.findByText("当前快照 #2");
  await user.click(screen.getByRole("button", { name: "刷新快照" }));
  await waitFor(() => expect(getAptLifecycleState).toHaveBeenCalledTimes(3));
  await act(async () =>
    resolve({
      data: {
        ...state,
        snapshots: [{ snapshot: { ...snapshot, sequence: 99 } }],
      },
    } as never),
  );
  expect(screen.queryByText("当前快照 #99")).not.toBeInTheDocument();
});

// The Gateway exports a visible or retired snapshot and restores it only
// against the exact receipt the operator saved at backup time. These tests pin
// the Console wiring for both halves of that recovery workflow.
describe("APT disaster-recovery archive", () => {
  const createObjectURL = vi.fn();
  const revokeObjectURL = vi.fn();
  const validReceipt = `sha256:${"b".repeat(64)}`;
  const archiveBlob = () =>
    new Blob(["apt-archive"], {
      type: "application/vnd.artifact-gateway.apt-snapshot.v1+tar",
    });

  beforeEach(() => {
    createObjectURL.mockReturnValue("blob:apt-archive");
    Object.defineProperty(URL, "createObjectURL", {
      configurable: true,
      value: createObjectURL,
    });
    Object.defineProperty(URL, "revokeObjectURL", {
      configurable: true,
      value: revokeObjectURL,
    });
    // jsdom cannot navigate, so the synthetic download anchor click would log
    // "Not implemented: navigation" without changing the assertions.
    vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(
      () => undefined,
    );
    vi.mocked(exportAptRepositorySnapshot).mockResolvedValue({
      data: archiveBlob(),
    } as never);
    vi.mocked(restoreAptRepositorySnapshot).mockResolvedValue({
      data: { ...snapshot, sequence: 4 },
    } as never);
  });

  it("exports every visible and retired snapshot but never a pruned one", async () => {
    vi.mocked(getAptLifecycleState).mockResolvedValueOnce({
      data: {
        ...state,
        snapshots: [
          ...state.snapshots,
          {
            snapshot: {
              ...snapshot,
              id: "snapshot-pruned",
              sequence: 0,
              state: "pruned",
            },
          },
        ],
      },
    } as never);
    const user = userEvent.setup();
    mount();
    await screen.findByText("当前快照 #2");
    await user.click(screen.getByRole("tab", { name: "灾备归档" }));
    const exportButtons = await screen.findAllByRole("button", {
      name: "导出归档",
    });
    expect(exportButtons).toHaveLength(2);
    await user.click(exportButtons[0]);
    expect(exportAptRepositorySnapshot).toHaveBeenCalledWith({
      path: { repositoryId: repo.id, snapshotId: "snapshot-current" },
    });
    await screen.findByText(/apt-stable-2\.tar/);
    expect(createObjectURL).toHaveBeenCalledTimes(1);
    expect(revokeObjectURL).toHaveBeenCalledTimes(1);
  });

  it("restores only with an archive file and a well-formed independent receipt", async () => {
    const user = userEvent.setup();
    const { container } = mount();
    await screen.findByText("当前快照 #2");
    await user.click(screen.getByRole("tab", { name: "灾备归档" }));
    const receiptField = screen.getByRole("textbox", {
      name: "备份回执 (sha256)",
    });
    expect(screen.getByRole("button", { name: "恢复归档" })).toBeDisabled();

    await user.type(receiptField, "sha256:nothex");
    expect(screen.getByRole("button", { name: "恢复归档" })).toBeDisabled();

    await user.clear(receiptField);
    await user.type(receiptField, validReceipt);
    const input = container.querySelector('input[type="file"]');
    expect(input).not.toBeNull();
    await user.upload(
      input as HTMLInputElement,
      new File(["apt-archive"], "apt-stable-2.tar", {
        type: "application/vnd.artifact-gateway.apt-snapshot.v1+tar",
      }),
    );
    await waitFor(() =>
      expect(screen.getByRole("button", { name: "恢复归档" })).toBeEnabled(),
    );

    await user.click(screen.getByRole("button", { name: "恢复归档" }));
    await user.click(
      screen.getAllByRole("button", { name: "恢复归档" }).at(-1)!,
    );
    await screen.findByText("已从归档恢复 stable 快照 #4");
    const call = vi.mocked(restoreAptRepositorySnapshot).mock.calls[0]?.[0];
    expect(call?.path).toEqual({ repositoryId: repo.id });
    expect(call?.headers).toEqual({
      "X-Artifact-Archive-Digest": validReceipt,
    });
    expect((call?.body as File).name).toBe("apt-stable-2.tar");
    expect(exportAptRepositorySnapshot).not.toHaveBeenCalled();
  });
});
