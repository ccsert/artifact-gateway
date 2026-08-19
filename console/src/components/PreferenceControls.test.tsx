import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it } from "vitest";
import { PreferencesProvider } from "../lib/preferences";
import { PreferenceControls } from "./PreferenceControls";

afterEach(() => {
  cleanup();
  localStorage.clear();
});

describe("PreferenceControls", () => {
  it("commits the complete theme through one view transition", async () => {
    const user = userEvent.setup();
    let transitionCalls = 0;
    const original = (document as Document & { startViewTransition?: unknown })
      .startViewTransition;
    Object.defineProperty(document, "startViewTransition", {
      configurable: true,
      value: (update: () => void) => {
        transitionCalls += 1;
        update();
        return {
          finished: Promise.resolve(),
          skipTransition: () => undefined,
        };
      },
    });

    try {
      render(
        <PreferencesProvider>
          <PreferenceControls />
        </PreferencesProvider>,
      );

      await user.click(
        screen.getByRole("button", { name: /选择主题.*Gateway Dark/ }),
      );
      await user.click(
        await screen.findByRole("menuitem", { name: /Aerok Light/ }),
      );

      expect(transitionCalls).toBe(1);
      expect(document.documentElement).toHaveAttribute("data-theme", "light");
      expect(document.documentElement).toHaveAttribute(
        "data-theme-id",
        "aerok-light",
      );
      expect(document.documentElement).not.toHaveAttribute(
        "data-theme-transition",
      );
      expect(localStorage.getItem("ag.console.theme")).toBe("light");
      expect(localStorage.getItem("ag.console.theme.id")).toBe("aerok-light");
      expect(
        screen.getByRole("button", { name: /选择主题.*Aerok Light/ }),
      ).toBeInTheDocument();
    } finally {
      Object.defineProperty(document, "startViewTransition", {
        configurable: true,
        value: original,
      });
    }
  });

  it("opens the language menu without showing a tooltip", async () => {
    const user = userEvent.setup();
    render(
      <PreferencesProvider>
        <PreferenceControls />
      </PreferencesProvider>,
    );

    await user.click(screen.getByRole("button", { name: "语言" }));

    expect(
      await screen.findByRole("menuitem", { name: "中文" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("menuitem", { name: "English" }),
    ).toBeInTheDocument();
    expect(screen.queryByRole("tooltip")).not.toBeInTheDocument();

    await user.click(screen.getByRole("menuitem", { name: "English" }));

    expect(screen.getByRole("button", { name: "Language" })).toHaveTextContent(
      "EN",
    );
    expect(localStorage.getItem("ag.console.locale")).toBe("en-US");
  });
});
