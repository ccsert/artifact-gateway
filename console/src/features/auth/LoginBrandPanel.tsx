import { SafetyCertificateOutlined } from "@ant-design/icons";
import { SiteBrandMark, SiteName } from "../../components/ui/SiteBrand";
import { usePreferences } from "../../lib/preferences";

/** The sign-in page's brand lockup, product line and security note. */
export function LoginBrandPanel() {
  const { t } = usePreferences();
  return (
    <aside className="ag-login-brand-panel">
      <div className="ag-login-brand-lockup">
        <SiteBrandMark className="flex h-11 w-11 items-center justify-center rounded-lg text-base font-bold text-on-identity" />
        <div>
          <div className="text-sm font-semibold text-fg-strong">Console</div>
          <div className="mt-0.5 font-mono text-xs uppercase tracking-wider text-[var(--ag-content-secondary)]">
            Native Hosted API v2
          </div>
        </div>
      </div>

      <div className="ag-login-brand-copy">
        <h1 className="break-words text-3xl font-semibold leading-tight text-fg-strong xl:text-4xl">
          <SiteName />
        </h1>
        <p className="mt-3 max-w-xs text-sm leading-6 text-fg-secondary">
          {t("auth.controlPlane")}
        </p>
      </div>

      <div className="ag-login-security-note">
        <SafetyCertificateOutlined />
        <div>
          <div className="text-xs font-medium text-fg">
            {t("auth.protectedAccess")}
          </div>
          <div className="mt-1 font-mono text-xs text-fg-tertiary">
            OIDC · Local session · Bearer token
          </div>
        </div>
      </div>
    </aside>
  );
}
