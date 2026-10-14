import type { Translate } from "../../lib/preferences";
import { CheckCircleFilled, CloseCircleFilled } from "@ant-design/icons";
import type {
  EffectiveAccessDecision,
  RepositoryEffectiveAccess,
} from "../../client";
import { usePreferences } from "../../lib/preferences";

export function accessSourceLabel(source: string, t: Translate): string {
  switch (source) {
    case "administrator":
      return t("access.administratorIdentity");
    case "role":
      return t("access.globalRole");
    case "legacy_static":
      return t("access.legacyStaticPolicy");
    case "anonymous_policy":
      return t("access.anonymousAccessPolicy");
    case "repository_grants":
      return t("access.repositoryGrant");
    default:
      return source || t("access.notSpecified");
  }
}

export function accessReasonLabel(reason: string, t: Translate): string {
  const labels: Record<string, string> = {
    administrator: t("access.allowedByAdministratorIdentity"),
    role_admin: t("access.allowedByTheGlobalAdminRole"),
    role_member: t("access.aMemberLevelCarriesNoImplicitCapabilityRepository"),
    scope_granted: t("access.principalPermissionAndResourceScopeAllMatch"),
    scope_not_granted: t(
      "access.noRepositoryRuleMatchesThePrincipalPermissionAnd",
    ),
    grant_lookup_failed: t("access.repositoryGrantsCouldNotBeLoadedAccessIs"),
    read_pattern_granted: t("access.matchedALegacyReadRule"),
    write_pattern_granted: t("access.matchedALegacyWriteRule"),
    global_anonymous_access_disabled: t(
      "access.globalAnonymousReadsAreDisabled",
    ),
    repository_anonymous_read_disabled: t(
      "access.anonymousReadsAreDisabledForThisRepository",
    ),
    repository_anonymous_read_enabled: t(
      "access.anonymousReadsAreEnabledGloballyAndForThis",
    ),
    repository_not_active: t("access.theRepositoryIsNotCurrentlyActive"),
  };
  return labels[reason] ?? reason.replaceAll("_", " ");
}

function Decision({
  label,
  decision,
}: {
  label: string;
  decision: EffectiveAccessDecision;
}) {
  const { t } = usePreferences();
  return (
    <div className="min-w-0 px-4 py-3">
      <div className="text-xs font-medium text-fg-tertiary">{label}</div>
      <div
        className={`mt-1.5 flex items-center gap-1.5 text-sm font-semibold ${
          decision.allowed
            ? "text-[var(--ag-status-success)]"
            : "text-fg-secondary"
        }`}
      >
        {decision.allowed ? <CheckCircleFilled /> : <CloseCircleFilled />}
        {decision.allowed ? t("access.allowed") : t("access.denied")}
      </div>
      <div className="mt-1 text-xs font-medium text-fg-secondary">
        {accessSourceLabel(decision.source, t)}
      </div>
      <p className="mt-0.5 text-xs leading-4 text-fg-disabled">
        {accessReasonLabel(decision.reason, t)}
      </p>
    </div>
  );
}

export function AccessDecisionSummary({
  access,
}: {
  access: RepositoryEffectiveAccess;
}) {
  const { t } = usePreferences();
  const decisions = [
    {
      label: t("access.anonymousReads"),
      decision: access.anonymousRead,
    },
    { label: t("access.scopeRead"), decision: access.permissions.read },
    { label: t("access.scopeWrite"), decision: access.permissions.write },
    { label: t("access.admin"), decision: access.permissions.admin },
    {
      label: t("access.scopeIntelligence"),
      decision: access.permissions.intelligence,
    },
  ];
  return (
    <div className="grid grid-cols-5 divide-x divide-line overflow-hidden rounded-md border border-line bg-surface-translucent">
      {decisions.map((item) => (
        <Decision key={item.label} {...item} />
      ))}
    </div>
  );
}
