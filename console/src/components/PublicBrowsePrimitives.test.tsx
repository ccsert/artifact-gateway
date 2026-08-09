import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  MetadataItem,
  SearchableVersionSelect,
  UsageSnippetBlock,
} from "./PublicBrowsePrimitives";
import { PreferencesProvider } from "../lib/preferences";

afterEach(async () => {
  cleanup();
  await new Promise<void>((resolve) => setTimeout(resolve, 0));
});

describe("SearchableVersionSelect", () => {
  it("filters versions by the visible label and reports the selected value", async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();

    render(
      <SearchableVersionSelect
        value=""
        options={[
          { value: "release", label: "1.0.0" },
          { value: "snapshot", label: "1.1-SNAPSHOT #42" },
        ]}
        onChange={onChange}
      />,
    );

    const input = screen.getByRole("combobox");
    await user.click(input);
    expect(screen.getByText("1.0.0")).toBeInTheDocument();
    expect(screen.getByText("1.1-SNAPSHOT #42")).toBeInTheDocument();

    await user.type(input, "snapshot");
    expect(screen.queryByText("1.0.0")).not.toBeInTheDocument();
    expect(screen.getByText("1.1-SNAPSHOT #42")).toBeInTheDocument();

    await user.click(screen.getByText("1.1-SNAPSHOT #42"));
    expect(onChange).toHaveBeenCalledWith(
      "snapshot",
      expect.objectContaining({ label: "1.1-SNAPSHOT #42" }),
    );
  });

  it("renders metadata and exposes a labelled copy action", async () => {
    const user = userEvent.setup();
    const onCopy = vi.fn();

    render(
      <PreferencesProvider>
        <MetadataItem label="摘要" value="sha256:abc" mono />
        <UsageSnippetBlock
          snippet={{ label: "Maven URL", code: "https://gateway.test/maven" }}
          copied={false}
          onCopy={onCopy}
        />
      </PreferencesProvider>,
    );

    expect(screen.getByText("sha256:abc")).toHaveAttribute(
      "title",
      "sha256:abc",
    );
    await user.click(screen.getByRole("button", { name: "复制 Maven URL" }));
    expect(onCopy).toHaveBeenCalledTimes(1);
  });
});
