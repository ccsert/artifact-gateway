import type { ReactNode } from "react";
import { Link } from "react-router-dom";
import { Card } from "../../components/ui/Layout";
import { PreferenceControls } from "../../components/ui/PreferenceControls";
import { SiteBrandMark, SiteName } from "../../components/ui/SiteBrand";
import { usePreferences } from "../../lib/preferences";

/** Site identity, preferences and the way into the management Console. */
export function PublicBrowseHeader({
  authenticated,
}: {
  authenticated: boolean;
}) {
  const { t } = usePreferences();
  return (
    <div className="ag-public-browse-header ag-page-header flex flex-wrap items-center justify-between gap-4">
      <div className="flex min-w-0 flex-1 items-center gap-3">
        <SiteBrandMark className="flex size-10 shrink-0 items-center justify-center rounded-xl text-sm font-bold text-on-identity" />
        <div className="min-w-0">
          <div className="break-words font-semibold tracking-tight text-fg-strong">
            <SiteName />
          </div>
          <div className="mt-0.5 text-xs text-fg-tertiary">
            {t("public.browseTitle")}
          </div>
        </div>
      </div>
      <div className="flex shrink-0 items-center gap-3">
        <PreferenceControls compact />
        <Link
          to={authenticated ? "/" : "/login"}
          className="text-sm text-[var(--ag-content-secondary)] hover:text-[var(--ag-link-hover)]"
        >
          {t(
            authenticated
              ? "public.managementConsole"
              : "public.managementLogin",
          )}
        </Link>
      </div>
    </div>
  );
}

/** Frames a whole-section state: loading, error or empty. */
export function PublicBrowseStateSurface({
  children,
  className = "",
}: {
  children: ReactNode;
  className?: string;
}) {
  return (
    <Card
      className={`ag-public-state-surface overflow-hidden ${className}`}
      bodyClassName="p-5 sm:p-6"
    >
      {children}
    </Card>
  );
}
