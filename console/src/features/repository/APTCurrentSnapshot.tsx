import type { AptLifecycleState } from "../../client";
import { Badge } from "../../components/ui/Badge";
import { EmptyState } from "../../components/ui/Feedback";
import { formatDate } from "../../lib/format";
import { usePreferences } from "../../lib/preferences";
import "./apt.css";

/** The suite's visible signed snapshot and its signing evidence. */
export function APTCurrentSnapshot({
  current,
}: {
  current: AptLifecycleState["snapshots"][number]["snapshot"] | undefined;
}) {
  const { t } = usePreferences();
  return current ? (
    <div
      className="ag-apt-current"
      aria-label={t("repository.currentSignedSnapshot")}
    >
      <div className="ag-apt-heading">
        <strong>
          {t("repository.currentSnapshot", { sequence: current.sequence })}
        </strong>
        <Badge tone="success">{t("repository.visible")}</Badge>
      </div>
      <dl className="ag-apt-evidence">
        <div>
          <dt>{t("repository.snapshotID")}</dt>
          <dd>
            <code>{current.id}</code>
          </dd>
        </div>
        <div>
          <dt>{t("repository.signingFingerprint")}</dt>
          <dd>
            <code>{current.keyFingerprint}</code>
          </dd>
        </div>
        <div>
          <dt>{t("repository.signer")}</dt>
          <dd>{current.signerIdentity}</dd>
        </div>
        <div>
          <dt>{t("repository.published2")}</dt>
          <dd>{formatDate(current.publishedAt)}</dd>
        </div>
      </dl>
    </div>
  ) : (
    <EmptyState
      compact
      title={t("repository.noVisibleSnapshotInThisSuite")}
      hint={t("repository.checkTheSuiteNameOrCreateTheFirst")}
    />
  );
}
