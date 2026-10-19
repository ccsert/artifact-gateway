import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import userEvent from "@testing-library/user-event";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  getMigrationSource,
  listMigrationSources,
  listRepositories,
  preflightMigrationSource,
  startNexusMigrationBatch,
  getNexusMigrationBatch,
} from "../../client";
import { AntdProvider } from "../../app/AntdProvider";
import { PreferencesProvider } from "../../lib/preferences";
import {
  migrationPreflight,
  migrationSource,
} from "../../test/migrationFixtures";
import {
  nexusBatch,
  nexusCapabilities,
  nexusTargets,
} from "../../test/nexusBatchFixtures";
import { NexusBatchStart } from "./NexusBatchStart";
import { migrationKeys } from "./migrationQueries";

vi.mock("../../client", () => ({
  getMigrationSource: vi.fn(),
  listMigrationSources: vi.fn(),
  listRepositories: vi.fn(),
  preflightMigrationSource: vi.fn(),
  startNexusMigrationBatch: vi.fn(),
  getNexusMigrationBatch: vi.fn(),
}));
beforeEach(() => {
  vi.mocked(getMigrationSource).mockResolvedValue({
    data: migrationSource,
  } as never);
  vi.mocked(listMigrationSources).mockResolvedValue({
    data: { items: [migrationSource], nextCursor: "" },
  } as never);
  vi.mocked(listRepositories).mockResolvedValue({
    data: { items: nexusTargets, nextPageToken: "" },
  } as never);
  vi.mocked(preflightMigrationSource).mockResolvedValue({
    data: migrationPreflight,
  } as never);
  vi.mocked(startNexusMigrationBatch).mockResolvedValue({
    data: { ...nexusBatch, state: "scanning", version: "1", totalBytes: null },
  } as never);
  vi.mocked(getNexusMigrationBatch).mockResolvedValue({
    data: nexusBatch,
  } as never);
});
afterEach(() => {
  cleanup();
  vi.resetAllMocks();
});
function show(
  client = new QueryClient({
    defaultOptions: { queries: { retry: false, gcTime: Infinity } },
  }),
) {
  const accepted = vi.fn();
  render(
    <QueryClientProvider client={client}>
      <PreferencesProvider>
        <AntdProvider>
          <MemoryRouter>
            <NexusBatchStart
              binding={nexusCapabilities}
              draft={{
                sourceId: migrationSource.id,
                mappings: [
                  {
                    sourceRepository: "releases",
                    targetName: "native-releases",
                  },
                ],
              }}
              onClose={() => undefined}
              onCreated={accepted}
            />
          </MemoryRouter>
        </AntdProvider>
      </PreferencesProvider>
    </QueryClientProvider>,
  );
  return accepted;
}
async function fillCapacity() {
  const user = userEvent.setup();
  for (const label of [
    "存储池可用字节",
    "下载峰值字节",
    "上传峰值字节",
    "备份峰值字节",
    "恢复峰值字节",
    "保留余量字节",
  ]) {
    await user.click(screen.getByRole("textbox", { name: label }));
    await user.paste(label === "存储池可用字节" ? "1000000" : "0");
  }
  await user.click(
    screen.getByRole("textbox", { name: "容量观察时间（含时区）" }),
  );
  await user.paste(new Date(Date.now() - 1000).toISOString());
  await user.click(
    screen.getByRole("textbox", { name: "证据有效截止时间（含时区）" }),
  );
  await user.paste(new Date(Date.now() + 600000).toISOString());
}
describe("explicit start", () => {
  for (const status of [401, 403])
    for (const query of ["list", "detail", "preflight"] as const)
      it(`discards an executable draft and its source facts after ${query} refuses ${status}`, async () => {
        const client = new QueryClient({
          defaultOptions: { queries: { retry: false, gcTime: Infinity } },
        });
        const accepted = show(client);
        await userEvent.click(
          await screen.findByRole("button", { name: "运行预检" }),
        );
        await screen.findByRole("combobox", { name: "releases 的目标仓库" });
        await fillCapacity();
        expect(
          screen.getByRole("button", { name: "应用映射并创建批次" }),
        ).toBeEnabled();
        if (query === "preflight") {
          vi.mocked(preflightMigrationSource).mockResolvedValue({
            error: { status },
          } as never);
          await userEvent.click(
            screen.getByRole("button", { name: "运行预检" }),
          );
        } else {
          (query === "list"
            ? vi.mocked(listMigrationSources)
            : vi.mocked(getMigrationSource)
          ).mockResolvedValue({ error: { status } } as never);
          await act(async () => {
            await client.refetchQueries({
              queryKey:
                query === "list"
                  ? migrationKeys.list()
                  : migrationKeys.detail(migrationSource.id),
              exact: true,
            });
          });
        }
        await screen.findByText(/没有读取或管理此迁移源的权限/);
        expect(document.body).not.toHaveTextContent(migrationSource.name);
        expect(
          screen.queryByRole("combobox", { name: "已保存的迁移源" }),
        ).not.toBeInTheDocument();
        expect(
          screen.queryByRole("button", { name: "应用映射并创建批次" }),
        ).not.toBeInTheDocument();
        expect(startNexusMigrationBatch).not.toHaveBeenCalled();
        cleanup();
        show(client);
        await screen.findByText(/没有读取或管理此迁移源的权限/);
        vi.mocked(listMigrationSources).mockResolvedValue({
          data: { items: [migrationSource], nextCursor: "" },
        } as never);
        await userEvent.click(screen.getByRole("button", { name: /重试/ }));
        await screen.findByRole("combobox", { name: "已保存的迁移源" });
        expect(
          screen.getByRole("combobox", { name: "已保存的迁移源" }),
        ).toHaveValue("");
        expect(
          screen.getByRole("textbox", { name: "存储池可用字节" }),
        ).toHaveValue("");
        expect(
          screen.queryByRole("combobox", { name: "releases 的目标仓库" }),
        ).not.toBeInTheDocument();
        expect(accepted).not.toHaveBeenCalled();
      });
  it("does not execute a draft or unknown capacity, then binds real target IDs and re-reads the response", async () => {
    const accepted = show();
    const submit = screen.getByRole("button", { name: "应用映射并创建批次" });
    expect(submit).toBeDisabled();
    await userEvent.click(
      await screen.findByRole("button", { name: "运行预检" }),
    );
    await screen.findByRole("combobox", { name: "releases 的目标仓库" });
    expect(submit).toBeDisabled();
    expect(startNexusMigrationBatch).not.toHaveBeenCalled();
    await fillCapacity();
    await userEvent.click(submit);
    await waitFor(() => expect(accepted).toHaveBeenCalledWith(nexusBatch));
    expect(startNexusMigrationBatch).toHaveBeenCalledWith(
      expect.objectContaining({
        headers: { "Idempotency-Key": expect.any(String) },
        body: expect.objectContaining({
          sourceId: migrationSource.id,
          sourceVersion: "1",
          policyVersion: "release-raw-v1",
          mappings: [
            {
              sourceRepository: "releases",
              targetRepositoryId: nexusTargets[0].id,
            },
          ],
          capacityEvidence: expect.objectContaining({
            targetId: nexusCapabilities.targetId,
            poolId: nexusCapabilities.poolId,
            freeBytes: 1000000,
          }),
        }),
      }),
    );
  });
  it("requires a real compatible target instead of treating an invented target name as a UUID", async () => {
    vi.mocked(listRepositories).mockResolvedValue({
      data: {
        items: [
          { ...nexusTargets[0], type: "proxy" },
          { ...nexusTargets[1], name: "native-releases" },
        ],
        nextPageToken: "",
      },
    } as never);
    show();
    await userEvent.click(
      await screen.findByRole("button", { name: "运行预检" }),
    );
    await fillCapacity();
    expect(
      screen.getByRole("button", { name: "应用映射并创建批次" }),
    ).toBeDisabled();
    expect(startNexusMigrationBatch).not.toHaveBeenCalled();
  });
  it("coalesces repeated clicks while a real start command is in flight", async () => {
    let complete: () => void = () => undefined;
    vi.mocked(startNexusMigrationBatch).mockImplementation(
      () =>
        new Promise<never>((resolve) => {
          complete = () => resolve({ data: nexusBatch } as never);
        }),
    );
    const accepted = show();
    await userEvent.click(
      await screen.findByRole("button", { name: "运行预检" }),
    );
    await screen.findByRole("combobox", { name: "releases 的目标仓库" });
    await fillCapacity();
    await userEvent.dblClick(
      screen.getByRole("button", { name: "应用映射并创建批次" }),
    );
    expect(startNexusMigrationBatch).toHaveBeenCalledTimes(1);
    complete();
    await waitFor(() => expect(accepted).toHaveBeenCalledTimes(1));
  });
  it("rejects a mismatched source-version observation and does not submit from stale preview facts", async () => {
    vi.mocked(preflightMigrationSource).mockResolvedValue({
      data: { ...migrationPreflight, sourceVersion: "2" },
    } as never);
    show();
    await userEvent.click(
      await screen.findByRole("button", { name: "运行预检" }),
    );
    await screen.findByText(/源连接版本已变化/);
    expect(
      screen.getByRole("button", { name: "应用映射并创建批次" }),
    ).toBeDisabled();
    expect(startNexusMigrationBatch).not.toHaveBeenCalled();
  });
});
