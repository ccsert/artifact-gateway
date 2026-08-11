import { cleanup, render } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { FilterBar } from "./ConsolePrimitives";

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
