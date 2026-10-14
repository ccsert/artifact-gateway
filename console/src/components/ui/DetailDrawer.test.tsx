import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { PreferencesProvider } from "../../lib/preferences";
import { DetailDrawer } from "./DetailDrawer";

afterEach(() => cleanup());

function renderDrawer(busy: boolean, onClose: () => void) {
  return render(
    <PreferencesProvider>
      <DetailDrawer
        title="alice"
        busy={busy}
        error={new Error("save failed")}
        onClose={onClose}
      >
        <p>详情</p>
      </DetailDrawer>
    </PreferencesProvider>,
  );
}

describe("DetailDrawer", () => {
  it("cannot be dismissed while a change is saved", async () => {
    const onClose = vi.fn();
    renderDrawer(true, onClose);
    expect(await screen.findByText("详情")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Close" })).toBeNull();
    fireEvent.keyDown(screen.getByRole("dialog"), { key: "Escape" });
    const mask = document.querySelector(".ant-drawer-mask");
    if (mask) fireEvent.click(mask);
    expect(onClose).not.toHaveBeenCalled();
  });

  it("closes normally and shows the last failure above the details", async () => {
    const onClose = vi.fn();
    renderDrawer(false, onClose);
    expect(await screen.findByText("save failed")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Close" }));
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});
