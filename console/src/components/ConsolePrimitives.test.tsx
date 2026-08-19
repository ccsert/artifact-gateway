import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AntdProvider } from "../app/AntdProvider";
import { PreferencesProvider } from "../lib/preferences";
import { CopyableValue, FilterBar, MetricStrip } from "./ConsolePrimitives";

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

describe("CopyableValue", () => {
  function renderCopyableValue() {
    return render(
      <PreferencesProvider>
        <AntdProvider>
          <CopyableValue value="sha256:verified" />
        </AntdProvider>
      </PreferencesProvider>,
    );
  }

  it("announces a successful copy and exposes the completed state", async () => {
    const user = userEvent.setup();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { writeText },
    });
    renderCopyableValue();

    await user.click(screen.getByRole("button", { name: "复制" }));

    expect(writeText).toHaveBeenCalledWith("sha256:verified");
    expect(
      await screen.findByRole("button", { name: "已复制" }),
    ).toBeInTheDocument();
    expect(screen.getByRole("status")).toHaveTextContent("已复制到剪贴板");
  });

  it("shows actionable feedback when clipboard access fails", async () => {
    const user = userEvent.setup();
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { writeText: vi.fn().mockRejectedValue(new Error("denied")) },
    });
    renderCopyableValue();

    await user.click(screen.getByRole("button", { name: "复制" }));

    expect(
      await screen.findByText("复制失败，请手动选择并复制该值。"),
    ).toBeInTheDocument();
  });
});
