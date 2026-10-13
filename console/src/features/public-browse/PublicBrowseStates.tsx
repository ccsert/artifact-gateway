import { SafetyCertificateOutlined, SearchOutlined } from "@ant-design/icons";
import { Button } from "antd";
import { Link } from "react-router-dom";
import { EmptyState } from "../../components/ui/Feedback";
import { usePreferences } from "../../lib/preferences";
import { PublicBrowseStateSurface } from "./PublicBrowseChrome";

/** Anonymous reads are switched off for the whole Gateway. */
export function AnonymousReadsDisabled() {
  const { t } = usePreferences();
  return (
    <PublicBrowseStateSurface>
      <EmptyState
        icon={<SafetyCertificateOutlined />}
        title={t("publicBrowse.globalAnonymousReadsAreDisabled")}
        hint={t("publicBrowse.anonymousDisabledHint")}
        action={
          <Link
            to="/access"
            className="text-sm text-[var(--ag-link)] hover:text-[var(--ag-link-hover)]"
          >
            {t("publicBrowse.openAccessControl")}
          </Link>
        }
      />
    </PublicBrowseStateSurface>
  );
}

/** The linked repository does not exist or is not public. */
export function PublicRepositoryMissing({ onBack }: { onBack: () => void }) {
  const { t } = usePreferences();
  return (
    <PublicBrowseStateSurface>
      <EmptyState
        icon={<SearchOutlined />}
        title={t("publicBrowse.publicRepositoryNotFoundOrVisible")}
        hint={t("publicBrowse.repositoryMissingHint")}
        action={
          <Button type="primary" onClick={onBack}>
            {t("publicBrowse.backToCatalog")}
          </Button>
        }
      />
    </PublicBrowseStateSurface>
  );
}
