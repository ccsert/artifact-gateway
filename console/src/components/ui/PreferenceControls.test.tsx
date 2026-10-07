import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { PreferencesProvider } from "../../lib/preferences";
import { PreferenceControls } from "./PreferenceControls";

afterEach(() => {
  cleanup();
  localStorage.clear();
});

describe("PreferenceControls", () => {
  it("commits the complete theme through one view transition", async () => {
    const user = userEvent.setup();
    let transitionCalls = 0;
    let transitionGeometry: Record<string, string> = {};
    const original = (document as Document & { startViewTransition?: unknown })
      .startViewTransition;
    Object.defineProperty(document, "startViewTransition", {
      configurable: true,
      value: (update: () => void) => {
        transitionCalls += 1;
        transitionGeometry = {
          x: document.documentElement.style.getPropertyValue(
            "--ag-theme-reveal-x",
          ),
          y: document.documentElement.style.getPropertyValue(
            "--ag-theme-reveal-y",
          ),
          radius: document.documentElement.style.getPropertyValue(
            "--ag-theme-reveal-radius",
          ),
        };
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

      const themeButton = screen.getByRole("button", {
        name: /选择主题.*Gateway Dark/,
      });
      vi.spyOn(themeButton, "getBoundingClientRect").mockReturnValue({
        x: 900,
        y: 20,
        left: 900,
        top: 20,
        right: 940,
        bottom: 60,
        width: 40,
        height: 40,
        toJSON: () => ({}),
      });

      await user.click(themeButton);
      await user.click(
        await screen.findByRole("menuitem", { name: /Aerok Light/ }),
      );

      expect(transitionCalls).toBe(1);
      expect(transitionGeometry).toEqual({
        x: "920px",
        y: "40px",
        radius: `${Math.ceil(
          Math.hypot(
            Math.max(920, window.innerWidth - 920),
            Math.max(40, window.innerHeight - 40),
          ),
        )}px`,
      });
      expect(document.documentElement).toHaveAttribute("data-theme", "light");
      expect(document.documentElement).toHaveAttribute(
        "data-theme-id",
        "aerok-light",
      );
      expect(document.documentElement).not.toHaveAttribute(
        "data-theme-transition",
      );
      expect(
        document.documentElement.style.getPropertyValue("--ag-theme-reveal-x"),
      ).toBe("");
      expect(
        document.documentElement.style.getPropertyValue("--ag-theme-reveal-y"),
      ).toBe("");
      expect(
        document.documentElement.style.getPropertyValue(
          "--ag-theme-reveal-radius",
        ),
      ).toBe("");
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

  it("persists the accepted choice while keeping its palette inside the capture update", async () => {
    const user = userEvent.setup();
    let update!: () => void | Promise<void>;
    let resolveFinished!: () => void;
    const original = (document as Document & { startViewTransition?: unknown })
      .startViewTransition;
    Object.defineProperty(document, "startViewTransition", {
      configurable: true,
      value: (callback: () => void | Promise<void>) => {
        update = callback;
        return {
          finished: new Promise<void>((resolve) => {
            resolveFinished = resolve;
          }),
          skipTransition() {},
        };
      },
    });
    try {
      render(
        <PreferencesProvider>
          <PreferenceControls />
        </PreferencesProvider>,
      );
      await user.click(screen.getByRole("button", { name: /选择主题/ }));
      await user.click(
        await screen.findByRole("menuitem", { name: /Gateway Light/ }),
      );
      expect(localStorage.getItem("ag.console.theme.id")).toBe("gateway-light");
      expect(localStorage.getItem("ag.console.theme")).toBe("light");
      expect(document.documentElement).toHaveAttribute(
        "data-theme-id",
        "gateway-dark",
      );
      await act(async () => {
        await update();
        resolveFinished();
      });
      expect(document.documentElement).toHaveAttribute(
        "data-theme-id",
        "gateway-light",
      );
      expect(document.documentElement).not.toHaveAttribute(
        "data-theme-transition",
      );
    } finally {
      Object.defineProperty(document, "startViewTransition", {
        configurable: true,
        value: original,
      });
    }
  });

  it("commits atomically when view transitions are unavailable", async () => {
    const user = userEvent.setup();
    const original = (document as Document & { startViewTransition?: unknown })
      .startViewTransition;
    Object.defineProperty(document, "startViewTransition", {
      configurable: true,
      value: undefined,
    });

    let animationFrameCallbacks: FrameRequestCallback[] = [];
    let animationFrameSpy: { mockRestore: () => void } | undefined;
    try {
      render(
        <PreferencesProvider>
          <PreferenceControls />
        </PreferencesProvider>,
      );

      await user.click(
        screen.getByRole("button", { name: /选择主题.*Gateway Dark/ }),
      );
      const lightTheme = await screen.findByRole("menuitem", {
        name: /Gateway Light/,
      });
      animationFrameSpy = vi
        .spyOn(window, "requestAnimationFrame")
        .mockImplementation((callback) => {
          animationFrameCallbacks.push(callback);
          return animationFrameCallbacks.length;
        });
      await user.click(lightTheme);

      expect(document.documentElement).toHaveAttribute("data-theme", "light");
      expect(document.documentElement).toHaveAttribute(
        "data-theme-transition",
        "instant",
      );
      const pendingCallbacks = animationFrameCallbacks;
      animationFrameCallbacks = [];
      for (const callback of pendingCallbacks) callback(0);
      await waitFor(() =>
        expect(document.documentElement).not.toHaveAttribute(
          "data-theme-transition",
        ),
      );
      expect(
        screen.getByRole("button", { name: /选择主题.*Gateway Light/ }),
      ).toBeInTheDocument();
    } finally {
      animationFrameSpy?.mockRestore();
      Object.defineProperty(document, "startViewTransition", {
        configurable: true,
        value: original,
      });
    }
  });

  it("keeps the theme selection when the browser cannot start its reveal", async () => {
    const user = userEvent.setup();
    const original = (document as Document & { startViewTransition?: unknown })
      .startViewTransition;
    Object.defineProperty(document, "startViewTransition", {
      configurable: true,
      value: () => {
        throw new DOMException("Capture unavailable", "InvalidStateError");
      },
    });
    try {
      render(
        <PreferencesProvider>
          <PreferenceControls />
        </PreferencesProvider>,
      );
      await user.click(screen.getByRole("button", { name: /选择主题/ }));
      await user.click(
        await screen.findByRole("menuitem", { name: /Gateway Light/ }),
      );
      expect(document.documentElement).toHaveAttribute(
        "data-theme-id",
        "gateway-light",
      );
      expect(localStorage.getItem("ag.console.theme.id")).toBe("gateway-light");
      expect(document.documentElement).not.toHaveAttribute(
        "data-theme-transition",
      );
    } finally {
      Object.defineProperty(document, "startViewTransition", {
        configurable: true,
        value: original,
      });
    }
  });

  it("settles a rejected reveal without losing the selected theme", async () => {
    const user = userEvent.setup();
    let rejectFinished!: (reason: Error) => void;
    const original = (document as Document & { startViewTransition?: unknown })
      .startViewTransition;
    Object.defineProperty(document, "startViewTransition", {
      configurable: true,
      value: (update: () => void | Promise<void>) => {
        void update();
        return {
          finished: new Promise<void>((_, reject) => {
            rejectFinished = reject;
          }),
          skipTransition() {},
        };
      },
    });
    try {
      render(
        <PreferencesProvider>
          <PreferenceControls />
        </PreferencesProvider>,
      );
      await user.click(screen.getByRole("button", { name: /选择主题/ }));
      await user.click(
        await screen.findByRole("menuitem", { name: /Gateway Light/ }),
      );
      rejectFinished(new Error("Snapshot skipped"));
      await waitFor(() =>
        expect(document.documentElement).not.toHaveAttribute(
          "data-theme-transition",
        ),
      );
      expect(document.documentElement).toHaveAttribute(
        "data-theme-id",
        "gateway-light",
      );
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
