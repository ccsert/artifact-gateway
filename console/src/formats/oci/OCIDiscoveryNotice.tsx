import { usePreferences } from "../../lib/preferences";
import { useFormatProfiles } from "../../features/repository/repositoryQueries";

/** API support is separate from the coverage of an individual upstream. */
export function OCIDiscoveryNotice() {
  const { t } = usePreferences();
  const profiles = useFormatProfiles(true);
  const discovery = profiles.data?.find(
    (profile) => profile.format === "oci",
  )?.ociDiscovery;
  if (!discovery?.catalog || !discovery.referrers) return null;

  return (
    <section
      aria-label={t("formats.ociDiscovery")}
      className="grid min-w-0 gap-1 text-xs leading-5 text-fg-secondary"
    >
      <h3 className="text-sm font-medium text-fg-strong">
        {t("formats.ociDiscovery")}
      </h3>
      <p>{t("formats.ociCatalogCoverage")}</p>
      <p>{t("formats.ociReferrerAuthority")}</p>
      {!discovery.signatureVerification && (
        <p>{t("formats.ociDiscoveryTrust")}</p>
      )}
      {!discovery.tagFallback && <p>{t("formats.ociReferrersApiRequired")}</p>}
    </section>
  );
}
