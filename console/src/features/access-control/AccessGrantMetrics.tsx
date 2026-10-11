import { MetricStrip } from "../../components/ui/ConsolePrimitives";
import { usePreferences } from "../../lib/preferences";
import { scopeLabel, type GrantRow } from "./accessModel";

/** Derived grant data belongs to the same recoverable section as its source. */
export function AccessGrantMetrics({ rows }: { rows: GrantRow[] }) {
  const { t } = usePreferences();
  const principalCount = new Set(rows.map((grant) => grant.principal)).size;
  const adminCount = rows.filter(
    (grant) => scopeLabel(grant.scopes, t).key === "admin",
  ).length;
  const writeCount = rows.filter(
    (grant) => scopeLabel(grant.scopes, t).key === "write",
  ).length;
  return (
    <MetricStrip
      items={[
        {
          label: t("access.principals"),
          value: principalCount,
          hint: t("access.usersApiKeysOrOtherPrincipals"),
        },
        {
          label: t("access.writeGrants"),
          value: writeCount,
          hint: t("access.canPublishOrEditArtifacts"),
          tone: writeCount ? "warning" : "default",
        },
        {
          label: t("access.adminGrants"),
          value: adminCount,
          hint: t("access.canManageGrantsAndDelete"),
          tone: adminCount ? "danger" : "success",
        },
      ]}
    />
  );
}
