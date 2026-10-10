import { Button, Modal as AntdModal, Space } from "antd";
import { useState } from "react";
import type { ReactNode } from "react";
import { usePreferences } from "../../lib/preferences";

/**
 * Every dialog in the console goes through this component: it owns the width
 * tiers, the body height cap and the busy-close protection, so a call site
 * cannot drift into its own variant of any of them.
 *
 * Width: leave unset for a form (520), use `wide` for any dialog that embeds a
 * table (1152), and reach for `width` only for an editor that is genuinely in
 * between. Tables inside a dialog take no `scroll.y` — the body cap below is
 * the single scrolling layer, and a second one makes the dialog scroll against
 * its own content.
 */
export function Modal({
  open,
  title,
  onClose,
  children,
  footer,
  wide,
  width,
  busy,
}: {
  open: boolean;
  title: string;
  onClose: () => void;
  children: ReactNode;
  footer?: ReactNode;
  wide?: boolean;
  /** Overrides both `wide` and the default; for the in-between editors. */
  width?: number;
  /**
   * A mutation is in flight: the dialog cannot be dismissed by escape, mask or
   * close button, so a half-applied change cannot be abandoned by accident.
   */
  busy?: boolean;
}) {
  return (
    <AntdModal
      open={open}
      title={title}
      onCancel={busy ? undefined : onClose}
      footer={footer ?? null}
      centered
      destroyOnHidden
      closable={!busy}
      mask={{ closable: !busy }}
      keyboard={!busy}
      width={width ?? (wide ? 1152 : 520)}
      styles={{
        body: {
          maxHeight: "calc(85vh - 112px)",
          overflowY: "auto",
        },
      }}
    >
      {children}
    </AntdModal>
  );
}

/**
 * The console confirms a destructive action in exactly two shapes: anything
 * irreversible, or anything whose blast radius needs spelling out, uses this
 * dialog; an inline and reversible edit uses `Popconfirm` next to the row.
 * `modal.confirm` is not a third option — it renders outside the shared chrome
 * and re-implements the danger and busy affordances per call site.
 */
export function ConfirmDialog({
  open,
  title,
  message,
  confirmLabel,
  danger,
  busy,
  onConfirm,
  onClose,
}: {
  open: boolean;
  title: string;
  message: ReactNode;
  confirmLabel?: string;
  danger?: boolean;
  busy?: boolean;
  onConfirm: () => void;
  onClose: () => void;
}) {
  const { text } = usePreferences();
  return (
    <Modal
      open={open}
      title={title}
      onClose={onClose}
      busy={busy}
      footer={
        <Space>
          <Button onClick={onClose} disabled={busy}>
            {text("取消", "Cancel")}
          </Button>
          <Button
            type="primary"
            onClick={onConfirm}
            danger={danger}
            loading={busy}
          >
            {confirmLabel ?? text("确认", "Confirm")}
          </Button>
        </Space>
      }
    >
      <div className="text-sm text-fg-secondary">{message}</div>
    </Modal>
  );
}

export function useDisclosure() {
  const [open, setOpen] = useState(false);
  return {
    open,
    show: () => setOpen(true),
    hide: () => setOpen(false),
    toggle: () => setOpen((v) => !v),
  };
}
