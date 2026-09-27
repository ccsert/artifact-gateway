import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { PreferencesProvider } from "../../lib/preferences";
import { ConfirmDialog, Modal } from "./Modal";

afterEach(() => {
  cleanup();
  localStorage.clear();
});

function renderDialog(dialog: ReactNode) {
  return render(<PreferencesProvider>{dialog}</PreferencesProvider>);
}

async function dialogElement() {
  return screen.findByRole("dialog");
}

function maskElement() {
  const mask = document.querySelector(".ant-modal-wrap");
  expect(mask).not.toBeNull();
  return mask as HTMLElement;
}

// The mask only closes on a click that both starts and lands on it, so the
// negative case has to reproduce that full gesture to mean anything.
function clickMask() {
  const mask = maskElement();
  fireEvent.mouseDown(mask);
  fireEvent.click(mask);
}

describe("Modal", () => {
  it("refuses every dismissal route while a mutation is in flight", async () => {
    const onClose = vi.fn();
    renderDialog(
      <Modal open title="删除用户" onClose={onClose} busy>
        <p>正在删除</p>
      </Modal>,
    );
    await dialogElement();

    fireEvent.keyDown(window, { key: "Escape" });
    clickMask();

    expect(onClose).not.toHaveBeenCalled();
    expect(document.querySelector(".ant-modal-close")).toBeNull();
  });

  it("stays dismissible through escape, mask and the close button when idle", async () => {
    const onClose = vi.fn();
    renderDialog(
      <Modal open title="删除用户" onClose={onClose}>
        <p>确认删除？</p>
      </Modal>,
    );
    await dialogElement();

    fireEvent.keyDown(window, { key: "Escape" });
    expect(onClose).toHaveBeenCalledTimes(1);

    clickMask();
    expect(onClose).toHaveBeenCalledTimes(2);

    fireEvent.click(document.querySelector(".ant-modal-close") as HTMLElement);
    expect(onClose).toHaveBeenCalledTimes(3);
  });
});

describe("ConfirmDialog", () => {
  it("locks both actions and the dismissal routes while confirming", async () => {
    const onClose = vi.fn();
    const onConfirm = vi.fn();
    renderDialog(
      <ConfirmDialog
        open
        busy
        danger
        title="撤销全部会话"
        message="已签发的令牌将立即失效。"
        onConfirm={onConfirm}
        onClose={onClose}
      />,
    );
    await dialogElement();

    fireEvent.keyDown(window, { key: "Escape" });
    clickMask();

    expect(onClose).not.toHaveBeenCalled();
    // antd renders a two-character CJK label with a space inside it.
    expect(screen.getByRole("button", { name: /取\s*消/ })).toBeDisabled();

    // A loading button swallows the click instead of taking the disabled
    // attribute, so assert the second confirmation cannot be fired.
    const confirm = screen.getByRole("button", { name: /确\s*认/ });
    expect(confirm).toHaveClass("ant-btn-loading");
    fireEvent.click(confirm);
    expect(onConfirm).not.toHaveBeenCalled();
  });

  it("labels its own buttons in the active locale", async () => {
    localStorage.setItem("ag.console.locale", "en-US");
    renderDialog(
      <ConfirmDialog
        open
        title="Delete user"
        message="The account immediately loses access."
        onConfirm={() => {}}
        onClose={() => {}}
      />,
    );
    await dialogElement();

    expect(screen.getByRole("button", { name: "Cancel" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Confirm" })).toBeInTheDocument();
  });
});
