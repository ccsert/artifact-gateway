import { Alert, Button, Space } from "antd";
import type { AptLifecyclePackage, AptLifecycleState } from "../../client";
import { ErrorBanner } from "../../components/ui/Feedback";
import { Modal } from "../../components/ui/Modal";
import { usePreferences } from "../../lib/preferences";

import { packageName, type Review } from "./aptModel";

/** The change a preview found, applied as a newly signed snapshot. */
export function APTReviewDialog({
  review,
  busy,
  actionError,
  removed,
  restored,
  changedCount,
  onCancel,
  onApply,
}: {
  review: Review | null;
  busy: boolean;
  actionError: unknown;
  removed: AptLifecyclePackage[];
  restored: AptLifecycleState["deletions"];
  changedCount: number;
  onCancel: () => void;
  onApply: () => void;
}) {
  const { t } = usePreferences();
  return (
    <Modal
      open={!!review}
      title={t("repository.reviewSnapshotChange")}
      onClose={onCancel}
      busy={busy}
      footer={
        <Space>
          <Button disabled={busy} onClick={onCancel}>
            {t("repository.cancel")}
          </Button>
          <Button
            color={review?.request.action === "restore" ? "primary" : "danger"}
            variant="outlined"
            loading={busy}
            disabled={!review || changedCount === 0}
            onClick={onApply}
          >
            {t("repository.applyAndSignNewSnapshot")}
          </Button>
        </Space>
      }
    >
      {review && (
        <div className="ag-page-stack">
          {actionError !== null && <ErrorBanner error={actionError} />}
          <p className="ag-apt-wrap">
            {t("repository.basedOnSnapshot")}{" "}
            <code>{review.plan.expectedSnapshotId}</code>
          </p>
          <Alert
            type="warning"
            showIcon
            title={t("repository.removeRestorePackagesRemain", {
              length: review.plan.removeSessionIds.length,
              length2: review.plan.restoreIds.length,
              remainingPackages: review.plan.remainingPackages,
            })}
            description={t(
              "repository.deletedPackagesCanBeRecoveredForDaysSubmission",
              { recoveryDays: review.plan.recoveryDays },
            )}
          />
          {changedCount === 0 ? (
            <p>{t("repository.noMatchingChangesThereIsNothingToApply")}</p>
          ) : (
            <ul className="ag-apt-change-list">
              {removed.map((item) => (
                <li key={item.publicationSessionId}>
                  {t("repository.remove")} · {packageName(item)}{" "}
                  <code>{item.poolPath}</code>
                </li>
              ))}
              {restored.map((item) => (
                <li key={item.id}>
                  {t("repository.restore")} · {packageName(item.package)}{" "}
                  <code>{item.package.poolPath}</code>
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
    </Modal>
  );
}
