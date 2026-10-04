import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { CurrentIdentity, EmailTarget } from "../../client";
import { PreferencesProvider } from "../../lib/preferences";
import { EmailNotificationsPanel } from "./EmailNotificationsPanel";

const mocks = vi.hoisted(() => ({
  identity: {
    actor: "synthetic-admin",
    kind: "local_session",
    administrator: true,
    role: "admin",
  } as CurrentIdentity,
  listEmailTargets: vi.fn(),
  getEmailNotificationCapability: vi.fn(),
  listEmailDeliveries: vi.fn(),
  getEmailTarget: vi.fn(),
  createEmailTarget: vi.fn(),
  updateEmailTarget: vi.fn(),
  previewEmailNotification: vi.fn(),
  testEmailNotification: vi.fn(),
}));
vi.mock("../../lib/auth", () => ({
  useAuth: () => ({ identity: mocks.identity }),
}));
vi.mock("../../client", () => mocks);
const target: EmailTarget = {
  id: "target-a",
  name: "Synthetic operations",
  locale: "zh-CN",
  enabled: true,
  recipientConfigured: true,
  version: "v1",
  createdAt: "2026-01-01T12:00:00Z",
  updatedAt: "2026-01-01T12:00:00Z",
};
function mount() {
  return render(
    <PreferencesProvider>
      <EmailNotificationsPanel />
    </PreferencesProvider>,
  );
}
beforeEach(() => {
  vi.resetAllMocks();
  localStorage.clear();
  mocks.identity.administrator = true;
  mocks.listEmailTargets.mockResolvedValue({ data: [target] });
  mocks.getEmailNotificationCapability.mockResolvedValue({
    data: { enabled: true, reason: "ready" },
  });
  mocks.listEmailDeliveries.mockResolvedValue({ data: [] });
  mocks.previewEmailNotification.mockResolvedValue({
    data: {
      subject: "Synthetic preview",
      html: "<p>Synthetic</p>",
      text: "Synthetic",
      templateVersion: "2",
    },
  });
});
afterEach(cleanup);
it("rejects non-administrators before reading email resources", () => {
  mocks.identity.administrator = false;
  mount();
  expect(screen.getByText(/仅平台管理员/)).toBeInTheDocument();
  expect(mocks.listEmailTargets).not.toHaveBeenCalled();
});
it("creates disabled without sending and clears the sensitive value on cancel", async () => {
  mocks.createEmailTarget.mockResolvedValue({
    data: { ...target, enabled: false },
  });
  mount();
  await userEvent.click(
    await screen.findByRole("button", { name: "新建邮件目标" }),
  );
  await userEvent.type(screen.getByLabelText("目标名称"), "Synthetic");
  await userEvent.type(screen.getByLabelText("收件人地址"), "a@example.test");
  await userEvent.click(screen.getByRole("button", { name: "取消" }));
  await userEvent.click(screen.getByRole("button", { name: "新建邮件目标" }));
  expect(screen.getByLabelText("收件人地址")).toHaveValue("");
  await userEvent.type(screen.getByLabelText("目标名称"), "Synthetic");
  await userEvent.type(screen.getByLabelText("收件人地址"), "a@example.test");
  await userEvent.click(screen.getByRole("button", { name: "保存停用目标" }));
  await waitFor(() => expect(mocks.createEmailTarget).toHaveBeenCalledTimes(1));
  expect(mocks.createEmailTarget.mock.calls[0][0].body).toEqual({
    name: "Synthetic",
    recipient: "a@example.test",
    locale: "zh-CN",
    enabled: false,
  });
  expect(mocks.testEmailNotification).not.toHaveBeenCalled();
  expect(JSON.stringify(localStorage)).not.toContain("a@example.test");
});
it("omits the write-only recipient on edit and uses the target version", async () => {
  mocks.updateEmailTarget.mockResolvedValue({
    data: { ...target, version: "v2" },
  });
  mount();
  await userEvent.click(
    await screen.findByRole("button", { name: "编辑 Synthetic operations" }),
  );
  expect(screen.getByLabelText("收件人地址")).toHaveValue("");
  await userEvent.click(screen.getByRole("button", { name: "保存目标" }));
  await waitFor(() => expect(mocks.updateEmailTarget).toHaveBeenCalledTimes(1));
  expect(mocks.updateEmailTarget.mock.calls[0][0]).toMatchObject({
    headers: { "If-Match": "v1" },
    body: { name: target.name, locale: "zh-CN", enabled: true },
  });
  expect(mocks.updateEmailTarget.mock.calls[0][0].body).not.toHaveProperty(
    "recipient",
  );
});
it("preserves only the safe conflict draft and requires refresh to re-edit", async () => {
  mocks.updateEmailTarget.mockResolvedValue({
    error: { code: "version_conflict", message: "private-raw" },
  });
  mount();
  await userEvent.click(
    await screen.findByRole("button", { name: "编辑 Synthetic operations" }),
  );
  await userEvent.type(
    screen.getByLabelText("收件人地址"),
    "private@example.test",
  );
  await userEvent.click(screen.getByRole("button", { name: "保存目标" }));
  expect(await screen.findByText(/配置已变更/)).toBeInTheDocument();
  expect(screen.getByLabelText("收件人地址")).toHaveValue("");
  expect(screen.getByRole("button", { name: "保存目标" })).toBeDisabled();
  expect(screen.queryByText("private-raw")).not.toBeInTheDocument();
  mocks.getEmailTarget.mockResolvedValue({
    data: { ...target, version: "v2" },
  });
  await userEvent.click(
    screen.getByRole("button", { name: "刷新最新目标并重新编辑" }),
  );
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "保存目标" })).toBeEnabled(),
  );
  expect(screen.getByLabelText("目标名称")).toHaveValue(target.name);
});
it("blocks the whole workspace after a password-change error", async () => {
  mocks.updateEmailTarget.mockResolvedValue({
    error: { code: "password_change_required" },
  });
  mount();
  await userEvent.click(
    await screen.findByRole("button", { name: "编辑 Synthetic operations" }),
  );
  await userEvent.type(
    screen.getByLabelText("收件人地址"),
    "private@example.test",
  );
  await userEvent.click(screen.getByRole("button", { name: "保存目标" }));
  expect(await screen.findByText(/必须先修改密码/)).toBeInTheDocument();
  expect(screen.queryByLabelText("收件人地址")).not.toBeInTheDocument();
  expect(
    screen.queryByRole("button", { name: "预览模板" }),
  ).not.toBeInTheDocument();
});
it.each(["test", "enable", "disable"])(
  "keeps %s confirmation consequences visible until cancel or submission",
  async (kind) => {
    mocks.listEmailTargets.mockResolvedValue({
      data: [{ ...target, enabled: kind !== "enable" }],
    });
    mount();
    const action =
      kind === "test" ? "测试" : kind === "enable" ? "启用" : "停用";
    await userEvent.click(
      await screen.findByRole("button", {
        name: `${action} Synthetic operations`,
      }),
    );
    expect(screen.queryByRole("button", { name: /close/i })).toBeNull();
    expect(
      screen.getByText(
        kind === "test" ? /请求会入真实交付队列/ : /目标版本会变更/,
      ),
    ).toBeInTheDocument();
  },
);
it("confirms the target summary and suppresses repeated send clicks", async () => {
  let finish!: (v: unknown) => void;
  mocks.testEmailNotification.mockImplementation(
    () =>
      new Promise((r) => {
        finish = r;
      }),
  );
  mount();
  await userEvent.click(
    await screen.findByRole("button", { name: "测试 Synthetic operations" }),
  );
  expect(mocks.testEmailNotification).not.toHaveBeenCalled();
  expect(screen.getByText(/v1/)).toBeInTheDocument();
  const confirm = screen.getByRole("button", { name: "确认发送合成测试" });
  fireEvent.click(confirm);
  fireEvent.click(confirm);
  expect(mocks.testEmailNotification).toHaveBeenCalledTimes(1);
  expect(mocks.testEmailNotification.mock.calls[0][0]).toMatchObject({
    body: { targetId: "target-a", scenario: "warning" },
    headers: { "If-Match": "v1" },
  });
  expect(
    mocks.testEmailNotification.mock.calls[0][0].headers["Idempotency-Key"],
  ).toMatch(/^[\da-f-]{36}$/);
  finish({ data: { id: "delivery-a", state: "pending" } });
  expect(await screen.findByText(/合成测试已入队/)).toBeInTheDocument();
});

it("clears the whole workspace immediately on refresh denial while a sibling request hangs", async () => {
  mount();
  await userEvent.click(
    await screen.findByRole("button", { name: "编辑 Synthetic operations" }),
  );
  await userEvent.type(
    screen.getByLabelText("收件人地址"),
    "private@example.test",
  );
  mocks.listEmailDeliveries.mockImplementation(() => new Promise(() => {}));
  mocks.listEmailTargets.mockResolvedValue({
    error: { code: "permission_denied" },
  });
  await userEvent.click(screen.getByRole("button", { name: "刷新" }));
  expect(await screen.findByText(/无权访问邮件配置/)).toBeInTheDocument();
  expect(screen.queryByLabelText("收件人地址")).not.toBeInTheDocument();
  expect(
    screen.queryByRole("button", { name: "保存目标" }),
  ).not.toBeInTheDocument();
});

it("retries an uncertain test using the same immutable idempotency key", async () => {
  mocks.testEmailNotification.mockRejectedValueOnce(
    new Error("private-response"),
  );
  mocks.testEmailNotification.mockResolvedValueOnce({
    data: { id: "d", state: "pending" },
  });
  mount();
  await userEvent.click(
    await screen.findByRole("button", { name: "测试 Synthetic operations" }),
  );
  await userEvent.click(
    screen.getByRole("button", { name: "确认发送合成测试" }),
  );
  expect(await screen.findByText(/结果可能尚未返回/)).toBeInTheDocument();
  expect(screen.queryByText("private-response")).not.toBeInTheDocument();
  await userEvent.click(
    screen.getByRole("button", { name: "确认发送合成测试" }),
  );
  await waitFor(() =>
    expect(mocks.testEmailNotification).toHaveBeenCalledTimes(2),
  );
  const [first, second] = mocks.testEmailNotification.mock.calls.map(
    ([options]) => options,
  );
  expect(second.body).toEqual(first.body);
  expect(second.headers).toEqual(first.headers);
});

it("cancels a pending save and ignores its later success after a new editor opens", async () => {
  let finish!: (v: unknown) => void;
  mocks.updateEmailTarget.mockImplementation(
    () =>
      new Promise((r) => {
        finish = r;
      }),
  );
  mount();
  await userEvent.click(
    await screen.findByRole("button", { name: "编辑 Synthetic operations" }),
  );
  await userEvent.type(
    screen.getByLabelText("收件人地址"),
    "private@example.test",
  );
  await userEvent.click(screen.getByRole("button", { name: "保存目标" }));
  await userEvent.click(screen.getByRole("button", { name: "取消" }));
  await userEvent.click(screen.getByRole("button", { name: "新建邮件目标" }));
  expect(mocks.updateEmailTarget.mock.calls[0][0].signal.aborted).toBe(true);
  finish({ data: target });
  await waitFor(() =>
    expect(
      screen.getByRole("button", { name: "保存停用目标" }),
    ).toBeInTheDocument(),
  );
  expect(screen.getByLabelText("收件人地址")).toHaveValue("");
  expect(screen.queryByText(/邮件目标已保存/)).not.toBeInTheDocument();
});
