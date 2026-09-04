import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { PreferencesProvider } from "../lib/preferences";
import { SearchOutlined } from "@ant-design/icons";
import { EmptyState, ErrorBanner, Loading } from "./Feedback";

afterEach(cleanup);

describe("Loading", () => {
  it("announces the busy state without requiring visual inspection", () => {
    render(
      <PreferencesProvider>
        <Loading label="正在核对制品摘要…" />
      </PreferencesProvider>,
    );

    const status = screen.getByRole("status");
    expect(status).toHaveAttribute("aria-live", "polite");
    expect(status).toHaveAttribute("aria-busy", "true");
    expect(status).toHaveTextContent("正在核对制品摘要…");
  });
});

describe("ErrorBanner", () => {
  it("preserves plain-text server errors instead of blaming the network or token", () => {
    render(
      <PreferencesProvider>
        <ErrorBanner error="upstream maintenance in progress" />
      </PreferencesProvider>,
    );

    expect(screen.getByText("upstream maintenance in progress")).toBeVisible();
    expect(
      screen.queryByText("请求失败，请检查网络或 Token"),
    ).not.toBeInTheDocument();
  });

  it("turns an unmounted API route into an actionable version-mismatch message", () => {
    render(
      <PreferencesProvider>
        <ErrorBanner error="404 page not found" />
      </PreferencesProvider>,
    );

    expect(
      screen.getByText(
        "当前 Gateway 未提供此接口，Console 与 Gateway 版本可能不一致。请更新或重启 Gateway 后重试。",
      ),
    ).toBeVisible();
  });
});

describe("EmptyState", () => {
  it("renders a quiet semantic icon without decorative imagery", () => {
    render(
      <PreferencesProvider>
        <EmptyState
          title="No repositories"
          hint="Create the first repository"
          action={<button type="button">New repository</button>}
        />
      </PreferencesProvider>,
    );

    expect(document.querySelector("img")).not.toBeInTheDocument();
    expect(document.querySelector(".ag-empty-state-icon")).toHaveAttribute(
      "aria-hidden",
      "true",
    );
    expect(screen.getByText("No repositories")).toBeVisible();
    expect(
      screen.getByRole("button", { name: "New repository" }),
    ).toBeVisible();
  });

  it("supports compact contextual states without changing their semantics", () => {
    render(
      <PreferencesProvider>
        <EmptyState
          compact
          className="catalog-empty"
          icon={<SearchOutlined data-testid="search-empty-icon" />}
          title="No public repositories"
          hint="Public sources will appear here."
          action={<button type="button">Open management</button>}
        />
      </PreferencesProvider>,
    );

    const empty = screen
      .getByText("No public repositories")
      .closest(".ant-empty");
    expect(empty).toHaveClass(
      "ag-empty-state",
      "ag-empty-state-compact",
      "catalog-empty",
    );
    expect(screen.getByTestId("search-empty-icon")).toBeInTheDocument();
    expect(screen.getByText("Public sources will appear here.")).toBeVisible();
    expect(
      screen.getByRole("button", { name: "Open management" }),
    ).toBeVisible();
  });
});
