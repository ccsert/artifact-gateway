import { Drawer } from "antd";
import type { ReactNode } from "react";
import { ErrorBanner } from "./Feedback";

/**
 * The side panel for one record's details and edits. Like `Modal`, it cannot
 * be dismissed by escape, mask or close button while `busy`, so a half-applied
 * change cannot be abandoned; the last failure shows above the content.
 */
export function DetailDrawer({
  title,
  extra,
  footer,
  size = 720,
  busy = false,
  error,
  onClose,
  children,
}: {
  title: ReactNode;
  extra?: ReactNode;
  footer?: ReactNode;
  size?: number;
  /** A change is being saved. */
  busy?: boolean;
  error?: unknown;
  onClose: () => void;
  children: ReactNode;
}) {
  return (
    <Drawer
      open
      size={size}
      destroyOnHidden
      title={title}
      extra={extra}
      footer={footer}
      closable={!busy}
      mask={{ closable: !busy }}
      keyboard={!busy}
      onClose={busy ? undefined : onClose}
    >
      {error ? (
        <div className="mb-4">
          <ErrorBanner error={error} />
        </div>
      ) : null}
      {children}
    </Drawer>
  );
}
