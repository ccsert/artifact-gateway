import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { createMigrationSource, updateMigrationSource } from "../../client";
import { AntdProvider } from "../../app/AntdProvider";
import { PreferencesProvider } from "../../lib/preferences";
import { migrationSource } from "../../test/migrationFixtures";
import { MigrationSourceEditor } from "./MigrationSourceEditor";
import { TestQueryProvider } from "../../test/queryClient";

vi.mock("../../client", () => ({
  createMigrationSource: vi.fn(),
  updateMigrationSource: vi.fn(),
}));
afterEach(() => {
  cleanup();
  vi.resetAllMocks();
});
function editor(edit = true) {
  const onSaved = vi.fn();
  const onClose = vi.fn();
  const onReload = vi.fn().mockResolvedValue(undefined);
  render(
    <TestQueryProvider>
      <PreferencesProvider>
        <AntdProvider>
          <MigrationSourceEditor
            source={edit ? migrationSource : null}
            onSaved={onSaved}
            onClose={onClose}
            onReload={onReload}
          />
        </AntdProvider>
      </PreferencesProvider>
    </TestQueryProvider>,
  );
  return { onSaved, onClose, onReload };
}
async function choose(label: string) {
  await userEvent.click(screen.getByRole("combobox", { name: "源凭据" }));
  await userEvent.click(await screen.findByRole("option", { name: label }));
}
describe("migration source credentials", () => {
  it("retains existing credentials by omission and writes with If-Match", async () => {
    vi.mocked(updateMigrationSource).mockResolvedValue({
      data: { ...migrationSource, version: "2" },
    } as never);
    const { onSaved } = editor();
    expect(screen.queryByLabelText("源密码")).not.toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "保存迁移源" }));
    await waitFor(() => expect(onSaved).toHaveBeenCalled());
    expect(updateMigrationSource).toHaveBeenCalledWith({
      path: { sourceId: migrationSource.id },
      headers: { "If-Match": "1" },
      body: { name: migrationSource.name, baseUrl: migrationSource.baseUrl },
    });
  });
  it("clears credentials explicitly without sending empty secret fields", async () => {
    vi.mocked(updateMigrationSource).mockResolvedValue({
      data: { ...migrationSource, credentialsConfigured: false, version: "2" },
    } as never);
    const { onSaved } = editor();
    await choose("清除当前凭据");
    await userEvent.click(screen.getByRole("button", { name: "保存迁移源" }));
    await waitFor(() => expect(onSaved).toHaveBeenCalled());
    expect(updateMigrationSource).toHaveBeenCalledWith(
      expect.objectContaining({
        body: {
          name: migrationSource.name,
          baseUrl: migrationSource.baseUrl,
          clearCredentials: true,
        },
      }),
    );
  });
  it("clears submitted credentials on rejection and never renders an echoed secret", async () => {
    vi.mocked(createMigrationSource).mockRejectedValue(
      new Error("server echoed source-password"),
    );
    editor(false);
    await userEvent.type(
      screen.getByRole("textbox", { name: "源名称" }),
      "Test source",
    );
    await userEvent.type(
      screen.getByRole("textbox", { name: "Nexus HTTPS 地址" }),
      "https://nexus.example.com",
    );
    await choose("设置新凭据");
    await userEvent.type(
      screen.getByRole("textbox", { name: "源用户名" }),
      "source-user",
    );
    await userEvent.type(
      screen.getByLabelText("源密码", { selector: "input" }),
      "source-password",
    );
    await userEvent.click(screen.getByRole("button", { name: "创建迁移源" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("请求未完成");
    expect(screen.getByRole("textbox", { name: "源用户名" })).toHaveValue("");
    expect(screen.getByLabelText("源密码", { selector: "input" })).toHaveValue(
      "",
    );
    expect(screen.getByRole("button", { name: "创建迁移源" })).toBeDisabled();
    expect(document.body).not.toHaveTextContent("source-password");
    expect(localStorage.getItem("source-password")).toBeNull();
  });
  it("requires replacing or clearing credentials when the address changes", async () => {
    editor();
    const address = screen.getByRole("textbox", { name: "Nexus HTTPS 地址" });
    await userEvent.clear(address);
    await userEvent.type(address, "https://other.example.com");
    expect(screen.getByRole("button", { name: "保存迁移源" })).toBeDisabled();
    await choose("清除当前凭据");
    expect(screen.getByRole("button", { name: "保存迁移源" })).toBeEnabled();
  });
  it("offers an explicit reread after a version conflict", async () => {
    vi.mocked(updateMigrationSource).mockResolvedValue({
      error: { status: 412, message: "hidden-server-data" },
    } as never);
    const { onReload } = editor();
    await userEvent.click(screen.getByRole("button", { name: "保存迁移源" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("读取最新版本");
    expect(document.body).not.toHaveTextContent("hidden-server-data");
    await userEvent.click(screen.getByRole("button", { name: "读取最新版本" }));
    await waitFor(() => expect(onReload).toHaveBeenCalledOnce());
  });
});
