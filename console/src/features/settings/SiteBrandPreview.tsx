import { PictureOutlined } from "@ant-design/icons";
import { SiteBrandMark } from "../../components/ui/SiteBrand";
import { usePreferences } from "../../lib/preferences";
import type { SiteSettingsDraft } from "./siteSettingsModel";

/** The draft's brand lockup and browser tab, before it is saved. */
export function SiteBrandPreview({
  normalizedDraft,
}: {
  normalizedDraft: SiteSettingsDraft;
}) {
  const { t } = usePreferences();
  return (
    <aside
      className="ag-site-settings-preview"
      aria-label={t("settings.brandPreview")}
    >
      <div className="ag-site-preview-label">
        <PictureOutlined />
        {t("settings.livePreview")}
      </div>
      <div className="ag-site-preview-lockup">
        <SiteBrandMark
          className="ag-site-preview-mark"
          preview={normalizedDraft}
        />
        <div className="min-w-0">
          <div className="ag-site-preview-name">
            {normalizedDraft.siteName || t("settings.untitledSite")}
          </div>
          <div className="ag-site-preview-product">Console</div>
        </div>
      </div>
      <div className="ag-site-preview-browser">
        <SiteBrandMark
          className="ag-site-preview-favicon"
          preview={normalizedDraft}
        />
        <span>
          {(normalizedDraft.siteName || t("settings.untitledSite")) +
            " Console"}
        </span>
      </div>
      <p>{t("settings.thisPreviewShowsTheIdentityLockupOnlyOther")}</p>
    </aside>
  );
}
