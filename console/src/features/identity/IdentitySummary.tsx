import { Descriptions } from "antd";
import type { CurrentIdentity } from "../../client";
import { Badge } from "../../components/ui/Badge";
import { usePreferences } from "../../lib/preferences";

export function IdentitySummary({ identity }: { identity: CurrentIdentity }) {
  const { t } = usePreferences();
  const role = identity.role;
  const kindLabels: Record<CurrentIdentity["kind"], string> = {
    static_admin: t("identity.staticAdministratorToken"),
    static_resolver: t("identity.staticResolverCredential"),
    local_session: t("identity.localUserSession"),
    api_key: "API Key",
    service_account_credential: t("identity.serviceAccountCredential"),
    oidc: "OIDC",
  };
  const roleLabels = {
    none: t("identity.awaitingApproval"),
    member: t("identity.member"),
    admin: t("identity.administrator"),
  };
  return (
    <div className="min-w-0">
      <Descriptions
        className="ag-identity-summary"
        size="small"
        colon={false}
        column={4}
        items={[
          {
            key: "actor",
            label: t("identity.principal"),
            children: (
              <span className="font-mono text-xs text-fg">
                {identity.actor}
              </span>
            ),
          },
          {
            key: "kind",
            label: t("identity.authentication"),
            children: (
              <Badge tone="visualization-1">{kindLabels[identity.kind]}</Badge>
            ),
          },
          {
            key: "role",
            label: t("identity.globalRole"),
            children: role ? (
              <Badge
                tone={role === "admin" ? "visualization-3" : "visualization-1"}
              >
                {roleLabels[role]}
              </Badge>
            ) : (
              <span className="text-xs text-fg-tertiary">
                {t("identity.noneRepositoryRulesDecide")}
              </span>
            ),
          },
          {
            key: "administrator",
            label: t("identity.administrator2"),
            children: identity.administrator ? (
              <Badge tone="danger">{t("identity.yes")}</Badge>
            ) : (
              <Badge tone="neutral">{t("identity.no")}</Badge>
            ),
          },
        ]}
      />
      {identity.oidc && (
        <div className="mt-3 flex min-w-0 items-start gap-4 border-t border-line pt-3 text-xs">
          <span className="shrink-0 text-fg-tertiary">
            {t("identity.oIDCMapping")}
          </span>
          <div className="flex min-w-0 flex-wrap gap-2">
            {identity.oidc.adminSubject && (
              <Badge tone="danger">
                {t("identity.administratorSubjectListAdmin")}
              </Badge>
            )}
            {identity.oidc.roleMappings.map((mapping) => (
              <Badge
                key={`${mapping.externalRole}-${mapping.gatewayRole}`}
                tone={
                  mapping.gatewayRole === "admin"
                    ? "visualization-3"
                    : "visualization-1"
                }
              >
                {mapping.externalRole} → {mapping.gatewayRole}
              </Badge>
            ))}
            {!identity.oidc.adminSubject &&
              identity.oidc.roleMappings.length === 0 && (
                <span className="text-fg-tertiary">
                  {t("identity.noConfiguredRoleMappingMatched")}
                </span>
              )}
          </div>
        </div>
      )}
    </div>
  );
}
