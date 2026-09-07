import {
  cleanup,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { MemoryRouter } from "react-router-dom";
import { getGroupResolution } from "../../client";
import type { Group, GroupResolution } from "../../client";
import { PreferencesProvider } from "../../lib/preferences";
import { GroupResolutionDialog } from "./GroupResolutionDialog";

vi.mock("../../client", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../../client")>()),
  getGroupResolution: vi.fn(),
}));
const getResolution = vi.mocked(getGroupResolution);
const group: Group = {
  id: "11111111-1111-4111-8111-111111111111",
  name: "raw-chain",
  format: "raw",
  anonymousRead: false,
  members: [],
  version: "1",
};
const plan: GroupResolution = {
  groupId: group.id,
  groupVersion: "1",
  format: "raw",
  strategy: "hosted_first",
  excludedMemberCount: 1,
  members: [
    {
      repositoryId: "22222222-2222-4222-8222-222222222222",
      repositoryName: "hosted-from-server",
      type: "hosted",
      configuredPosition: 3,
      resolutionOrder: 1,
    },
    {
      repositoryId: "33333333-3333-4333-8333-333333333333",
      repositoryName: "proxy-from-server",
      type: "proxy",
      configuredPosition: 0,
      resolutionOrder: 2,
    },
  ],
};
function showDialog() {
  render(
    <MemoryRouter>
      <PreferencesProvider>
        <GroupResolutionDialog group={group} />
      </PreferencesProvider>
    </MemoryRouter>,
  );
}
afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe("GroupResolutionDialog", () => {
  it("shows server order, source names and configured positions without inferring a hit", async () => {
    const user = userEvent.setup();
    getResolution.mockResolvedValue({ data: plan } as never);
    showDialog();
    await user.click(screen.getByRole("button", { name: "解析顺序" }));
    const rows = await screen.findAllByTestId("resolution-member");
    expect(
      within(rows[0]).getByRole("link", { name: "hosted-from-server" }),
    ).toHaveAttribute("href", `/repositories/${plan.members[0].repositoryId}`);
    expect(within(rows[0]).getByText("配置位置 4")).toBeInTheDocument();
    expect(within(rows[1]).getByText("配置位置 1")).toBeInTheDocument();
    expect(
      screen.getByText("1 个配置成员当前未参与解析。"),
    ).toBeInTheDocument();
    expect(screen.getByText(/这里展示候选顺序/)).toBeInTheDocument();
    expect(getResolution).toHaveBeenCalledWith({ path: { groupId: group.id } });
  });

  it("retries an initial rejected request without leaving loading below the error", async () => {
    const user = userEvent.setup();
    getResolution
      .mockRejectedValueOnce(new Error("resolution unavailable"))
      .mockResolvedValueOnce({ data: plan } as never);
    showDialog();
    await user.click(screen.getByRole("button", { name: "解析顺序" }));
    expect(
      await screen.findByText("resolution unavailable"),
    ).toBeInTheDocument();
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /重试/ }));
    expect(await screen.findByText("hosted-from-server")).toBeInTheDocument();
  });

  it("preserves the current members when refresh fails", async () => {
    const user = userEvent.setup();
    getResolution
      .mockResolvedValueOnce({ data: plan } as never)
      .mockRejectedValueOnce(new Error("refresh unavailable"));
    showDialog();
    await user.click(screen.getByRole("button", { name: "解析顺序" }));
    await screen.findByText("hosted-from-server");
    await user.click(screen.getByRole("button", { name: /刷新/ }));
    expect(await screen.findByText("refresh unavailable")).toBeInTheDocument();
    expect(screen.getByText("hosted-from-server")).toBeInTheDocument();
  });

  it("renders an empty result without an active loading state", async () => {
    const user = userEvent.setup();
    getResolution.mockResolvedValueOnce({
      data: { ...plan, members: [] },
    } as never);
    showDialog();
    await user.click(screen.getByRole("button", { name: "解析顺序" }));
    expect(await screen.findByText("暂无可参与解析的成员")).toBeInTheDocument();
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
  });

  it("ignores a response from an earlier opening", async () => {
    const user = userEvent.setup();
    let finish: ((value: never) => void) | undefined;
    getResolution
      .mockImplementationOnce(
        () =>
          new Promise<never>((resolve) => {
            finish = resolve;
          }),
      )
      .mockResolvedValueOnce({ data: plan } as never);
    showDialog();
    await user.click(screen.getByRole("button", { name: "解析顺序" }));
    await user.click(screen.getByRole("button", { name: "关闭" }));
    await user.click(screen.getByRole("button", { name: "解析顺序" }));
    await screen.findByText("hosted-from-server");
    finish?.({ data: { ...plan, members: [] } } as never);
    await waitFor(() =>
      expect(screen.getAllByTestId("resolution-member")).toHaveLength(2),
    );
  });
});
