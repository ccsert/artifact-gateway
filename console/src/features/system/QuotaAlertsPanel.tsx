import { useCallback, useRef, useState } from "react";
import { ArrowLeftOutlined, ReloadOutlined } from "@ant-design/icons";
import { Button } from "antd";
import {
  getEmailNotificationCapability,
  listEmailTargets,
  listRepositories,
  listRepositoryQuotaAlertRules,
  type RepositoryQuotaAlertRule,
} from "../../client";
import { QuotaBadge as Badge } from "./QuotaBadge";
import {
  EmptyState,
  ErrorBanner,
  Loading,
  Notice,
} from "../../components/ui/Feedback";
import { Card } from "../../components/ui/Layout";
import { useAuth } from "../../lib/auth";
import { platformCapabilities } from "../../lib/authorization";
import { usePreferences } from "../../lib/preferences";
import { percent, quotaLabel, safeQuotaError } from "./quotaAlertPresentation";
import { quotaResult, useQuotaSnapshot } from "./useQuotaSnapshot";
import { QuotaRuleEditor } from "./QuotaRuleEditor";
import { QuotaRuleDetail } from "./QuotaRuleDetail";

export function QuotaAlertsPanel() {
  const { identity } = useAuth();
  const { t } = usePreferences();
  if (!platformCapabilities(identity).platformAdmin)
    return (
      <EmptyState
        title={t("system.onlyPlatformAdministratorsCanManageQuotaAlerts")}
      />
    );
  return <AuthorizedQuotaAlerts />;
}

function AuthorizedQuotaAlerts() {
  const { t } = usePreferences();
  const [selected, setSelected] = useState<string>();
  const [editor, setEditor] = useState<{ rule?: RepositoryQuotaAlertRule }>();
  const [saved, setSaved] = useState<false | "saved" | "deleted">(false);
  const [authBlocked, setAuthBlocked] = useState<unknown>();
  const authGeneration = useRef(0);
  const blockAuthorization = useCallback((error: unknown) => {
    authGeneration.current++;
    setAuthBlocked(error);
  }, []);
  const snapshot = useQuotaSnapshot(
    async (signal) => {
      const [rules, targets, capability, repositories] = await Promise.all([
        listRepositoryQuotaAlertRules({ signal }),
        listEmailTargets({ signal }),
        getEmailNotificationCapability({ signal }),
        listRepositories({ signal, query: { pageSize: 200 } }),
      ]);
      return {
        rules: quotaResult(rules),
        targets: quotaResult(targets),
        capability: quotaResult(capability),
        repositories: quotaResult(repositories),
      };
    },
    "rules",
    blockAuthorization,
  );
  const refreshExplicitly = async () => {
    const generation = authGeneration.current;
    if ((await snapshot.refresh()) && generation === authGeneration.current)
      setAuthBlocked(undefined);
  };
  if (!snapshot.data)
    return snapshot.error ? (
      <ErrorBanner
        error={safeQuotaError(snapshot.error, t)}
        onRetry={() => void refreshExplicitly()}
      />
    ) : (
      <Loading />
    );
  const { rules, targets, capability, repositories } = snapshot.data;
  const selectedRule = rules.find((rule) => rule.id === selected);
  const changed = (rule: RepositoryQuotaAlertRule) => {
    snapshot.update((old) => ({
      ...old,
      rules: [...old.rules.filter((item) => item.id !== rule.id), rule],
    }));
    setEditor(undefined);
    setSelected(rule.id);
    setSaved("saved");
    void snapshot.refresh();
  };
  const writable = !snapshot.error && !snapshot.loading && !authBlocked;
  return (
    <div className="ag-page-stack ag-quota-alerts">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <h2 className="text-base font-semibold text-fg-strong">
            {t("system.repositoryQuotaAlerts")}
          </h2>
          <p className="mt-1 text-sm text-fg-tertiary">
            {t("system.observesLogicalRepositoryUsageNotThePhysicalCapacity")}
          </p>
        </div>
        <div className="flex flex-wrap gap-2">
          <Button
            icon={<ReloadOutlined aria-hidden="true" />}
            loading={snapshot.loading}
            onClick={() => void refreshExplicitly()}
          >
            {t("system.refresh")}
          </Button>
          {!editor && !selected && (
            <Button
              type="primary"
              disabled={!writable || targets.length === 0}
              onClick={() => {
                setSaved(false);
                setEditor({});
              }}
            >
              {t("system.createRule")}
            </Button>
          )}
        </div>
      </div>
      {Boolean(snapshot.error) && (
        <ErrorBanner
          tone="warning"
          title={t("system.showingPreviouslyReadData")}
          error={safeQuotaError(snapshot.error, t)}
          onRetry={() => void refreshExplicitly()}
        />
      )}
      {Boolean(authBlocked) && (
        <ErrorBanner
          error={safeQuotaError(authBlocked, t)}
          onRetry={() => void refreshExplicitly()}
        />
      )}
      {snapshot.readAt && (
        <p className="text-xs text-fg-tertiary">
          {t("system.readAt")}: {new Date(snapshot.readAt).toLocaleString()}
        </p>
      )}
      {saved && (
        <Notice
          tone="success"
          title={
            saved === "deleted"
              ? t("system.ruleDeletedHistoryRemainsAvailable")
              : t("system.ruleSaved")
          }
          onClose={() => setSaved(false)}
        />
      )}
      {!capability.enabled && (
        <Notice
          tone="info"
          title={quotaLabel(capability.reason, t)}
          description={t("system.disabledRulesCanBeSavedAnAdministratorMust")}
        />
      )}
      {targets.length === 0 && (
        <Notice
          tone="info"
          title={t("system.noEmailTargets")}
          description={t("system.createATargetInTheSystemRuntimeEmail")}
        />
      )}
      {editor ? (
        <>
          <QuotaRuleEditor
            nextPageToken={repositories.nextPageToken}
            onAuthBlocked={blockAuthorization}
            rule={editor.rule}
            repositories={repositories.items}
            targets={targets}
            canSave={writable && (!editor.rule?.enabled || capability.enabled)}
            onCancel={() => setEditor(undefined)}
            onSaved={changed}
          />
        </>
      ) : selectedRule ? (
        <>
          <div>
            <Button
              icon={<ArrowLeftOutlined aria-hidden="true" />}
              onClick={() => {
                setSelected(undefined);
                setSaved(false);
              }}
            >
              {t("system.backToRules")}
            </Button>
          </div>
          <QuotaRuleDetail
            key={selectedRule.id}
            onAuthBlocked={blockAuthorization}
            rule={selectedRule}
            repositoryName={
              repositories.items.find(
                (repo) => repo.id === selectedRule.repositoryId,
              )?.name ?? selectedRule.repositoryId
            }
            targets={targets}
            capability={capability}
            writable={writable}
            onEdit={() => {
              setSaved(false);
              setEditor({ rule: selectedRule });
            }}
            onChanged={changed}
            onDeleted={() => {
              setSaved("deleted");
              void snapshot.refresh();
            }}
          />
        </>
      ) : (
        <Card>
          {rules.length === 0 ? (
            <EmptyState
              title={t("system.noQuotaAlertRules")}
              hint={t("system.newRulesAreDisabledByDefaultCreatingOne")}
            />
          ) : (
            <ul className="divide-y divide-line">
              {rules.map((rule) => (
                <li
                  key={rule.id}
                  className="flex flex-wrap items-start justify-between gap-4 px-5 py-4"
                >
                  <div className="min-w-0">
                    <h3 className="break-words text-sm font-semibold text-fg-strong">
                      {repositories.items.find(
                        (r) => r.id === rule.repositoryId,
                      )?.name ?? rule.repositoryId}
                    </h3>
                    <p className="mt-2 text-xs text-fg-tertiary">
                      {t("system.warning")}{" "}
                      {percent(rule.policy.warningBasisPoints)} ·{" "}
                      {t("system.critical")}{" "}
                      {percent(rule.policy.criticalBasisPoints)} ·{" "}
                      {t("system.recoveryBelow")}{" "}
                      {percent(rule.policy.recoveryBelowBasisPoints)}
                    </p>
                  </div>
                  <div className="flex flex-wrap items-center gap-2">
                    <Badge>
                      {rule.enabled && !rule.deleted
                        ? t("system.enabled")
                        : quotaLabel(rule.deleted ? "deleted" : "disabled", t)}
                    </Badge>
                    <Badge
                      tone={
                        rule.state.severity === "critical"
                          ? "danger"
                          : rule.state.severity === "warning"
                            ? "warning"
                            : "neutral"
                      }
                    >
                      {quotaLabel(rule.state.severity, t)}
                    </Badge>
                    <Badge>{quotaLabel(rule.state.dataState, t)}</Badge>
                    <Button
                      size="small"
                      aria-label={t("system.view", {
                        name:
                          repositories.items.find(
                            (r) => r.id === rule.repositoryId,
                          )?.name ?? rule.repositoryId,
                      })}
                      onClick={() => {
                        setSelected(rule.id);
                        setSaved(false);
                      }}
                    >
                      {t("system.view2")}
                    </Button>
                  </div>
                </li>
              ))}
            </ul>
          )}
        </Card>
      )}
    </div>
  );
}
