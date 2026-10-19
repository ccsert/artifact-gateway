import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { getPyPiYankState, updatePyPiYankState } from "../../client";
import { PreferencesProvider } from "../../lib/preferences";
import { TestQueryProvider } from "../../test/queryClient";
import { PyPIYankPanel } from "./PyPIYankPanel";

vi.mock("../../client", () => ({
  getPyPiYankState: vi.fn(),
  updatePyPiYankState: vi.fn(),
}));

const state = {
  repositoryId: "repo-1",
  project: "widget",
  version: "1.0",
  revision: "a".repeat(64),
  files: [
    {
      filename: "widget-1.0.tar.gz",
      yanked: false,
      reason: "",
      digest: `sha256:${"b".repeat(64)}`,
      size: 42,
    },
  ],
};

beforeEach(() => {
  vi.mocked(getPyPiYankState).mockResolvedValue({ data: state } as Awaited<
    ReturnType<typeof getPyPiYankState>
  >);
});
afterEach(() => {
  cleanup();
  localStorage.clear();
  vi.resetAllMocks();
});

function mount(canManage = true) {
  return render(
    <TestQueryProvider>
      <PreferencesProvider>
        <PyPIYankPanel
          repositoryId="repo-1"
          project="widget"
          version="1.0"
          canManage={canManage}
        />
      </PreferencesProvider>
    </TestQueryProvider>,
  );
}

describe("PyPI yank operation", () => {
  it("confirms a complete version with a captured revision and refreshes the committed state", async () => {
    vi.mocked(updatePyPiYankState).mockResolvedValue({
      data: {
        ...state,
        revision: "c".repeat(64),
        files: [{ ...state.files[0], yanked: true, reason: "bad ABI" }],
      },
    } as Awaited<ReturnType<typeof updatePyPiYankState>>);
    mount();
    fireEvent.click(await screen.findByRole("button", { name: "管理撤回" }));
    fireEvent.change(screen.getByLabelText("撤回原因（可选）"), {
      target: { value: "bad ABI" },
    });
    fireEvent.click(screen.getByRole("button", { name: "确认撤回" }));
    await waitFor(() =>
      expect(updatePyPiYankState).toHaveBeenCalledWith(
        expect.objectContaining({
          path: { repositoryId: "repo-1", project: "widget", version: "1.0" },
          headers: { "If-Match": `"${state.revision}"` },
          body: { yanked: true, reason: "bad ABI" },
        }),
      ),
    );
    await waitFor(() =>
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
    );
    expect(screen.getByText("已撤回 1 / 1 个文件")).toBeInTheDocument();
  });

  it("retains a failed draft, reports stale revision and offers a deliberate refresh", async () => {
    vi.mocked(updatePyPiYankState).mockResolvedValue({
      error: {
        status: 412,
        code: "version_conflict",
        message: "refresh before retry",
      },
    } as Awaited<ReturnType<typeof updatePyPiYankState>>);
    mount();
    fireEvent.click(await screen.findByRole("button", { name: "管理撤回" }));
    fireEvent.change(screen.getByLabelText("撤回原因（可选）"), {
      target: { value: "keep my reason" },
    });
    fireEvent.click(screen.getByRole("button", { name: "确认撤回" }));
    expect(
      await screen.findByDisplayValue("keep my reason"),
    ).toBeInTheDocument();
    await waitFor(() =>
      expect(
        screen.getByRole("button", { name: "刷新后重新确认" }),
      ).toBeInTheDocument(),
    );
    expect(updatePyPiYankState).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByRole("button", { name: "刷新后重新确认" }));
    await waitFor(() =>
      expect(screen.queryByRole("dialog")).not.toBeInTheDocument(),
    );
    expect(getPyPiYankState).toHaveBeenCalledTimes(2);
  });

  it("does not request management state or expose controls without write authorization", () => {
    mount(false);
    expect(getPyPiYankState).not.toHaveBeenCalled();
    expect(
      screen.queryByRole("button", { name: "管理撤回" }),
    ).not.toBeInTheDocument();
  });

  it("keeps the draft when a successful status carries changed archive identity", async () => {
    vi.mocked(updatePyPiYankState).mockResolvedValue({
      data: {
        ...state,
        files: [
          {
            ...state.files[0],
            yanked: true,
            digest: `sha256:${"c".repeat(64)}`,
          },
        ],
      },
    } as Awaited<ReturnType<typeof updatePyPiYankState>>);
    mount();
    fireEvent.click(await screen.findByRole("button", { name: "管理撤回" }));
    fireEvent.click(screen.getByRole("button", { name: "确认撤回" }));
    expect(
      await screen.findByText("未收到完整的撤回状态，请刷新后核实结果。"),
    ).toBeInTheDocument();
    expect(screen.getByRole("dialog")).toBeInTheDocument();
    expect(updatePyPiYankState).toHaveBeenCalledTimes(1);
  });
});
