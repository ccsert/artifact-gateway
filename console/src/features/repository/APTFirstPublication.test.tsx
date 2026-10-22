import { webcrypto } from "node:crypto";
import { StrictMode } from "react";
import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import {
  createAptPublicationSession,
  getAptLifecycleState,
  publishAptRepositorySnapshot,
  uploadAptPublicationPackage,
} from "../../client";
import type {
  AptPackageRevision,
  AptPublicationSession,
  AptRepositorySnapshot,
  Problem,
  Repository,
} from "../../client";
import { PreferencesProvider } from "../../lib/preferences";
import { TestQueryProvider } from "../../test/queryClient";
import { APTOperationsTab } from "./APTOperationsTab";
import { APTFirstPublication } from "./APTFirstPublication";

vi.mock("../../client", () => ({
  applyAptLifecycle: vi.fn(),
  createAptPublicationSession: vi.fn(),
  exportAptRepositorySnapshot: vi.fn(),
  getAptLifecycleState: vi.fn(),
  previewAptLifecycle: vi.fn(),
  pruneAptSnapshots: vi.fn(),
  publishAptRepositorySnapshot: vi.fn(),
  restoreAptRepositorySnapshot: vi.fn(),
  uploadAptPublicationPackage: vi.fn(),
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

function success<T>(data: T, status = 200) {
  return {
    data,
    error: undefined,
    request: new Request("http://gateway.test/api/v2"),
    response: new Response(null, { status }),
  };
}

function failure(status: number, code: Problem["code"], message: string) {
  return {
    data: undefined,
    error: {
      type: "about:blank",
      title: message,
      message,
      code,
      status,
      requestId: "apt-first-fixture",
    },
    request: new Request("http://gateway.test/api/v2"),
    response: new Response(null, { status }),
  };
}

const digest =
  "sha256:3adc870b6595ccbffaa9ccaa6fa5653652136fbeea99ffd754c52960bd0b9ea9";
const session: AptPublicationSession = {
  id: "session-first",
  repositoryId: repo.id,
  suite: "stable",
  component: "main",
  publisher: "operator",
  objectName: "widget_1.2.3_amd64.deb",
  declaredDigest: digest,
  declaredSize: 9,
  state: "open",
  expiresAt: "2099-01-01T00:00:00Z",
  createdAt: "2026-10-22T00:00:00Z",
};
const revision: AptPackageRevision = {
  id: "revision-first",
  repositoryId: repo.id,
  package: "widget",
  version: "1.2.3",
  architecture: "amd64",
  canonicalIdentity: "widget@1.2.3#amd64",
  digest,
  size: 9,
  objectName: "native/apt/widget",
  publisher: "operator",
  createdAt: session.createdAt,
};
const snapshot: AptRepositorySnapshot = {
  id: "snapshot-first",
  repositoryId: repo.id,
  suite: "stable",
  sequence: 1,
  state: "visible",
  releaseDigest: `sha256:${"a".repeat(64)}`,
  inReleaseDigest: `sha256:${"b".repeat(64)}`,
  signerIdentity: "fixture-signer",
  keyFingerprint: "F".repeat(40),
  signatureAlgorithm: "openpgp",
  createdAt: session.createdAt,
  publishedAt: session.createdAt,
};

// jsdom lacks Blob.arrayBuffer. These exact nine input bytes exercise the real
// WebCrypto digest; package parsing is the mocked server's responsibility here.
function packageFile(name = session.objectName, body = "deb-bytes") {
  const bytes = new TextEncoder().encode(body);
  const file = new File([bytes], name);
  Object.defineProperty(file, "arrayBuffer", {
    value: async () => bytes.buffer,
  });
  return file;
}

function deferred<T>() {
  let resolve: (value: T) => void = () => {
    throw new Error("deferred promise is not initialized");
  };
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve: (value: T) => resolve(value) };
}

function workflow(
  onPublished = vi.fn(async () => {}),
  target = repo,
  suite = "stable",
) {
  return (
    <StrictMode>
      <TestQueryProvider>
        <PreferencesProvider>
          <APTFirstPublication
            repo={target}
            suite={suite}
            disabled={false}
            onPublished={onPublished}
          />
        </PreferencesProvider>
      </TestQueryProvider>
    </StrictMode>
  );
}

async function choose(
  container: HTMLElement,
  user: ReturnType<typeof userEvent.setup>,
  file = packageFile(),
) {
  const input = container.querySelector('input[type="file"]');
  if (!(input instanceof HTMLInputElement))
    throw new Error("file picker missing");
  await user.upload(input, file);
  await screen.findByText(digest);
  return file;
}

async function stage(
  container: HTMLElement,
  user: ReturnType<typeof userEvent.setup>,
) {
  const file = await choose(container, user);
  await user.click(screen.getByRole("button", { name: "暂存并核对软件包" }));
  await screen.findByText("已暂存：核对服务端识别的身份");
  return file;
}

beforeEach(() => {
  vi.stubGlobal("crypto", webcrypto);
  vi.mocked(getAptLifecycleState).mockResolvedValue(
    success({ snapshots: [], packages: [], deletions: [] }),
  );
  vi.mocked(createAptPublicationSession).mockResolvedValue(
    success(session, 201),
  );
  vi.mocked(uploadAptPublicationPackage).mockResolvedValue(success(revision));
  vi.mocked(publishAptRepositorySnapshot).mockResolvedValue(
    success(snapshot, 201),
  );
});
afterEach(() => {
  cleanup();
  vi.resetAllMocks();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

it.each(["empty.deb", "wrong.zip"])(
  "does not create a session for an invalid selection %s",
  async (name) => {
    const user = userEvent.setup({ applyAccept: false });
    const { container } = render(workflow());
    const input = container.querySelector('input[type="file"]');
    if (!(input instanceof HTMLInputElement))
      throw new Error("file picker missing");
    await user.upload(
      input,
      packageFile(name, name === "empty.deb" ? "" : "deb-bytes"),
    );
    await screen.findByText(
      "请选择一个非空 .deb 文件，文件名不含路径且不超过 255 个字符，大小不超过 1 GiB。",
    );
    expect(
      screen.getByRole("button", { name: "暂存并核对软件包" }),
    ).toBeDisabled();
    expect(createAptPublicationSession).not.toHaveBeenCalled();
  },
);

it("requires a valid component and a successful browser digest", async () => {
  const user = userEvent.setup();
  const view = render(workflow());
  await choose(view.container, user);
  const component = screen.getByRole("textbox", { name: "组件" });
  await user.clear(component);
  await user.type(component, "Bad/Component");
  expect(
    screen.getByRole("button", { name: "暂存并核对软件包" }),
  ).toBeDisabled();
  expect(createAptPublicationSession).not.toHaveBeenCalled();
  view.unmount();
  vi.stubGlobal("crypto", { randomUUID: () => "unused" });
  const next = render(workflow());
  const input = next.container.querySelector('input[type="file"]');
  if (!(input instanceof HTMLInputElement))
    throw new Error("file picker missing");
  await user.upload(input, packageFile());
  await screen.findByText(
    "无法计算文件的 SHA-256。请在 HTTPS 或 localhost 页面重新选择文件。",
  );
  expect(
    screen.getByRole("button", { name: "暂存并核对软件包" }),
  ).toBeDisabled();
  expect(createAptPublicationSession).not.toHaveBeenCalled();
});

it("does not offer first publication when the initial administrator state cannot be read", async () => {
  vi.mocked(getAptLifecycleState).mockRejectedValueOnce(
    new Error("state unavailable"),
  );
  render(
    <TestQueryProvider>
      <PreferencesProvider>
        <APTOperationsTab repo={repo} canAdmin />
      </PreferencesProvider>
    </TestQueryProvider>,
  );
  await screen.findByText("state unavailable");
  expect(
    screen.queryByRole("button", { name: "暂存并核对软件包" }),
  ).not.toBeInTheDocument();
  expect(screen.queryByText("加载中…")).not.toBeInTheDocument();
});

it("replays the same File against an existing session after a lost upload response", async () => {
  vi.mocked(uploadAptPublicationPackage).mockRejectedValueOnce(
    new Error("upload response lost"),
  );
  const user = userEvent.setup();
  const { container } = render(workflow());
  const file = await choose(container, user);
  await user.click(screen.getByRole("button", { name: "暂存并核对软件包" }));
  await screen.findByText("upload response lost");
  await user.click(screen.getByRole("button", { name: "重试暂存并核对" }));
  await screen.findByText("widget@1.2.3#amd64");
  expect(createAptPublicationSession).toHaveBeenCalledTimes(1);
  expect(uploadAptPublicationPackage).toHaveBeenCalledTimes(2);
  for (const [request] of vi.mocked(uploadAptPublicationPackage).mock.calls)
    expect(request.body).toBe(file);
});

it("does not treat a failed state refresh as an unconfirmed publication", async () => {
  const user = userEvent.setup();
  const onPublished = vi.fn(async () => {
    throw new Error("refresh disconnected");
  });
  const { container } = render(workflow(onPublished));
  await stage(container, user);
  await user.click(
    screen.getByRole("checkbox", { name: "已核对包名、版本和架构" }),
  );
  await user.click(screen.getByRole("button", { name: "确认并签署首次快照" }));
  await screen.findByText("refresh disconnected");
  expect(screen.getByText("已发布 stable 快照 #1")).toBeInTheDocument();
  expect(
    screen.queryByRole("button", { name: "重试首次发布" }),
  ).not.toBeInTheDocument();
  expect(
    screen.queryByRole("button", { name: "清除本次选择" }),
  ).not.toBeInTheDocument();
  expect(publishAptRepositorySnapshot).toHaveBeenCalledTimes(1);
});

it.each(["upload", "publish"])(
  "ignores a late %s result after a target change",
  async (phase) => {
    const user = userEvent.setup();
    const onPublished = vi.fn(async () => {});
    const view = render(workflow(onPublished));
    const upload = deferred<ReturnType<typeof success<AptPackageRevision>>>();
    const publish =
      deferred<ReturnType<typeof success<AptRepositorySnapshot>>>();
    if (phase === "upload") {
      vi.mocked(uploadAptPublicationPackage).mockReturnValueOnce(
        upload.promise,
      );
      await choose(view.container, user);
      await user.click(
        screen.getByRole("button", { name: "暂存并核对软件包" }),
      );
      await waitFor(() =>
        expect(uploadAptPublicationPackage).toHaveBeenCalledTimes(1),
      );
    } else {
      await stage(view.container, user);
      vi.mocked(publishAptRepositorySnapshot).mockReturnValueOnce(
        publish.promise,
      );
      await user.click(
        screen.getByRole("checkbox", { name: "已核对包名、版本和架构" }),
      );
      await user.click(
        screen.getByRole("button", { name: "确认并签署首次快照" }),
      );
    }
    view.rerender(
      workflow(onPublished, { ...repo, id: "next-repository" }, "bookworm"),
    );
    const nextComponent = screen.getByRole("textbox", { name: "组件" });
    await user.click(nextComponent);
    await act(async () => {
      upload.resolve(success(revision));
      publish.resolve(success(snapshot, 201));
    });
    expect(screen.getByText("尚未选择文件")).toBeInTheDocument();
    expect(screen.queryByText("widget@1.2.3#amd64")).not.toBeInTheDocument();
    expect(nextComponent).toHaveFocus();
    expect(onPublished).not.toHaveBeenCalled();
    const request =
      phase === "upload"
        ? vi.mocked(uploadAptPublicationPackage).mock.calls[0][0]
        : vi.mocked(publishAptRepositorySnapshot).mock.calls[0][0];
    expect(request.signal?.aborted).toBe(true);
  },
);

it("never fetches administrator state for a member or a non-Hosted repository", () => {
  const view = render(
    <TestQueryProvider>
      <PreferencesProvider>
        <APTOperationsTab repo={repo} canAdmin={false} />
      </PreferencesProvider>
    </TestQueryProvider>,
  );
  expect(screen.getByText("需要仓库管理权限")).toBeInTheDocument();
  view.rerender(
    <TestQueryProvider>
      <PreferencesProvider>
        <APTOperationsTab repo={{ ...repo, type: "proxy" }} canAdmin />
      </PreferencesProvider>
    </TestQueryProvider>,
  );
  expect(
    screen.getByText("首次发布仅适用于 APT Hosted 仓库"),
  ).toBeInTheDocument();
  expect(getAptLifecycleState).not.toHaveBeenCalled();
  expect(createAptPublicationSession).not.toHaveBeenCalled();
});

it("hashes without uploading, stages the original File, and requires identity confirmation", async () => {
  const user = userEvent.setup();
  const onPublished = vi.fn(async () => {});
  const { container } = render(workflow(onPublished));
  const file = await choose(container, user);
  expect(screen.getByText("9 字节")).toBeInTheDocument();
  expect(createAptPublicationSession).not.toHaveBeenCalled();
  expect(uploadAptPublicationPackage).not.toHaveBeenCalled();
  await user.click(screen.getByRole("button", { name: "暂存并核对软件包" }));
  await screen.findByText("widget@1.2.3#amd64");
  expect(createAptPublicationSession).toHaveBeenCalledWith(
    expect.objectContaining({
      path: { repositoryId: repo.id },
      body: {
        suite: "stable",
        component: "main",
        objectName: file.name,
        declaredDigest: digest,
        declaredSize: 9,
      },
      headers: { "Idempotency-Key": expect.any(String) },
    }),
  );
  expect(vi.mocked(uploadAptPublicationPackage).mock.calls[0][0].body).toBe(
    file,
  );
  expect(publishAptRepositorySnapshot).not.toHaveBeenCalled();
  const publishButton = screen.getByRole("button", {
    name: "确认并签署首次快照",
  });
  expect(publishButton).toBeDisabled();
  await user.click(
    screen.getByRole("checkbox", { name: "已核对包名、版本和架构" }),
  );
  await user.click(publishButton);
  await waitFor(() => expect(onPublished).toHaveBeenCalledWith(snapshot));
  expect(publishAptRepositorySnapshot).toHaveBeenCalledWith(
    expect.objectContaining({
      path: { repositoryId: repo.id },
      body: {
        suite: "stable",
        sequence: 1,
        publicationSessionIds: [session.id],
      },
      headers: { "Idempotency-Key": expect.any(String) },
    }),
  );
});

it("focuses the staged identity heading and lets keyboard review continue without refocusing", async () => {
  const user = userEvent.setup();
  const view = render(workflow());
  await stage(view.container, user);
  const heading = screen.getByRole("heading", {
    name: "已暂存：核对服务端识别的身份",
  });
  expect(heading).toHaveFocus();
  await user.tab();
  const review = screen.getByRole("checkbox", {
    name: "已核对包名、版本和架构",
  });
  expect(review).toHaveFocus();
  await user.keyboard(" ");
  expect(review).toBeChecked();
  expect(review).toHaveFocus();
  view.rerender(workflow());
  expect(review).toHaveFocus();
});

it("keeps the create request and idempotency key after a lost create response", async () => {
  vi.mocked(createAptPublicationSession).mockRejectedValueOnce(
    new Error("create response lost"),
  );
  const user = userEvent.setup();
  const { container } = render(workflow());
  await choose(container, user);
  await user.click(screen.getByRole("button", { name: "暂存并核对软件包" }));
  await screen.findByText("create response lost");
  expect(screen.getByRole("textbox", { name: "组件" })).toBeDisabled();
  await user.click(screen.getByRole("button", { name: "重试暂存并核对" }));
  await screen.findByText("widget@1.2.3#amd64");
  const [first, second] = vi.mocked(createAptPublicationSession).mock.calls;
  expect(second[0].headers).toEqual(first[0].headers);
  expect(second[0].body).toEqual(first[0].body);
  expect(uploadAptPublicationPackage).toHaveBeenCalledTimes(1);
});

it.each([
  [422, "digest_mismatch", "invalid deb"],
  [507, "quota_exceeded", "quota exhausted"],
] as const)(
  "retains the same session and bytes after upload %s",
  async (status, code, message) => {
    vi.mocked(uploadAptPublicationPackage).mockResolvedValueOnce(
      failure(status, code, message),
    );
    const user = userEvent.setup();
    const { container } = render(workflow());
    const file = await choose(container, user);
    await user.click(screen.getByRole("button", { name: "暂存并核对软件包" }));
    await screen.findByText(message);
    expect(screen.getByText(session.id)).toBeInTheDocument();
    expect(publishAptRepositorySnapshot).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "重试暂存并核对" }));
    await screen.findByText("widget@1.2.3#amd64");
    expect(createAptPublicationSession).toHaveBeenCalledTimes(1);
    for (const [request] of vi.mocked(uploadAptPublicationPackage).mock.calls) {
      expect(request.body).toBe(file);
      expect(request.path).toEqual({
        repositoryId: repo.id,
        sessionId: session.id,
      });
    }
  },
);

it.each(["signer 503", "lost publish response", "concurrent snapshot"])(
  "keeps staged identity and an exact sequence-one retry after %s",
  async (kind) => {
    if (kind === "lost publish response")
      vi.mocked(publishAptRepositorySnapshot).mockRejectedValueOnce(
        new Error(kind),
      );
    else
      vi.mocked(publishAptRepositorySnapshot).mockResolvedValueOnce(
        failure(
          kind === "signer 503" ? 503 : 409,
          kind === "signer 503" ? "signer_unavailable" : "version_conflict",
          kind,
        ),
      );
    const user = userEvent.setup();
    const onPublished = vi.fn(async () => {});
    const { container } = render(workflow(onPublished));
    await stage(container, user);
    await user.click(
      screen.getByRole("checkbox", { name: "已核对包名、版本和架构" }),
    );
    await user.click(
      screen.getByRole("button", { name: "确认并签署首次快照" }),
    );
    await screen.findByText(kind);
    expect(screen.getByText("widget@1.2.3#amd64")).toBeInTheDocument();
    expect(
      screen.getByRole("checkbox", { name: "已核对包名、版本和架构" }),
    ).toBeChecked();
    await user.click(screen.getByRole("button", { name: "重试首次发布" }));
    await waitFor(() => expect(onPublished).toHaveBeenCalledTimes(1));
    const [first, second] = vi.mocked(publishAptRepositorySnapshot).mock.calls;
    expect(second[0].headers).toEqual(first[0].headers);
    expect(first[0].body).toEqual({
      suite: "stable",
      sequence: 1,
      publicationSessionIds: [session.id],
    });
    expect(second[0].body).toEqual(first[0].body);
    expect(uploadAptPublicationPackage).toHaveBeenCalledTimes(1);
  },
);

it.each([
  { repositoryId: "another-repository" },
  { digest: `sha256:${"f".repeat(64)}` },
  { size: 10 },
  { canonicalIdentity: "" },
])(
  "rejects a server revision not bound to the uploaded file: %j",
  async (change) => {
    vi.mocked(uploadAptPublicationPackage).mockResolvedValueOnce(
      success({ ...revision, ...change }),
    );
    const user = userEvent.setup();
    const { container } = render(workflow());
    await choose(container, user);
    await user.click(screen.getByRole("button", { name: "暂存并核对软件包" }));
    await screen.findByText(
      "服务端软件包身份不完整，或摘要、大小与本次文件不一致，未允许发布。",
    );
    expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();
    expect(publishAptRepositorySnapshot).not.toHaveBeenCalled();
  },
);

it("rejects a mismatched session before sending any package bytes", async () => {
  vi.mocked(createAptPublicationSession).mockResolvedValueOnce(
    success({ ...session, suite: "bookworm" }, 201),
  );
  const user = userEvent.setup();
  const { container } = render(workflow());
  await choose(container, user);
  await user.click(screen.getByRole("button", { name: "暂存并核对软件包" }));
  await screen.findByText("服务端暂存会话与本次文件或目标不一致，未继续上传。");
  expect(uploadAptPublicationPackage).not.toHaveBeenCalled();
});

it.each(["suite", "repository"])(
  "ignores a late create result after changing %s",
  async (scope) => {
    const pending =
      deferred<ReturnType<typeof success<AptPublicationSession>>>();
    vi.mocked(createAptPublicationSession).mockReturnValueOnce(pending.promise);
    const user = userEvent.setup();
    const onPublished = vi.fn(async () => {});
    const view = render(workflow(onPublished));
    await choose(view.container, user);
    await user.click(screen.getByRole("button", { name: "暂存并核对软件包" }));
    view.rerender(
      workflow(
        onPublished,
        scope === "repository" ? { ...repo, id: "another" } : repo,
        scope === "suite" ? "bookworm" : "stable",
      ),
    );
    await act(async () => pending.resolve(success(session, 201)));
    expect(screen.getByText("尚未选择文件")).toBeInTheDocument();
    expect(uploadAptPublicationPackage).not.toHaveBeenCalled();
    expect(onPublished).not.toHaveBeenCalled();
  },
);

it("prevents same-event double submission before React paints a busy state", async () => {
  const pending = deferred<ReturnType<typeof success<AptPublicationSession>>>();
  vi.mocked(createAptPublicationSession).mockReturnValueOnce(pending.promise);
  const user = userEvent.setup();
  const { container } = render(workflow());
  await choose(container, user);
  const button = screen.getByRole("button", { name: "暂存并核对软件包" });
  act(() => {
    button.click();
    button.click();
  });
  expect(createAptPublicationSession).toHaveBeenCalledTimes(1);
  await act(async () => pending.resolve(success(session, 201)));
  await screen.findByText("widget@1.2.3#amd64");
  await user.click(
    screen.getByRole("checkbox", { name: "已核对包名、版本和架构" }),
  );
  const publish = deferred<ReturnType<typeof success<AptRepositorySnapshot>>>();
  vi.mocked(publishAptRepositorySnapshot).mockReturnValueOnce(publish.promise);
  const confirm = screen.getByRole("button", { name: "确认并签署首次快照" });
  act(() => {
    confirm.click();
    confirm.click();
  });
  expect(publishAptRepositorySnapshot).toHaveBeenCalledTimes(1);
  await act(async () => publish.resolve(success(snapshot, 201)));
});

it("keeps existing suites in the management view and refreshes a successful first publication", async () => {
  const user = userEvent.setup();
  const view = render(
    <TestQueryProvider>
      <PreferencesProvider>
        <APTOperationsTab repo={repo} canAdmin />
      </PreferencesProvider>
    </TestQueryProvider>,
  );
  await screen.findByRole("button", { name: "暂存并核对软件包" });
  await stage(view.container, user);
  vi.mocked(getAptLifecycleState).mockResolvedValue(
    success({ snapshots: [{ snapshot }], packages: [], deletions: [] }),
  );
  await user.click(
    screen.getByRole("checkbox", { name: "已核对包名、版本和架构" }),
  );
  await user.click(screen.getByRole("button", { name: "确认并签署首次快照" }));
  await screen.findByText("当前快照 #1");
  expect(
    screen.queryByRole("button", { name: "暂存并核对软件包" }),
  ).not.toBeInTheDocument();
  expect(screen.getByText(snapshot.keyFingerprint)).toBeInTheDocument();
  expect(getAptLifecycleState).toHaveBeenCalledTimes(2);
});

it("lets an administrator start the first publication in an empty suite", async () => {
  render(
    <TestQueryProvider>
      <PreferencesProvider>
        <APTOperationsTab repo={repo} canAdmin />
      </PreferencesProvider>
    </TestQueryProvider>,
  );
  expect(
    await screen.findByRole("button", { name: "暂存并核对软件包" }),
  ).toBeDisabled();
});
