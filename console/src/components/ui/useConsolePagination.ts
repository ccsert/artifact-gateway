import type { TablePaginationConfig } from "antd/es/table";
import { usePreferences } from "../../lib/preferences";

export const CONSOLE_PAGE_SIZE = 20;
export const CONSOLE_PAGE_SIZE_OPTIONS = [20, 50, 100];

/** Known totals only. Cursor lists keep the shared load-more footer. */
export function useConsolePagination(): TablePaginationConfig {
  const { text } = usePreferences();
  return {
    placement: ["bottomEnd"],
    size: "middle",
    defaultPageSize: CONSOLE_PAGE_SIZE,
    pageSizeOptions: CONSOLE_PAGE_SIZE_OPTIONS,
    showSizeChanger: true,
    hideOnSinglePage: false,
    showTotal: (total, range) =>
      text(
        `第 ${range[0]}-${range[1]} 项，共 ${total} 项`,
        `${range[0]}-${range[1]} of ${total} items`,
      ),
  };
}
