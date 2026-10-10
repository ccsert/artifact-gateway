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
  const { text } = usePreferences();
  if (!platformCapabilities(identity).platformAdmin)
    return (
      <EmptyState
        title={text(
          "仅平台管理员可管理配额告警",
          "Only platform administrators can manage quota alerts",
        )}
      />
    );
  return <AuthorizedQuotaAlerts />;
}

function AuthorizedQuotaAlerts() {
  const { text } = usePreferences();
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
        error={safeQuotaError(snapshot.error, text)}
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
            {text("仓库配额告警", "Repository quota alerts")}
          </h2>
          <p className="mt-1 text-sm text-fg-tertiary">
            {text(
              "观察仓库逻辑用量，不表示 S3、NAS 或本地挂载物理容量。",
              "Observes logical repository usage, not the physical capacity of S3, NAS, or local mounts.",
            )}
          </p>
        </div>
        <div className="flex flex-wrap gap-2">
          <Button
            icon={<ReloadOutlined aria-hidden="true" />}
            loading={snapshot.loading}
            onClick={() => void refreshExplicitly()}
          >
            {text("刷新", "Refresh")}
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
              {text("创建规则", "Create rule")}
            </Button>
          )}
        </div>
      </div>
      {Boolean(snapshot.error) && (
        <ErrorBanner
          tone="warning"
          title={text("当前显示上次读取的数据", "Showing previously read data")}
          error={safeQuotaError(snapshot.error, text)}
          onRetry={() => void refreshExplicitly()}
        />
      )}
      {Boolean(authBlocked) && (
        <ErrorBanner
          error={safeQuotaError(authBlocked, text)}
          onRetry={() => void refreshExplicitly()}
        />
      )}
      {snapshot.readAt && (
        <p className="text-xs text-fg-tertiary">
          {text("读取时间", "Read at")}:{" "}
          {new Date(snapshot.readAt).toLocaleString()}
        </p>
      )}
      {saved && (
        <Notice
          tone="success"
          title={
            saved === "deleted"
              ? text(
                  "规则已删除，历史仍可查看",
                  "Rule deleted; history remains available",
                )
              : text("规则已保存", "Rule saved")
          }
          onClose={() => setSaved(false)}
        />
      )}
      {!capability.enabled && (
        <Notice
          tone="info"
          title={quotaLabel(capability.reason, text)}
          description={text(
            "可以保存停用规则；管理员完成邮件通道及加密配置后才能启用。",
            "Disabled rules can be saved. An administrator must configure the email channel and encryption before enabling them.",
          )}
        />
      )}
      {targets.length === 0 && (
        <Notice
          tone="info"
          title={text("尚无邮件目标", "No email targets")}
          description={text(
            "在系统运行的“邮件通知”页签新建目标，再返回此页配置规则。",
            "Create a target in the System runtime Email notifications tab, then return here to configure a rule.",
          )}
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
              {text("返回规则列表", "Back to rules")}
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
              title={text("尚无配额告警规则", "No quota alert rules")}
              hint={text(
                "新规则默认停用，创建不会发送测试邮件。",
                "New rules are disabled by default. Creating one sends no test email.",
              )}
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
                      {text("警告", "Warning")}{" "}
                      {percent(rule.policy.warningBasisPoints)} ·{" "}
                      {text("严重", "Critical")}{" "}
                      {percent(rule.policy.criticalBasisPoints)} ·{" "}
                      {text("恢复低于", "Recovery below")}{" "}
                      {percent(rule.policy.recoveryBelowBasisPoints)}
                    </p>
                  </div>
                  <div className="flex flex-wrap items-center gap-2">
                    <Badge>
                      {rule.enabled && !rule.deleted
                        ? text("已启用", "Enabled")
                        : quotaLabel(
                            rule.deleted ? "deleted" : "disabled",
                            text,
                          )}
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
                      {quotaLabel(rule.state.severity, text)}
                    </Badge>
                    <Badge>{quotaLabel(rule.state.dataState, text)}</Badge>
                    <Button
                      size="small"
                      aria-label={text(
                        `查看 ${repositories.items.find((r) => r.id === rule.repositoryId)?.name ?? rule.repositoryId}`,
                        `View ${repositories.items.find((r) => r.id === rule.repositoryId)?.name ?? rule.repositoryId}`,
                      )}
                      onClick={() => {
                        setSelected(rule.id);
                        setSaved(false);
                      }}
                    >
                      {text("查看", "View")}
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
