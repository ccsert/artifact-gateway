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
        <div className="text-sm font-semibold text-fg-strong">Console</div>
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
          <div className="mt-1 text-xs text-fg-tertiary">
            {t("auth.ssoMode")} · {t("auth.passwordMode")} ·{" "}
            {t("auth.tokenMode")}
          </div>
        </div>
      </div>
    </aside>
  );
}
