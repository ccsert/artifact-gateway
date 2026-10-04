import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { CurrentIdentity, RepositoryQuotaAlertRule } from "../../client";
import { PreferencesProvider } from "../../lib/preferences";
import { QuotaAlertsPanel } from "./QuotaAlertsPanel";

const mocks = vi.hoisted(() => ({
  identity: {
    actor: "fixture-admin",
    kind: "local_session",
    administrator: true,
    role: "admin",
  } as CurrentIdentity,
  listRepositoryQuotaAlertRules: vi.fn(),
  listEmailTargets: vi.fn(),
  getEmailNotificationCapability: vi.fn(),
  listRepositories: vi.fn(),
  createRepositoryQuotaAlertRule: vi.fn(),
  updateRepositoryQuotaAlertRule: vi.fn(),
  deleteRepositoryQuotaAlertRule: vi.fn(),
  listRepositoryQuotaAlertEvents: vi.fn(),
  getEmailDelivery: vi.fn(),
}));
vi.mock("../../lib/auth", () => ({
  useAuth: () => ({ identity: mocks.identity }),
}));
vi.mock("../../client", () => mocks);

const policy = {
  warningBasisPoints: 8000,
  criticalBasisPoints: 9500,
  recoveryBelowBasisPoints: 7500,
  warningForSeconds: 120,
  criticalForSeconds: 30,
  recoveryForSeconds: 300,
  maxSampleAgeSeconds: 60,
};
const rule: RepositoryQuotaAlertRule = {
  id: "rule-a",
  repositoryId: "repo-a",
  targetId: "target-a",
  targetVersion: "target-v1",
  enabled: false,
  deleted: false,
  version: "rule-v1",
  stateVersion: "state-v2",
  policy,
  sequence: 1,
  state: {
    severity: "critical",
    phase: "firing",
    dataState: "stale",
    usedBytes: 0,
    quotaBytes: 10000,
    lastSampleAt: "2026-10-04T12:00:00Z",
  },
  createdAt: "2026-10-04T11:00:00Z",
  updatedAt: "2026-10-04T12:00:00Z",
};
const target = {
  id: "target-a",
  name: "Synthetic operations",
  locale: "zh-CN",
  enabled: true,
  recipientConfigured: true,
  version: "target-v1",
  createdAt: rule.createdAt,
  updatedAt: rule.updatedAt,
};

function mount() {
  return render(
    <PreferencesProvider>
      <QuotaAlertsPanel />
    </PreferencesProvider>,
  );
}
beforeEach(() => {
  vi.resetAllMocks();
  localStorage.clear();
  mocks.identity = {
    actor: "fixture-admin",
    kind: "local_session",
    administrator: true,
    role: "admin",
  };
  mocks.listRepositoryQuotaAlertRules.mockResolvedValue({ data: [rule] });
  mocks.listEmailTargets.mockResolvedValue({ data: [target] });
  mocks.getEmailNotificationCapability.mockResolvedValue({
    data: { enabled: false, reason: "email_disabled" },
  });
  mocks.listRepositories.mockResolvedValue({
    data: {
      items: [
        {
          id: "repo-a",
          name: "Synthetic Raw",
          format: "raw",
          state: "active",
          type: "hosted",
        },
      ],
    },
  });
  mocks.listRepositoryQuotaAlertEvents.mockResolvedValue({ data: [] });
});
afterEach(cleanup);

it("keeps a password-change denial across cancel/back and clears it only after an explicit successful refresh", async () => {
  mocks.getEmailNotificationCapability.mockResolvedValue({
    data: { enabled: true, reason: "ready" },
  });
  mocks.updateRepositoryQuotaAlertRule.mockResolvedValue({
    error: { code: "password_change_required" },
  });
  mount();
  await userEvent.click(
    await screen.findByRole("button", { name: "查看 Synthetic Raw" }),
  );
  await userEvent.click(screen.getByRole("button", { name: "启用规则" }));
  await userEvent.click(screen.getByRole("button", { name: "确认启用" }));
  expect(await screen.findByText(/必须先修改密码/)).toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "取消" }));
  expect(screen.getByRole("button", { name: "编辑规则" })).toBeDisabled();
  await userEvent.click(screen.getByRole("button", { name: "返回规则列表" }));
  expect(screen.getByRole("button", { name: "创建规则" })).toBeDisabled();
  await userEvent.click(screen.getByRole("button", { name: "刷新" }));
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "创建规则" })).toBeEnabled(),
  );
  expect(mocks.updateRepositoryQuotaAlertRule).toHaveBeenCalledTimes(1);
});

it("keeps a loaded repository page and selection while refreshing alert rules", async () => {
  mocks.listRepositoryQuotaAlertRules.mockResolvedValue({ data: [] });
  mocks.listRepositories.mockImplementation(({ query }) =>
    Promise.resolve({
      data: query.pageToken
        ? {
            items: [
              {
                id: "repo-b",
                name: "Second page repository",
                state: "active",
                type: "hosted",
                format: "raw",
              },
            ],
          }
        : {
            items: [
              {
                id: "repo-a",
                name: "Synthetic Raw",
                state: "active",
                type: "hosted",
                format: "raw",
              },
            ],
            nextPageToken: "page-two",
          },
    }),
  );
  mount();
  await userEvent.click(
    await screen.findByRole("button", { name: "创建规则" }),
  );
  await userEvent.click(screen.getByRole("button", { name: "加载更多仓库" }));
  await userEvent.click(screen.getByRole("combobox", { name: "仓库" }));
  await userEvent.click(
    await screen.findByRole("option", { name: "Second page repository" }),
  );
  await userEvent.click(screen.getByRole("button", { name: "刷新" }));
  await waitFor(() =>
    expect(mocks.listRepositoryQuotaAlertRules).toHaveBeenCalledTimes(2),
  );
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "刷新" })).not.toHaveClass(
      "ant-btn-loading",
    ),
  );
  await userEvent.click(screen.getByRole("combobox", { name: "仓库" }));
  expect(
    await screen.findByRole("option", { name: "Second page repository" }),
  ).toBeInTheDocument();
  expect(
    screen.getByRole("combobox", { name: "仓库" }).closest(".ant-select"),
  ).toHaveTextContent("Second page repository");
});

it("ignores an older read that resolves after a newer refresh", async () => {
  mount();
  await screen.findByText("Synthetic Raw");
  let finish:
    ((value: { data: RepositoryQuotaAlertRule[] }) => void) | undefined;
  mocks.listRepositoryQuotaAlertRules.mockImplementationOnce(
    () =>
      new Promise((resolve) => {
        finish = resolve;
      }),
  );
  await userEvent.click(screen.getByRole("button", { name: "刷新" }));
  mocks.listRepositoryQuotaAlertRules.mockResolvedValue({ data: [] });
  await act(async () => {
    document.dispatchEvent(new Event("visibilitychange"));
  });
  expect(await screen.findByText("尚无配额告警规则")).toBeInTheDocument();
  await act(async () => {
    finish?.({ data: [rule] });
  });
  expect(screen.queryByText("Synthetic Raw")).not.toBeInTheDocument();
});

it("holds a root read authorization denial through automatic recovery until an explicit refresh", async () => {
  mount();
  await screen.findByText("Synthetic Raw");
  mocks.listRepositoryQuotaAlertRules.mockResolvedValue({
    error: { code: "access_denied" },
  });
  await userEvent.click(screen.getByRole("button", { name: "刷新" }));
  await screen.findByText(/上次读取的数据/);
  mocks.listRepositoryQuotaAlertRules.mockResolvedValue({ data: [rule] });
  await act(async () => {
    document.dispatchEvent(new Event("visibilitychange"));
  });
  await waitFor(() =>
    expect(screen.queryByText(/上次读取的数据/)).not.toBeInTheDocument(),
  );
  expect(screen.getByRole("button", { name: "创建规则" })).toBeDisabled();
  await userEvent.click(screen.getByRole("button", { name: "刷新" }));
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "创建规则" })).toBeEnabled(),
  );
});

it("does not let a pending explicit refresh clear a newer event authorization denial", async () => {
  mount();
  await userEvent.click(
    await screen.findByRole("button", { name: "查看 Synthetic Raw" }),
  );
  await screen.findByText("尚无告警事件");
  let finish:
    ((value: { data: RepositoryQuotaAlertRule[] }) => void) | undefined;
  mocks.listRepositoryQuotaAlertRules.mockImplementationOnce(
    () =>
      new Promise((resolve) => {
        finish = resolve;
      }),
  );
  await userEvent.click(screen.getByRole("button", { name: "刷新" }));
  mocks.listRepositoryQuotaAlertEvents.mockResolvedValue({
    error: { code: "access_denied" },
  });
  await userEvent.click(screen.getByRole("button", { name: "刷新事件" }));
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "编辑规则" })).toBeDisabled(),
  );
  await act(async () => {
    finish?.({ data: [rule] });
  });
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "刷新" })).not.toHaveClass(
      "ant-btn-loading",
    ),
  );
  expect(screen.getByRole("button", { name: "编辑规则" })).toBeDisabled();
  await userEvent.click(screen.getByRole("button", { name: "刷新" }));
  await waitFor(() =>
    expect(screen.getByRole("button", { name: "编辑规则" })).toBeEnabled(),
  );
});

it("locks duplicate submissions and ignores a late mutation after leaving the panel", async () => {
  let finish: ((value: { data: RepositoryQuotaAlertRule }) => void) | undefined;
  mocks.updateRepositoryQuotaAlertRule.mockImplementation(
    () =>
      new Promise((resolve) => {
        finish = resolve;
      }),
  );
  const view = mount();
  await userEvent.click(
    await screen.findByRole("button", { name: "查看 Synthetic Raw" }),
  );
  await userEvent.click(screen.getByRole("button", { name: "编辑规则" }));
  const button = screen.getByRole("button", { name: "保存规则" });
  act(() => {
    fireEvent.click(button);
    fireEvent.click(button);
  });
  expect(mocks.updateRepositoryQuotaAlertRule).toHaveBeenCalledTimes(1);
  view.unmount();
  mocks.listRepositoryQuotaAlertRules.mockResolvedValue({ data: [] });
  mount();
  await screen.findByText("尚无配额告警规则");
  await act(async () => {
    finish?.({ data: rule });
  });
  expect(screen.queryByText("规则已保存")).not.toBeInTheDocument();
  expect(screen.getByText("尚无配额告警规则")).toBeInTheDocument();
});

describe("quota alerts read boundary", () => {
  it("shows retained severity separately from stale evidence and mail prerequisites", async () => {
    mount();
    expect(screen.getByRole("status")).toHaveAttribute("aria-busy", "true");
    expect(await screen.findByText("Synthetic Raw")).toBeInTheDocument();
    expect(screen.getByText("严重")).toBeInTheDocument();
    expect(screen.getByText("样本陈旧")).toBeInTheDocument();
    expect(screen.getByText(/邮件通道未开启/)).toBeInTheDocument();
    expect(screen.getByText(/不表示 S3、NAS/)).toBeInTheDocument();
    expect(mocks.createRepositoryQuotaAlertRule).not.toHaveBeenCalled();
  });
  it("has one initial error state and never echoes unsafe error detail", async () => {
    mocks.listRepositoryQuotaAlertRules.mockRejectedValue(
      new Error("smtp-password-sensitive-marker"),
    );
    mount();
    expect(await screen.findByRole("alert")).toHaveTextContent("请求失败");
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
    expect(screen.queryByText(/sensitive-marker/)).not.toBeInTheDocument();
  });
  it("retains last rows on refresh failure and marks them old", async () => {
    mount();
    await screen.findByText("Synthetic Raw");
    mocks.listRepositoryQuotaAlertRules.mockResolvedValue({
      error: { code: "internal_error", message: "unsafe" },
    });
    await userEvent.click(screen.getByRole("button", { name: "刷新" }));
    expect(await screen.findByText(/上次读取的数据/)).toBeInTheDocument();
    expect(screen.getByText("Synthetic Raw")).toBeInTheDocument();
  });
  it("does not request administrator data for a member", async () => {
    mocks.identity = {
      ...mocks.identity,
      administrator: false,
      role: "member",
    };
    mount();
    expect(screen.getByText("仅平台管理员可管理配额告警")).toBeInTheDocument();
    await waitFor(() =>
      expect(mocks.listRepositoryQuotaAlertRules).not.toHaveBeenCalled(),
    );
  });
  it("shows empty rules independently from missing targets", async () => {
    mocks.listRepositoryQuotaAlertRules.mockResolvedValue({ data: [] });
    mocks.listEmailTargets.mockResolvedValue({ data: [] });
    mount();
    expect(await screen.findByText("尚无配额告警规则")).toBeInTheDocument();
    expect(screen.getByText(/“邮件通知”页签新建目标/)).toBeInTheDocument();
  });
});

async function fillPolicy() {
  for (const [label, value] of [
    ["警告阈值（%）", "80.01"],
    ["严重阈值（%）", "95"],
    ["恢复阈值（%）", "75"],
    ["警告持续时间（秒）", "120"],
    ["严重持续时间（秒）", "30"],
    ["恢复持续时间（秒）", "300"],
    ["最大样本年龄（秒）", "60"],
  ])
    await userEvent.type(screen.getByRole("textbox", { name: label }), value);
}
describe("quota alerts explicit writes", () => {
  it("starts with blank policy and creates an explicitly disabled rule with exact basis points", async () => {
    mocks.listRepositoryQuotaAlertRules.mockResolvedValue({ data: [] });
    mocks.createRepositoryQuotaAlertRule.mockResolvedValue({ data: rule });
    mount();
    await userEvent.click(
      await screen.findByRole("button", { name: "创建规则" }),
    );
    expect(screen.getByRole("textbox", { name: "警告阈值（%）" })).toHaveValue(
      "",
    );
    await userEvent.click(screen.getByRole("combobox", { name: "仓库" }));
    await userEvent.click(
      await screen.findByRole("option", { name: "Synthetic Raw" }),
    );
    await userEvent.click(screen.getByRole("combobox", { name: "邮件目标" }));
    await userEvent.click(
      await screen.findByRole("option", { name: /Synthetic operations/ }),
    );
    await fillPolicy();
    await userEvent.click(screen.getByRole("button", { name: "创建停用规则" }));
    await waitFor(() =>
      expect(mocks.createRepositoryQuotaAlertRule).toHaveBeenCalledTimes(1),
    );
    expect(mocks.createRepositoryQuotaAlertRule.mock.calls[0][0].body).toEqual({
      repositoryId: "repo-a",
      targetId: "target-a",
      enabled: false,
      policy: { ...policy, warningBasisPoints: 8001 },
    });
    expect(await screen.findByText("规则已保存")).toBeInTheDocument();
  });
  it("validates threshold ordering without silently rounding invalid precision", async () => {
    mocks.listRepositoryQuotaAlertRules.mockResolvedValue({ data: [] });
    mount();
    await userEvent.click(
      await screen.findByRole("button", { name: "创建规则" }),
    );
    await fillPolicy();
    await userEvent.type(
      screen.getByRole("textbox", { name: "警告阈值（%）" }),
      "1",
    );
    await userEvent.click(screen.getByRole("button", { name: "创建停用规则" }));
    expect(await screen.findByText(/恢复阈值必须低于警告/)).toBeInTheDocument();
    expect(mocks.createRepositoryQuotaAlertRule).not.toHaveBeenCalled();
    await userEvent.click(screen.getByRole("button", { name: "取消" }));
    expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
  });
  it("preserves edit input after a CAS conflict and does not claim success", async () => {
    mocks.updateRepositoryQuotaAlertRule.mockResolvedValue({
      error: { code: "version_conflict", message: "smtp-sensitive-marker" },
    });
    mount();
    await userEvent.click(
      await screen.findByRole("button", { name: "查看 Synthetic Raw" }),
    );
    await userEvent.click(screen.getByRole("button", { name: "编辑规则" }));
    const field = screen.getByRole("textbox", { name: "警告阈值（%）" });
    await userEvent.clear(field);
    await userEvent.type(field, "81.25");
    await userEvent.click(screen.getByRole("button", { name: "保存规则" }));
    expect(
      await screen.findByText(/规则已被其他管理员修改/),
    ).toBeInTheDocument();
    expect(field).toHaveValue("81.25");
    expect(
      mocks.updateRepositoryQuotaAlertRule.mock.calls[0][0].headers,
    ).toEqual({ "If-Match": "rule-v1" });
    expect(screen.queryByText("规则已保存")).not.toBeInTheDocument();
    expect(screen.queryByText(/sensitive-marker/)).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "保存规则" })).toBeDisabled();
  });
});

describe("quota alert evidence and control", () => {
  it("reads events and accepted delivery details without offering send or replay", async () => {
    mocks.listRepositoryQuotaAlertEvents.mockResolvedValue({
      data: [
        {
          id: "event-a",
          ruleId: "rule-a",
          ruleVersion: "rule-v1",
          repositoryId: "repo-a",
          repositoryName: "Synthetic Raw",
          episodeId: "episode-a",
          sequence: 1,
          scenario: "critical",
          usedBytes: 9900,
          quotaBytes: 10000,
          policy,
          occurredAt: rule.updatedAt,
          sampleAt: rule.updatedAt,
          evidenceSince: rule.createdAt,
          targetId: "target-a",
          targetVersion: "target-v1",
          notificationCode: "queued",
          templateVersion: "quota-1",
          deliveryId: "delivery-a",
          deliveryState: "accepted",
        },
      ],
    });
    mocks.getEmailDelivery.mockResolvedValue({
      data: {
        id: "delivery-a",
        eventId: "event-a",
        targetId: "target-a",
        targetVersion: "target-v1",
        scenario: "critical",
        locale: "zh-CN",
        templateVersion: "quota-1",
        kind: "repository_quota",
        state: "accepted",
        attempts: 2,
        possibleDuplicate: true,
        version: "delivery-v2",
        acceptedAt: rule.updatedAt,
        createdAt: rule.createdAt,
        updatedAt: rule.updatedAt,
      },
    });
    mount();
    await userEvent.click(
      await screen.findByRole("button", { name: "查看 Synthetic Raw" }),
    );
    expect(await screen.findByText("SMTP 服务器已接受")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "查看交付详情" }));
    expect(await screen.findByText(/可能重复/)).toBeInTheDocument();
    expect(screen.getByText(/不表示收件箱已送达/)).toBeInTheDocument();
    expect(screen.getByText(/历史样本/)).toHaveTextContent("0 B");
    expect(
      screen.queryByRole("button", { name: /发送|重放/ }),
    ).not.toBeInTheDocument();
  });
  it("cannot enable when email encryption is unavailable", async () => {
    mocks.getEmailNotificationCapability.mockResolvedValue({
      data: { enabled: false, reason: "encryption_key_unavailable" },
    });
    mount();
    await userEvent.click(
      await screen.findByRole("button", { name: "查看 Synthetic Raw" }),
    );
    expect(screen.getByRole("button", { name: "启用规则" })).toBeDisabled();
    expect(mocks.updateRepositoryQuotaAlertRule).not.toHaveBeenCalled();
  });
  it("requires purpose and target confirmation before enabling and uses CAS", async () => {
    mocks.getEmailNotificationCapability.mockResolvedValue({
      data: { enabled: true, reason: "ready" },
    });
    mocks.updateRepositoryQuotaAlertRule.mockResolvedValue({
      data: { ...rule, enabled: true, version: "rule-v2" },
    });
    mount();
    await userEvent.click(
      await screen.findByRole("button", { name: "查看 Synthetic Raw" }),
    );
    await userEvent.click(screen.getByRole("button", { name: "启用规则" }));
    expect(screen.getByText(/后续真实超限事件/)).toBeInTheDocument();
    expect(
      screen.getAllByText(/Synthetic operations/, { selector: "p" }),
    ).toHaveLength(2);
    expect(mocks.updateRepositoryQuotaAlertRule).not.toHaveBeenCalled();
    await userEvent.click(screen.getByRole("button", { name: "确认启用" }));
    await waitFor(() =>
      expect(mocks.updateRepositoryQuotaAlertRule).toHaveBeenCalledTimes(1),
    );
    expect(mocks.updateRepositoryQuotaAlertRule.mock.calls[0][0]).toMatchObject(
      { headers: { "If-Match": "rule-v1" }, body: { enabled: true } },
    );
  });
  it("cancelled delete leaves the rule unchanged and a confirmed delete retains history", async () => {
    mocks.deleteRepositoryQuotaAlertRule.mockResolvedValue({
      response: { status: 204 },
    });
    mount();
    await userEvent.click(
      await screen.findByRole("button", { name: "查看 Synthetic Raw" }),
    );
    await userEvent.click(screen.getByRole("button", { name: "删除规则" }));
    expect(screen.getByText(/保留历史，不会生成恢复事件/)).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "取消" }));
    expect(mocks.deleteRepositoryQuotaAlertRule).not.toHaveBeenCalled();
    await userEvent.click(screen.getByRole("button", { name: "删除规则" }));
    await userEvent.click(screen.getByRole("button", { name: "确认删除" }));
    await waitFor(() =>
      expect(mocks.deleteRepositoryQuotaAlertRule).toHaveBeenCalledTimes(1),
    );
    expect(
      mocks.deleteRepositoryQuotaAlertRule.mock.calls[0][0].headers,
    ).toEqual({ "If-Match": "rule-v1" });
  });
});
