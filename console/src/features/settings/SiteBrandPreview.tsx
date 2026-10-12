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
  const { text } = usePreferences();
  return (
    <aside
      className="ag-site-settings-preview"
      aria-label={text("品牌预览", "Brand preview")}
    >
      <div className="ag-site-preview-label">
        <PictureOutlined />
        {text("实时预览", "Live preview")}
      </div>
      <div className="ag-site-preview-lockup">
        <SiteBrandMark
          className="ag-site-preview-mark"
          preview={normalizedDraft}
        />
        <div className="min-w-0">
          <div className="ag-site-preview-name">
            {normalizedDraft.siteName || text("未命名站点", "Untitled site")}
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
          {(normalizedDraft.siteName || text("未命名站点", "Untitled site")) +
            " Console"}
        </span>
      </div>
      <p>
        {text(
          "预览仅展示身份组合；保存前不会影响其他用户。",
          "This preview shows the identity lockup only; other users are unaffected until you save.",
        )}
      </p>
    </aside>
  );
}
