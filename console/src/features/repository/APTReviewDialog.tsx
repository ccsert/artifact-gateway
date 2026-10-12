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
  const { text } = usePreferences();
  return (
    <Modal
      open={!!review}
      title={text("审阅快照变更", "Review snapshot change")}
      onClose={onCancel}
      busy={busy}
      footer={
        <Space>
          <Button disabled={busy} onClick={onCancel}>
            {text("取消", "Cancel")}
          </Button>
          <Button
            color={review?.request.action === "restore" ? "primary" : "danger"}
            variant="outlined"
            loading={busy}
            disabled={!review || changedCount === 0}
            onClick={onApply}
          >
            {text("应用并签署新快照", "Apply and sign new snapshot")}
          </Button>
        </Space>
      }
    >
      {review && (
        <div className="ag-page-stack">
          {actionError !== null && <ErrorBanner error={actionError} />}
          <p className="ag-apt-wrap">
            {text("基于快照", "Based on snapshot")}{" "}
            <code>{review.plan.expectedSnapshotId}</code>
          </p>
          <Alert
            type="warning"
            showIcon
            title={text(
              `移除 ${review.plan.removeSessionIds.length} 个，恢复 ${review.plan.restoreIds.length} 个，完成后 ${review.plan.remainingPackages} 个软件包`,
              `Remove ${review.plan.removeSessionIds.length}, restore ${review.plan.restoreIds.length}; ${review.plan.remainingPackages} packages remain`,
            )}
            description={text(
              `删除后可恢复 ${review.plan.recoveryDays} 天。提交会生成新索引和签名，当前快照发生变化时必须重新预览。`,
              `Deleted packages can be recovered for ${review.plan.recoveryDays} days. Submission creates new indices and signatures; a changed current snapshot requires a fresh preview.`,
            )}
          />
          {changedCount === 0 ? (
            <p>
              {text(
                "没有符合条件的变更，无需应用。",
                "No matching changes; there is nothing to apply.",
              )}
            </p>
          ) : (
            <ul className="ag-apt-change-list">
              {removed.map((item) => (
                <li key={item.publicationSessionId}>
                  {text("移除", "Remove")} · {packageName(item)}{" "}
                  <code>{item.poolPath}</code>
                </li>
              ))}
              {restored.map((item) => (
                <li key={item.id}>
                  {text("恢复", "Restore")} · {packageName(item.package)}{" "}
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
