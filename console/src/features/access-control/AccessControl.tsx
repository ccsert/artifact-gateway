import { Tabs } from "antd";
import { useSearchParams } from "react-router-dom";
import { PageHeader } from "../../components/ui/Layout";
import { SectionBoundary } from "../../components/ui/SectionBoundary";
import { useAuth } from "../../lib/auth";
import { platformCapabilities } from "../../lib/authorization";
import { usePreferences } from "../../lib/preferences";
import { AccessEvaluator } from "./AccessEvaluator";
import { accessControlTabFromQuery } from "./accessModel";
import {
  useAccessDirectory,
  useAnonymousPolicy,
  useGrantRows,
  refetchAccessSources,
} from "./accessQueries";
import { AccessTemplates } from "./AccessTemplates";
import { AccessGrantMetrics } from "./AccessGrantMetrics";
import { AuthorizationRolesPanel } from "./AuthorizationRolesPanel";
import { GrantsPanel } from "./GrantsPanel";
import { PublicAccessCard } from "./PublicAccessCard";

export {
  accessControlTabFromQuery,
  type AccessControlTab,
} from "./accessModel";

/**
 * Access control: every repository grant, the access evaluator, and the
 * policies (anonymous gate, roles, templates). Each tab renders its own
 * section; this page owns the tab state and the shared directory data.
 */
export function AccessControlPage() {
  const { identity } = useAuth();
  const { t } = usePreferences();
  const [searchParams, setSearchParams] = useSearchParams();
  const activeTab = accessControlTabFromQuery(searchParams.get("tab"));
  const canManageAnonymousPolicy = platformCapabilities(identity).platformAdmin;
  const grantsQuery = useGrantRows();
  const rows = grantsQuery.data ?? null;
  const anonymousPolicyQuery = useAnonymousPolicy();
  const directory = useAccessDirectory();
  const { users, apiKeys, serviceAccounts } = directory;
  const retryGrants = () => grantsQuery.refetch({ throwOnError: true });
  const retryPolicies = () =>
    refetchAccessSources([
      anonymousPolicyQuery.refetch({ throwOnError: true }),
      directory.retry(),
    ]);
  const grants = rows ?? [];

  return (
    <div className="ag-page-stack">
      <PageHeader
        title={t("access.accessControl")}
        description={t(
          "access.evaluateEffectivePermissionsAndReviewAnonymousAndRepositoryAccess",
        )}
      />
      <SectionBoundary recoveryKeys={[rows]} onReset={retryGrants}>
        <AccessGrantMetrics rows={grants} />
      </SectionBoundary>
      <Tabs
        className="ag-access-tabs"
        size="small"
        tabBarGutter={24}
        activeKey={activeTab}
        onChange={(key) => {
          const nextTab = accessControlTabFromQuery(key);
          setSearchParams(
            (current) => {
              const next = new URLSearchParams(current);
              if (nextTab === "grants") next.delete("tab");
              else next.set("tab", nextTab);
              return next;
            },
            { replace: true },
          );
        }}
        items={[
          {
            key: "grants",
            label: t("access.grants", { grants: String(grants.length) }),
            children: (
              <SectionBoundary
                resetKeys={[activeTab]}
                recoveryKeys={[rows]}
                onReset={retryGrants}
              >
                <GrantsPanel
                  rows={rows}
                  error={grantsQuery.error}
                  onRetry={() => {
                    void retryGrants().catch(() => undefined);
                  }}
                />
              </SectionBoundary>
            ),
          },
          {
            key: "evaluate",
            label: t("access.accessEvaluation"),
            children: (
              <SectionBoundary
                resetKeys={[activeTab]}
                recoveryKeys={directory.recoveryKeys}
                onReset={directory.retry}
              >
                <AccessEvaluator
                  users={users}
                  apiKeys={apiKeys}
                  serviceAccounts={serviceAccounts}
                  repositories={directory.repositories}
                  evaluatorOptionsLoading={directory.loading}
                  evaluatorOptionsError={directory.error}
                  serviceAccountsError={directory.serviceAccountsError}
                  onRetryDirectory={() => {
                    void directory.retry().catch(() => undefined);
                  }}
                />
              </SectionBoundary>
            ),
          },
          {
            key: "policies",
            label: t("access.policiesTemplates"),
            children: (
              <div className="ag-page-stack">
                <SectionBoundary
                  resetKeys={[activeTab]}
                  recoveryKeys={[
                    anonymousPolicyQuery.data,
                    ...directory.recoveryKeys,
                  ]}
                  onReset={retryPolicies}
                >
                  <PublicAccessCard
                    anonymousPolicy={anonymousPolicyQuery.data ?? null}
                    loadError={anonymousPolicyQuery.error}
                    canManageAnonymousPolicy={canManageAnonymousPolicy}
                    repositories={directory.repositories}
                    directoryError={directory.repositoryError}
                    onRetry={() => {
                      void retryPolicies().catch(() => undefined);
                    }}
                  />
                </SectionBoundary>
                <AuthorizationRolesPanel />
                <SectionBoundary
                  resetKeys={[activeTab]}
                  recoveryKeys={directory.recoveryKeys}
                  onReset={directory.retry}
                >
                  <AccessTemplates
                    users={users}
                    apiKeys={apiKeys}
                    serviceAccounts={serviceAccounts}
                    repositories={directory.repositories}
                    directoryError={directory.error}
                    onRetry={() => {
                      void directory.retry().catch(() => undefined);
                    }}
                  />
                </SectionBoundary>
              </div>
            ),
          },
        ]}
      />
    </div>
  );
}
