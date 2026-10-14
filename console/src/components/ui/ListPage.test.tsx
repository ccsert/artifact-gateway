import {
  act,
  cleanup,
  render,
  renderHook,
  screen,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { MemoryRouter, useLocation } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import { PreferencesProvider } from "../../lib/preferences";
import { FilterCard, ListPage, ListStates } from "./ListPage";
import { useUrlFilters } from "./useUrlFilters";

afterEach(() => cleanup());

const copy = {
  empty: { title: "暂无密钥", hint: "创建一个密钥" },
  unavailable: { title: "分组不可用", hint: "后端未提供分组接口" },
};

function renderStates(props: {
  rows: readonly unknown[] | null;
  loading?: boolean;
  error?: unknown;
}) {
  return render(
    <PreferencesProvider>
      <ListPage title="API 密钥">
        <ListStates
          rows={props.rows}
          loading={props.loading}
          error={props.error ?? null}
          onRetry={() => {}}
          empty={copy.empty}
          unavailable={copy.unavailable}
        >
          <p>表格</p>
        </ListStates>
      </ListPage>
    </PreferencesProvider>,
  );
}

describe("ListStates", () => {
  it("shows the spinner until the first read answers", () => {
    renderStates({ rows: null });
    expect(
      screen.getByRole("heading", { name: "API 密钥" }),
    ).toBeInTheDocument();
    expect(screen.queryByText("表格")).toBeNull();
    expect(screen.queryByText(copy.empty.title)).toBeNull();
  });

  it("shows the empty state for a loaded empty list", () => {
    renderStates({ rows: [] });
    expect(screen.getByText(copy.empty.title)).toBeInTheDocument();
  });

  it("shows the error, or the unavailable state for a missing endpoint, when there is nothing to show", () => {
    renderStates({ rows: [], error: new Error("network down") });
    expect(screen.getByText("network down")).toBeInTheDocument();
    cleanup();
    renderStates({ rows: null, error: { status: 404, code: "not_found" } });
    expect(screen.getByText(copy.unavailable.title)).toBeInTheDocument();
  });

  it("keeps rows on screen under a later failure", () => {
    renderStates({ rows: [1], error: new Error("refresh failed") });
    expect(screen.getByText("refresh failed")).toBeInTheDocument();
    expect(screen.getByText("表格")).toBeInTheDocument();
  });
});

describe("FilterCard", () => {
  it("offers Clear filters only while a filter is set", async () => {
    const user = userEvent.setup();
    const onClear = vi.fn();
    const { rerender } = render(
      <PreferencesProvider>
        <FilterCard
          active={false}
          onClear={onClear}
          filters={<span>筛选</span>}
        >
          <p>表格</p>
        </FilterCard>
      </PreferencesProvider>,
    );
    expect(screen.queryByRole("button", { name: "清除筛选" })).toBeNull();
    rerender(
      <PreferencesProvider>
        <FilterCard active onClear={onClear} filters={<span>筛选</span>}>
          <p>表格</p>
        </FilterCard>
      </PreferencesProvider>,
    );
    await user.click(screen.getByRole("button", { name: "清除筛选" }));
    expect(onClear).toHaveBeenCalledTimes(1);
  });
});

const FILTERS = { q: "", state: "active" as "active" | "revoked" | "all" };
const ALLOWED = { state: ["active", "revoked", "all"] as const };

function useFiltersAndLocation() {
  return { filters: useUrlFilters(FILTERS, ALLOWED), location: useLocation() };
}

function routerAt(entry: string) {
  return ({ children }: { children: ReactNode }) => (
    <MemoryRouter initialEntries={[entry]}>{children}</MemoryRouter>
  );
}

describe("useUrlFilters", () => {
  it("reads the URL, falling back to defaults for missing or unknown values", () => {
    const { result } = renderHook(useFiltersAndLocation, {
      wrapper: routerAt("/keys?q=ci&state=bogus"),
    });
    expect(result.current.filters.values).toEqual({ q: "ci", state: "active" });
    expect(result.current.filters.active).toBe(true);
  });

  it("writes several keys at once and leaves defaults out of the URL", () => {
    const { result } = renderHook(useFiltersAndLocation, {
      wrapper: routerAt("/keys?page=2"),
    });
    act(() => result.current.filters.set({ q: "ci", state: "revoked" }));
    expect(result.current.location.search).toBe("?page=2&q=ci&state=revoked");
    act(() => result.current.filters.reset());
    expect(result.current.location.search).toBe("?page=2");
    expect(result.current.filters.active).toBe(false);
  });
});
