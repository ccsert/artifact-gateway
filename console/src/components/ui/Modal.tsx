import { Button, Modal as AntdModal, Space } from "antd";
import { useId, useState } from "react";
import type { ReactNode } from "react";
import { usePreferences } from "../../lib/preferences";
import { ErrorBanner } from "./Feedback";

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
  afterClose,
  children,
  footer,
  wide,
  width,
  busy,
}: {
  open: boolean;
  title: string;
  onClose: () => void;
  /** Called once Ant Design has finished closing the dialog. */
  afterClose?: () => void;
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
      afterClose={afterClose}
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
  afterClose,
}: {
  open: boolean;
  title: string;
  message: ReactNode;
  confirmLabel?: string;
  danger?: boolean;
  busy?: boolean;
  onConfirm: () => void;
  onClose: () => void;
  afterClose?: () => void;
}) {
  const { t } = usePreferences();
  return (
    <Modal
      open={open}
      title={title}
      onClose={onClose}
      afterClose={afterClose}
      busy={busy}
      footer={
        <Space>
          <Button onClick={onClose} disabled={busy}>
            {t("ui.cancel")}
          </Button>
          <Button
            type="primary"
            onClick={onConfirm}
            danger={danger}
            loading={busy}
          >
            {confirmLabel ?? t("ui.confirm")}
          </Button>
        </Space>
      }
    >
      <div className="text-sm text-fg-secondary">{message}</div>
    </Modal>
  );
}

/**
 * A dialog whose body is a form with one submit action. It owns what every
 * form dialog needs: Enter submits, the submit button shows progress and the
 * dialog cannot be dismissed while the request is in flight, and a thrown
 * error is shown above the fields until the next attempt or reopening.
 *
 * `onSubmit` throws to report failure (`unwrap` the SDK result, or throw its
 * `error`); on success it closes the dialog itself, usually through the
 * caller's `onClose` or `onCreated`.
 */
export function FormModal({
  open,
  title,
  submitLabel,
  canSubmit = true,
  onSubmit,
  onClose,
  wide,
  width,
  children,
}: {
  open: boolean;
  title: string;
  submitLabel: string;
  /** The form is complete enough to send. */
  canSubmit?: boolean;
  onSubmit: () => Promise<void>;
  onClose: () => void;
  wide?: boolean;
  width?: number;
  children: ReactNode;
}) {
  const { t } = usePreferences();
  const formId = useId();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  // Reopening starts clean rather than showing the last attempt's failure.
  const [wasOpen, setWasOpen] = useState(open);
  if (open !== wasOpen) {
    setWasOpen(open);
    if (open) setError(null);
  }

  const submit = async () => {
    if (busy || !canSubmit) return;
    setBusy(true);
    setError(null);
    try {
      await onSubmit();
    } catch (nextError) {
      setError(nextError);
    } finally {
      setBusy(false);
    }
  };

  return (
    <Modal
      open={open}
      title={title}
      onClose={onClose}
      busy={busy}
      wide={wide}
      width={width}
      footer={
        <Space>
          <Button onClick={onClose} disabled={busy}>
            {t("ui.cancel")}
          </Button>
          <Button
            type="primary"
            htmlType="submit"
            form={formId}
            loading={busy}
            disabled={!canSubmit}
          >
            {submitLabel}
          </Button>
        </Space>
      }
    >
      <form
        id={formId}
        noValidate
        onSubmit={(event) => {
          event.preventDefault();
          void submit();
        }}
      >
        <div className="space-y-4">
          {error !== null && <ErrorBanner error={error} />}
          {children}
        </div>
      </form>
    </Modal>
  );
}

/**
 * Confirms a destructive action on one target (a row, a credential, …) and
 * runs it. The dialog is open while `target` is set; it stays open and busy
 * while `onConfirm` runs, closes through `onClose` when it succeeds, and on
 * failure either hands the error to `onError` or shows it in the dialog.
 */
export function ConfirmDanger<Target>({
  target,
  title,
  message,
  confirmLabel,
  onConfirm,
  onClose,
  afterClose,
  onDone,
  onError,
}: {
  target: Target | null;
  title: string;
  message: (target: Target) => ReactNode;
  confirmLabel: string;
  onConfirm: (target: Target) => Promise<void>;
  /** Clears `target`; called on cancel and after success. */
  onClose: () => void;
  afterClose?: () => void;
  onDone?: () => void;
  onError?: (error: unknown) => void;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  // Keep the last target on screen while the dialog animates closed.
  const [shown, setShown] = useState(target);
  if (target !== null && target !== shown) {
    setShown(target);
    setError(null);
  }

  const confirm = async () => {
    if (target === null || busy) return;
    setBusy(true);
    setError(null);
    try {
      await onConfirm(target);
      onClose();
      onDone?.();
    } catch (nextError) {
      if (onError) onError(nextError);
      else setError(nextError);
    } finally {
      setBusy(false);
    }
  };

  return (
    <ConfirmDialog
      open={target !== null}
      title={title}
      message={
        shown === null ? null : (
          <>
            {error !== null && (
              <div className="mb-3">
                <ErrorBanner error={error} />
              </div>
            )}
            {message(shown)}
          </>
        )
      }
      confirmLabel={confirmLabel}
      danger
      busy={busy}
      onConfirm={() => void confirm()}
      onClose={onClose}
      afterClose={afterClose}
    />
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
