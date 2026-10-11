import type { ReactNode } from "react";
import { Alert, Button, Typography } from "antd";
import { usePreferences } from "../../lib/preferences";
import { Modal } from "./Modal";

/**
 * Shows a freshly issued secret exactly once: API keys and service account
 * credentials share it so both say the same thing the same way.
 */
export function OneTimeTokenDialog({
  title,
  warningTitle,
  warningDescription,
  token,
  details,
  doneLabel,
  onDone,
}: {
  title: string;
  warningTitle: string;
  warningDescription: string;
  token: string;
  details?: ReactNode;
  doneLabel: string;
  onDone: () => void;
}) {
  const { t } = usePreferences();
  return (
    <Modal open onClose={onDone} title={title}>
      <div className="space-y-4">
        <Alert
          type="warning"
          showIcon
          title={warningTitle}
          description={warningDescription}
        />
        <div className="rounded-xl border border-line bg-canvas p-4">
          <Typography.Text
            className="block break-all font-mono text-xs"
            copyable={{
              text: token,
              tooltips: [t("identity.copyToken"), t("identity.copied")],
            }}
          >
            {token}
          </Typography.Text>
        </div>
        {details && (
          <div className="rounded-lg border border-line bg-surface-translucent px-3 py-2 text-xs text-fg-tertiary">
            {details}
          </div>
        )}
        <div className="flex justify-end">
          <Button type="primary" onClick={onDone}>
            {doneLabel}
          </Button>
        </div>
      </div>
    </Modal>
  );
}
