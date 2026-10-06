import { render, screen, cleanup } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it } from "vitest";
import { PreferencesProvider } from "../../lib/preferences";
import { ConsoleTable } from "./ConsolePrimitives";
import { Pagination } from "./Layout";

afterEach(cleanup);

it("uses the shared total, page sizes and end placement, resetting size changes", async () => {
  const user = userEvent.setup();
  const rows = Array.from({ length: 120 }, (_, key) => ({
    key,
    name: `row-${key}`,
  }));
  const { container } = render(
    <PreferencesProvider>
      <ConsoleTable
        dataSource={rows}
        columns={[{ title: "Name", dataIndex: "name" }]}
      />
    </PreferencesProvider>,
  );
  expect(screen.getByText("第 1-20 项，共 120 项")).toBeVisible();
  expect(container.querySelector(".ant-table-pagination-end")).not.toBeNull();
  await user.click(screen.getByTitle("2"));
  expect(screen.getByText("第 21-40 项，共 120 项")).toBeVisible();
  await user.click(screen.getByRole("combobox"));
  await user.click(screen.getByTitle("50 / page"));
  expect(screen.getByText("第 1-50 项，共 120 项")).toBeVisible();
  expect(container.querySelectorAll("tbody tr.ant-table-row")).toHaveLength(50);
});

it("keeps cursor totals unknown and its explicit log action intact", () => {
  const { container } = render(
    <PreferencesProvider>
      <Pagination hasMore onMore={() => {}} label="加载更早日志" />
    </PreferencesProvider>,
  );
  expect(screen.getByRole("button", { name: /加载更早日志/ })).toBeVisible();
  expect(container.querySelector(".ag-pagination-footer")).not.toBeNull();
  expect(screen.queryByText(/共.*项/)).not.toBeInTheDocument();
});
