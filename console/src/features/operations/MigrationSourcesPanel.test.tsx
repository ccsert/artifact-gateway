import {
  cleanup,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  deleteMigrationSource,
  discoverMigrationSource,
  getMigrationSource,
  listMigrationSources,
  preflightMigrationSource,
  previewMigrationSourceMapping,
} from "../../client";
import { AntdProvider } from "../../app/AntdProvider";
import { PreferencesProvider } from "../../lib/preferences";
import { TestQueryProvider } from "../../test/queryClient";
import {
  migrationDiscovery,
  migrationMapping,
  migrationPreflight,
  migrationSource,
} from "../../test/migrationFixtures";
import { MigrationSourcesPanel } from "./MigrationSourcesPanel";

vi.mock("../../client", () => ({
  listMigrationSources: vi.fn(),
  getMigrationSource: vi.fn(),
  createMigrationSource: vi.fn(),
  updateMigrationSource: vi.fn(),
  deleteMigrationSource: vi.fn(),
  preflightMigrationSource: vi.fn(),
  discoverMigrationSource: vi.fn(),
  previewMigrationSourceMapping: vi.fn(),
}));
const list = vi.mocked(listMigrationSources);
const preflight = vi.mocked(preflightMigrationSource);
const discover = vi.mocked(discoverMigrationSource);
const preview = vi.mocked(previewMigrationSourceMapping);
const detail = vi.mocked(getMigrationSource);

function show(source = migrationSource.id) {
  return render(
    <TestQueryProvider>
      <PreferencesProvider>
        <AntdProvider>
          <MemoryRouter
            initialEntries={[
              `/operations?tab=migrations${source ? `&source=${source}` : ""}`,
            ]}
          >
            <MigrationSourcesPanel />
          </MemoryRouter>
        </AntdProvider>
      </PreferencesProvider>
    </TestQueryProvider>,
  );
}
beforeEach(() => {
  list.mockResolvedValue({
    data: { items: [migrationSource], nextCursor: "" },
  } as never);
  detail.mockResolvedValue({ data: migrationSource } as never);
  preflight.mockResolvedValue({ data: migrationPreflight } as never);
  discover.mockResolvedValue({ data: migrationDiscovery } as never);
  preview.mockResolvedValue({ data: migrationMapping } as never);
});
afterEach(() => {
  cleanup();
  vi.resetAllMocks();
});

describe("migration source observations", () => {
  it("renders one initial error and sanitizes transport messages", async () => {
    list.mockRejectedValue(new Error("password=never-render-this"));
    show("");
    await screen.findByText(/请求未完成/);
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
    expect(screen.queryByText("尚未配置迁移源")).not.toBeInTheDocument();
    expect(document.body).not.toHaveTextContent("never-render-this");
  });
  it("keeps loaded sources on refresh failure and supports retry", async () => {
    list
      .mockResolvedValueOnce({
        data: { items: [migrationSource], nextCursor: "" },
      } as never)
      .mockRejectedValueOnce(new Error("offline"))
      .mockResolvedValue({
        data: { items: [migrationSource], nextCursor: "" },
      } as never);
    show("");
    await screen.findByRole("button", { name: /Build Nexus/ });
    await userEvent.click(screen.getByRole("button", { name: "刷新源" }));
    await screen.findByText(/请求未完成/);
    expect(
      screen.getByRole("button", { name: /Build Nexus/ }),
    ).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: /重试/ }));
    await waitFor(() =>
      expect(screen.queryByText(/请求未完成/)).not.toBeInTheDocument(),
    );
  });
  it("loads a source deep link separately from list pagination", async () => {
    list
      .mockResolvedValueOnce({
        data: { items: [], nextCursor: "opaque-cursor" },
      } as never)
      .mockResolvedValue({
        data: { items: [migrationSource], nextCursor: "" },
      } as never);
    show();
    await screen.findByRole("button", { name: "运行预检" });
    expect(detail).toHaveBeenCalledWith(
      expect.objectContaining({ path: { sourceId: migrationSource.id } }),
    );
    await userEvent.click(
      screen.getByRole("button", { name: "加载更多迁移源" }),
    );
    await waitFor(() =>
      expect(list).toHaveBeenCalledWith(
        expect.objectContaining({
          query: { limit: 100, cursor: "opaque-cursor" },
        }),
      ),
    );
    expect(
      await screen.findByRole("button", { name: /Build Nexus/ }),
    ).toBeInTheDocument();
  });
  it("discovers only explicitly selected supported Hosted repositories", async () => {
    show();
    await userEvent.click(
      await screen.findByRole("button", { name: "运行预检" }),
    );
    expect(
      await screen.findByRole("checkbox", { name: "选择 snapshots" }),
    ).toBeDisabled();
    await userEvent.click(
      screen.getByRole("checkbox", { name: "选择 releases" }),
    );
    await userEvent.click(
      screen.getByRole("checkbox", { name: "选择 central" }),
    );
    await userEvent.click(
      screen.getByRole("button", { name: "发现选中 Hosted 内容（1）" }),
    );
    const result = await screen.findByRole("region", {
      name: "源资产发现结果",
    });
    await within(result).findByText(/2 个源资产/);
    expect(discover).toHaveBeenCalledWith(
      expect.objectContaining({ body: { repositories: ["releases"] } }),
    );
    expect(result).toHaveTextContent("未下载或校验任何字节");
    expect(result).toHaveTextContent("共享或无法归属单一发布版本");
    expect(result).not.toHaveTextContent("100%");
  });
  it("preserves the previous discovery on a failed reread without partial success", async () => {
    discover
      .mockResolvedValueOnce({ data: migrationDiscovery } as never)
      .mockResolvedValue({
        error: {
          status: 502,
          code: "source_limit_exceeded",
          message: "secret",
        },
      } as never);
    show();
    await userEvent.click(
      await screen.findByRole("button", { name: "运行预检" }),
    );
    await userEvent.click(
      await screen.findByRole("checkbox", { name: "选择 releases" }),
    );
    await userEvent.click(
      screen.getByRole("button", { name: "发现选中 Hosted 内容（1）" }),
    );
    const result = await screen.findByRole("region", {
      name: "源资产发现结果",
    });
    await within(result).findByText(/2 个源资产/);
    await userEvent.click(
      screen.getByRole("button", { name: "发现选中 Hosted 内容（1）" }),
    );
    expect(await within(result).findByRole("alert")).toHaveTextContent(
      "安全读取预算",
    );
    expect(result).toHaveTextContent("保留上次成功结果");
    expect(result).toHaveTextContent("2 个源资产");
    expect(result).not.toHaveTextContent("secret");
  });
  it("builds explicit multi-repository mappings with source Group order", async () => {
    show();
    await userEvent.click(
      await screen.findByRole("button", { name: "运行预检" }),
    );
    for (const name of ["public", "central", "releases"]) {
      await userEvent.click(
        await screen.findByRole("checkbox", { name: `选择 ${name}` }),
      );
      await userEvent.type(
        screen.getByRole("textbox", { name: `${name} 的目标名称` }),
        `native-${name}`,
      );
    }
    await userEvent.click(screen.getByRole("button", { name: "预览显式映射" }));
    const result = await screen.findByRole("region", { name: "映射预览结果" });
    await within(result).findByText(/所选映射通过当前校验/);
    expect(preview).toHaveBeenCalledWith(
      expect.objectContaining({
        body: {
          mappings: [
            {
              sourceRepository: "releases",
              targetName: "native-releases",
              format: "maven",
              type: "hosted",
            },
            {
              sourceRepository: "central",
              targetName: "native-central",
              format: "maven",
              type: "proxy",
            },
            {
              sourceRepository: "public",
              targetName: "native-public",
              format: "maven",
              type: "group",
              targetMembers: ["native-releases", "native-central"],
            },
          ],
        },
      }),
    );
    await userEvent.type(
      screen.getByRole("textbox", { name: "releases 的目标名称" }),
      "-changed",
    );
    expect(
      screen.queryByRole("region", { name: "映射预览结果" }),
    ).not.toBeInTheDocument();
  });
  it("rejects a response from a changed connection version", async () => {
    preflight.mockResolvedValue({
      data: { ...migrationPreflight, sourceVersion: "2" },
    } as never);
    show();
    await userEvent.click(
      await screen.findByRole("button", { name: "运行预检" }),
    );
    await screen.findByText(/源连接版本已变化/);
    expect(
      screen.queryByRole("checkbox", { name: "选择 releases" }),
    ).not.toBeInTheDocument();
  });
  it("removes a previous observation when administrator access is refused", async () => {
    preflight
      .mockResolvedValueOnce({ data: migrationPreflight } as never)
      .mockResolvedValue({
        error: { status: 403, message: "hidden-role-detail" },
      } as never);
    show();
    await userEvent.click(
      await screen.findByRole("button", { name: "运行预检" }),
    );
    await screen.findByRole("checkbox", { name: "选择 releases" });
    expect(
      screen.getAllByText("可读取源配置和资产元数据；尚未迁移内容。").length,
    ).toBe(2);
    await userEvent.click(screen.getByRole("button", { name: "运行预检" }));
    await screen.findByText(/没有读取或管理此迁移源的权限/);
    expect(
      screen.queryByRole("checkbox", { name: "选择 releases" }),
    ).not.toBeInTheDocument();
    expect(document.body).not.toHaveTextContent("hidden-role-detail");
  });
  it("does not show a late observation after changing the selected source", async () => {
    const second = {
      ...migrationSource,
      id: "cb7de0a3-79f3-4cf0-9d5e-d29bf5bd1a8c",
      name: "Second source",
    };
    list.mockResolvedValue({
      data: { items: [migrationSource, second], nextCursor: "" },
    } as never);
    detail.mockImplementation(
      async (options) =>
        ({
          data: options.path.sourceId === second.id ? second : migrationSource,
        }) as never,
    );
    let finish!: (value: unknown) => void;
    preflight.mockImplementation(
      () =>
        new Promise((resolve) => {
          finish = resolve;
        }) as never,
    );
    show();
    await userEvent.click(
      await screen.findByRole("button", { name: "运行预检" }),
    );
    const signal = preflight.mock.calls[0][0].signal;
    await userEvent.click(
      screen.getByRole("button", { name: /Second source/ }),
    );
    await screen.findByRole("heading", { name: "Second source" });
    expect(signal?.aborted).toBe(true);
    finish({ data: migrationPreflight });
    expect(
      screen.queryByRole("checkbox", { name: "选择 releases" }),
    ).not.toBeInTheDocument();
  });
  it("uses If-Match for deletion and retains the source on a stale version", async () => {
    vi.mocked(deleteMigrationSource).mockResolvedValue({
      error: { status: 412, code: "version_conflict" },
    } as never);
    show();
    await userEvent.click(
      await screen.findByRole("button", { name: "删除源" }),
    );
    const dialog = await screen.findByRole("dialog", { name: "删除迁移源？" });
    await userEvent.click(
      within(dialog).getByRole("button", { name: "删除源" }),
    );
    expect(await within(dialog).findByRole("alert")).toHaveTextContent(
      "源连接已被其他操作更新",
    );
    expect(vi.mocked(deleteMigrationSource)).toHaveBeenCalledWith({
      path: { sourceId: migrationSource.id },
      headers: { "If-Match": "1" },
    });
    expect(
      screen.getByRole("heading", { name: "Build Nexus" }),
    ).toBeInTheDocument();
  });
});
