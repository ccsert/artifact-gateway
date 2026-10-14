import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { PreferencesProvider } from "../../lib/preferences";
import { ConfirmDanger, ConfirmDialog, FormModal, Modal } from "./Modal";

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

describe("FormModal", () => {
  it("submits on Enter, locks while saving and shows a failure above the fields", async () => {
    const user = userEvent.setup();
    let reject: (error: unknown) => void = () => {};
    const onSubmit = vi.fn(
      () =>
        new Promise<void>((_, nextReject) => {
          reject = nextReject;
        }),
    );
    const onClose = vi.fn();
    renderDialog(
      <FormModal
        open
        title="新建密钥"
        submitLabel="创建"
        onSubmit={onSubmit}
        onClose={onClose}
      >
        <input aria-label="名称" />
      </FormModal>,
    );

    await user.type(await screen.findByLabelText("名称"), "ci{Enter}");
    expect(onSubmit).toHaveBeenCalledTimes(1);
    expect(screen.getByRole("button", { name: /取\s*消/ })).toBeDisabled();
    clickMask();
    expect(onClose).not.toHaveBeenCalled();

    await act(async () => reject(new Error("network down")));
    expect(await screen.findByText("network down")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /取\s*消/ })).toBeEnabled();
  });

  it("does not submit an incomplete form", async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn(async () => {});
    renderDialog(
      <FormModal
        open
        title="新建密钥"
        submitLabel="创建"
        canSubmit={false}
        onSubmit={onSubmit}
        onClose={() => {}}
      >
        <input aria-label="名称" />
      </FormModal>,
    );
    await user.type(await screen.findByLabelText("名称"), "{Enter}");
    expect(screen.getByRole("button", { name: /创\s*建/ })).toBeDisabled();
    expect(onSubmit).not.toHaveBeenCalled();
  });
});

describe("ConfirmDanger", () => {
  it("runs the action on its target, then closes and reports completion", async () => {
    const user = userEvent.setup();
    const onConfirm = vi.fn(async () => {});
    const onClose = vi.fn();
    const onDone = vi.fn();
    renderDialog(
      <ConfirmDanger
        target={{ name: "ci-deploy" }}
        title="吊销密钥"
        message={(key) => `吊销 ${key.name}？`}
        confirmLabel="吊销"
        onConfirm={onConfirm}
        onClose={onClose}
        onDone={onDone}
      />,
    );
    expect(await screen.findByText("吊销 ci-deploy？")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /^吊\s*销$/ }));
    expect(onConfirm).toHaveBeenCalledWith({ name: "ci-deploy" });
    expect(onClose).toHaveBeenCalledTimes(1);
    expect(onDone).toHaveBeenCalledTimes(1);
  });

  it("keeps the dialog open with the failure when no handler takes it", async () => {
    const user = userEvent.setup();
    const onClose = vi.fn();
    renderDialog(
      <ConfirmDanger
        target={{ name: "ci-deploy" }}
        title="吊销密钥"
        message={(key) => `吊销 ${key.name}？`}
        confirmLabel="吊销"
        onConfirm={async () => {
          throw new Error("network down");
        }}
        onClose={onClose}
      />,
    );
    await user.click(await screen.findByRole("button", { name: /^吊\s*销$/ }));
    expect(await screen.findByText("network down")).toBeInTheDocument();
    expect(onClose).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: /取\s*消/ })).toBeEnabled();
  });
});
