import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { PreferencesProvider } from "../lib/preferences";
import { Loading } from "./Feedback";

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
