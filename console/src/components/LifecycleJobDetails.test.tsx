import { render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { LifecycleJobDetails as LifecycleJobDetailsData } from "../client";
import { PreferencesProvider } from "../lib/preferences";
import { LifecycleJobDetails } from "./LifecycleJobDetails";

const details: LifecycleJobDetailsData = {
  format: "oci",
  sourceRepositoryId: "11111111-1111-4111-8111-111111111111",
  coordinate: "demo/api",
  digest:
    "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
};

describe("LifecycleJobDetails", () => {
  it("renders safe artifact identity fields with copy actions", () => {
    render(
      <PreferencesProvider>
        <LifecycleJobDetails details={details} />
      </PreferencesProvider>,
    );

    expect(screen.getByText("oci")).toBeInTheDocument();
    expect(screen.getByText("demo/api")).toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: "复制" })).toHaveLength(3);
  });

  it("omits source repository details for scan jobs", () => {
    const { container } = render(
      <PreferencesProvider>
        <LifecycleJobDetails
          details={{
            format: "raw",
            coordinate: "release/widget.bin",
            digest:
              "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
          }}
        />
      </PreferencesProvider>,
    );

    expect(within(container).queryByText("源仓库 ID")).not.toBeInTheDocument();
    expect(
      within(container).getAllByRole("button", { name: "复制" }),
    ).toHaveLength(2);
  });
});
