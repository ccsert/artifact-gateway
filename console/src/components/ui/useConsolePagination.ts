import type { TablePaginationConfig } from "antd/es/table";
import { usePreferences } from "../../lib/preferences";

export const CONSOLE_PAGE_SIZE = 20;
export const CONSOLE_PAGE_SIZE_OPTIONS = [20, 50, 100];

/** Known totals only. Cursor lists keep the shared load-more footer. */
export function useConsolePagination(): TablePaginationConfig {
  const { t } = usePreferences();
  return {
    placement: ["bottomEnd"],
    size: "middle",
    defaultPageSize: CONSOLE_PAGE_SIZE,
    pageSizeOptions: CONSOLE_PAGE_SIZE_OPTIONS,
    showSizeChanger: true,
    hideOnSinglePage: false,
    showTotal: (total, range) =>
      t("ui.ofItems", { start: range[0], end: range[1], total }),
  };
}
