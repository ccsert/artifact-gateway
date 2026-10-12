import type { AptLifecycleState } from "../../client";
import { Badge } from "../../components/ui/Badge";
import { EmptyState } from "../../components/ui/Feedback";
import { formatDate } from "../../lib/format";
import { usePreferences } from "../../lib/preferences";

/** The suite's visible signed snapshot and its signing evidence. */
export function APTCurrentSnapshot({
  current,
}: {
  current: AptLifecycleState["snapshots"][number]["snapshot"] | undefined;
}) {
  const { text } = usePreferences();
  return current ? (
    <div
      className="ag-apt-current"
      aria-label={text("当前签名快照", "Current signed snapshot")}
    >
      <div className="ag-apt-heading">
        <strong>
          {text(
            `当前快照 #${current.sequence}`,
            `Current snapshot #${current.sequence}`,
          )}
        </strong>
        <Badge tone="success">{text("可见", "Visible")}</Badge>
      </div>
      <dl className="ag-apt-evidence">
        <div>
          <dt>{text("快照 ID", "Snapshot ID")}</dt>
          <dd>
            <code>{current.id}</code>
          </dd>
        </div>
        <div>
          <dt>{text("签名指纹", "Signing fingerprint")}</dt>
          <dd>
            <code>{current.keyFingerprint}</code>
          </dd>
        </div>
        <div>
          <dt>{text("签名服务", "Signer")}</dt>
          <dd>{current.signerIdentity}</dd>
        </div>
        <div>
          <dt>{text("发布时间", "Published")}</dt>
          <dd>{formatDate(current.publishedAt)}</dd>
        </div>
      </dl>
    </div>
  ) : (
    <EmptyState
      compact
      title={text("此套件暂无可见快照", "No visible snapshot in this suite")}
      hint={text(
        "请核对套件名，或通过发布 API 创建首个签名快照。",
        "Check the suite name, or create the first signed snapshot through the publication API.",
      )}
    />
  );
}
