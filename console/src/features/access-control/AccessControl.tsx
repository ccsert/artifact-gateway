import { useMemo } from "react";
import { Tabs } from "antd";
import { useSearchParams } from "react-router-dom";
import { PageHeader } from "../../components/ui/Layout";
import { MetricStrip } from "../../components/ui/ConsolePrimitives";
import { SectionBoundary } from "../../components/ui/SectionBoundary";
import { useAuth } from "../../lib/auth";
import { platformCapabilities } from "../../lib/authorization";
import {
  isActiveApiKeyPrincipal,
  isActiveUserPrincipal,
} from "../../lib/accessPrincipals";
import { usePreferences } from "../../lib/preferences";
import { AccessEvaluator } from "./AccessEvaluator";
import { accessControlTabFromQuery, scopeLabel } from "./accessModel";
import {
  useAccessDirectory,
  useAnonymousPolicy,
  useGrantRows,
} from "./accessQueries";
import {
  AuthorizationTemplatesPanel,
  type PrincipalOption,
} from "./AuthorizationTemplatesPanel";
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
  const { locale, text } = usePreferences();
  const [searchParams, setSearchParams] = useSearchParams();
  const activeTab = accessControlTabFromQuery(searchParams.get("tab"));
  const english = locale === "en-US";
  const canManageAnonymousPolicy = platformCapabilities(identity).platformAdmin;
  const grantsQuery = useGrantRows();
  const rows = grantsQuery.data ?? null;
  const anonymousPolicyQuery = useAnonymousPolicy();
  const directory = useAccessDirectory();
  const { users, apiKeys, serviceAccounts } = directory;
  const publicRepositoryCount = directory.repositories.filter(
    (repository) => repository.anonymousRead,
  ).length;

  const principalOptions = useMemo<PrincipalOption[]>(() => {
    const options: PrincipalOption[] = [];
    const seen = new Set<string>();
    const add = (value: string, label: string) => {
      const normalized = value.trim();
      if (!normalized || seen.has(normalized)) return;
      seen.add(normalized);
      options.push({ value: normalized, label });
    };

    if (identity?.actor) {
      add(
        identity.actor,
        `${text("当前身份", "Current identity")} · ${identity.actor}`,
      );
    }
    users
      .filter(isActiveUserPrincipal)
      .forEach((user) =>
        add(
          `user:${user.name}`,
          `${text("用户", "User")} · ${user.name}${
            user.displayName ? ` · ${user.displayName}` : ""
          }`,
        ),
      );
    apiKeys
      .filter((key) => isActiveApiKeyPrincipal(key))
      .forEach((key) => add(`api-key:${key.id}`, `API Key · ${key.name}`));
    serviceAccounts
      .filter((account) => account.state === "active")
      .forEach((account) =>
        add(
          `service-account:${account.id}`,
          `${text("服务账号", "Service account")} · ${account.name}`,
        ),
      );
    return options;
  }, [apiKeys, identity, serviceAccounts, text, users]);

  const grants = rows ?? [];
  const principalCount = new Set(grants.map((grant) => grant.principal)).size;
  const adminCount = grants.filter(
    (grant) => scopeLabel(grant.scopes, english).key === "admin",
  ).length;
  const writeCount = grants.filter(
    (grant) => scopeLabel(grant.scopes, english).key === "write",
  ).length;

  return (
    <div className="ag-page-stack">
      <PageHeader
        title={text("访问控制", "Access control")}
        description={text(
          "检查主体的最终权限，集中查看匿名策略与逐仓库授权",
          "Evaluate effective permissions and review anonymous and repository access rules",
        )}
      />
      <MetricStrip
        items={[
          {
            label: text("授权主体", "Principals"),
            value: principalCount,
            hint: text(
              "用户、API Key 或其他主体",
              "Users, API keys, or other principals",
            ),
          },
          {
            label: text("写入授权", "Write grants"),
            value: writeCount,
            hint: text("可发布或编辑制品", "Can publish or edit artifacts"),
            tone: writeCount ? "warning" : "default",
          },
          {
            label: text("管理员授权", "Admin grants"),
            value: adminCount,
            hint: text("具备授权与删除能力", "Can manage grants and delete"),
            tone: adminCount ? "danger" : "success",
          },
        ]}
      />
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
            label: text(
              `授权记录（${grants.length}）`,
              `Grants (${grants.length})`,
            ),
            children: (
              <SectionBoundary resetKeys={[activeTab]}>
                <GrantsPanel rows={rows} error={grantsQuery.error} />
              </SectionBoundary>
            ),
          },
          {
            key: "evaluate",
            label: text("权限检查", "Access evaluation"),
            children: (
              <SectionBoundary resetKeys={[activeTab]}>
                <AccessEvaluator
                  users={users}
                  apiKeys={apiKeys}
                  serviceAccounts={serviceAccounts}
                  repositories={directory.repositories}
                  evaluatorOptionsLoading={directory.loading}
                  evaluatorOptionsError={directory.error}
                  serviceAccountsError={directory.serviceAccountsError}
                  onRetryDirectory={directory.retry}
                />
              </SectionBoundary>
            ),
          },
          {
            key: "policies",
            label: text("策略与模板", "Policies & templates"),
            children: (
              <div className="ag-page-stack">
                <SectionBoundary resetKeys={[activeTab]}>
                  <PublicAccessCard
                    anonymousPolicy={anonymousPolicyQuery.data ?? null}
                    loadError={anonymousPolicyQuery.error}
                    canManageAnonymousPolicy={canManageAnonymousPolicy}
                    publicRepositoryCount={publicRepositoryCount}
                    repositoryCount={directory.repositories.length}
                  />
                </SectionBoundary>
                <AuthorizationRolesPanel />
                <AuthorizationTemplatesPanel
                  repositories={directory.repositories}
                  principalOptions={principalOptions}
                />
              </div>
            ),
          },
        ]}
      />
    </div>
  );
}
