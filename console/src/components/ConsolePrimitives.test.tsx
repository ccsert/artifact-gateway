import { cleanup, render } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { PreferencesProvider } from "../lib/preferences";
import { FilterBar, MetricStrip } from "./ConsolePrimitives";

afterEach(cleanup);

describe("FilterBar", () => {
  it("keeps the standalone surface by default", () => {
    const { container } = render(<FilterBar>Filters</FilterBar>);

    expect(container.firstElementChild).toHaveClass("ag-filter-bar");
    expect(container.firstElementChild).not.toHaveClass(
      "ag-filter-bar-embedded",
    );
  });

  it("uses the embedded surface inside a card", () => {
    const { container } = render(<FilterBar embedded>Filters</FilterBar>);

    expect(container.firstElementChild).toHaveClass(
      "ag-filter-bar",
      "ag-filter-bar-embedded",
    );
  });
});

describe("MetricStrip", () => {
  it("exposes a CSS-controlled column count without truncating values", () => {
    const { container } = render(
      <PreferencesProvider>
        <MetricStrip
          items={[
            { label: "Repositories", value: 12, hint: "10 active" },
            { label: "Groups", value: 3 },
            { label: "Storage", value: "128 GiB" },
          ]}
        />
      </PreferencesProvider>,
    );

    const strip = container.querySelector(".ag-metric-strip");
    expect(strip).toHaveClass("ag-metric-strip-cols-3");
    expect(strip).not.toHaveAttribute("style");
    expect(container.querySelector(".ag-metric-value")).not.toHaveClass(
      "truncate",
    );
  });
});
