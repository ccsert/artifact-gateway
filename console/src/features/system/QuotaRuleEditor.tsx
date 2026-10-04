import { useState } from "react";
import { Button, Input, Select } from "antd";
import {
  createRepositoryQuotaAlertRule,
  updateRepositoryQuotaAlertRule,
  listRepositories,
  type EmailTarget,
  type Repository,
  type RepositoryQuotaAlertPolicy,
  type RepositoryQuotaAlertRule,
} from "../../client";
import { ErrorBanner } from "../../components/ui/Feedback";
import { Card, CardHeader, Field } from "../../components/ui/Layout";
import { usePreferences } from "../../lib/preferences";
import {
  parsePolicy,
  policyDraft,
  safeQuotaError,
  isQuotaAuthError,
} from "./quotaAlertPresentation";
import { useQuotaAction } from "./useQuotaAction";
import { quotaResult } from "./useQuotaSnapshot";

export function QuotaRuleEditor({
  rule,
  repositories,
  targets,
  canSave,
  onCancel,
  onSaved,
  nextPageToken,
  onAuthBlocked,
}: {
  rule?: RepositoryQuotaAlertRule;
  repositories: Repository[];
  targets: EmailTarget[];
  canSave: boolean;
  onCancel: () => void;
  onSaved: (rule: RepositoryQuotaAlertRule) => void;
  nextPageToken?: string;
  onAuthBlocked: (error: unknown) => void;
}) {
  const { text } = usePreferences();
  const [repositoryId, setRepositoryId] = useState(rule?.repositoryId ?? "");
  const [targetId, setTargetId] = useState(rule?.targetId ?? "");
  const [draft, setDraft] = useState(() => policyDraft(rule?.policy));
  const [invalid, setInvalid] = useState(false);
  const [catalogue, setCatalogue] = useState(() => ({
    items: repositories,
    nextPageToken,
  }));
  const action = useQuotaAction(onAuthBlocked);
  const more = useQuotaAction(onAuthBlocked);
  const fields = (
    percentKey: keyof RepositoryQuotaAlertPolicy,
    holdKey: keyof RepositoryQuotaAlertPolicy,
    prefix: [string, string],
  ) => (
    <div className="grid min-w-0 grid-cols-1 items-start gap-4 sm:grid-cols-2">
      {[
        {
          key: percentKey,
          label: text(`${prefix[0]}阈值（%）`, `${prefix[1]} threshold (%)`),
          mode: "decimal" as const,
        },
        {
          key: holdKey,
          label: text(
            `${prefix[0]}持续时间（秒）`,
            `${prefix[1]} hold duration (seconds)`,
          ),
          mode: "numeric" as const,
        },
      ].map((field) => (
        <Field key={field.key} label={field.label}>
          <Input
            aria-label={field.label}
            inputMode={field.mode}
            value={draft[field.key]}
            disabled={action.busy}
            autoComplete="off"
            onChange={(event) =>
              setDraft((old) => ({ ...old, [field.key]: event.target.value }))
            }
          />
        </Field>
      ))}
    </div>
  );
  return (
    <div className="ag-page-stack">
      {Boolean(action.error) && !isQuotaAuthError(action.error) && (
        <ErrorBanner error={safeQuotaError(action.error, text)} />
      )}
      {invalid && (
        <ErrorBanner
          error={text(
            "请填写仓库和邮件目标。恢复阈值必须低于警告，警告低于严重，范围 0–100%，至多两位小数。持续时间为 1–86400 整数秒，最大样本年龄为 30–3600 整数秒。",
            "Select a repository and email target. Recovery must be below warning, and warning below critical, within 0–100% with at most two decimals. Hold durations must be integer seconds from 1–86400; sample age from 30–3600.",
          )}
        />
      )}
      <Card>
        <CardHeader
          title={
            rule
              ? text("编辑规则", "Edit rule")
              : text("创建停用规则", "Create disabled rule")
          }
        />
        <form
          className="ag-quota-form grid min-w-0 gap-6 p-5"
          onSubmit={(event) => {
            event.preventDefault();
            const policy = parsePolicy(draft);
            if (!policy || !repositoryId || !targetId) {
              setInvalid(true);
              return;
            }
            if (!canSave || action.blocked) return;
            setInvalid(false);
            const body = {
              repositoryId,
              targetId,
              enabled: rule?.enabled ?? false,
              policy,
            };
            void action.run(
              async (signal) =>
                quotaResult(
                  rule
                    ? await updateRepositoryQuotaAlertRule({
                        path: { ruleId: rule.id },
                        headers: { "If-Match": rule.version },
                        body,
                        signal,
                      })
                    : await createRepositoryQuotaAlertRule({ body, signal }),
                ),
              onSaved,
            );
          }}
        >
          {catalogue.nextPageToken && (
            <div className="grid gap-3">
              <Button
                disabled={!canSave || more.blocked || action.busy}
                loading={more.busy}
                onClick={() =>
                  void more.run(
                    async (signal) =>
                      quotaResult(
                        await listRepositories({
                          signal,
                          query: {
                            pageSize: 200,
                            pageToken: catalogue.nextPageToken,
                          },
                        }),
                      ),
                    (next) =>
                      setCatalogue((old) => ({
                        ...next,
                        nextPageToken: next.nextPageToken,
                        items: [
                          ...old.items,
                          ...next.items.filter(
                            (repo) =>
                              !old.items.some((item) => item.id === repo.id),
                          ),
                        ],
                      })),
                  )
                }
              >
                {text("加载更多仓库", "Load more repositories")}
              </Button>
              {Boolean(more.error) && !isQuotaAuthError(more.error) && (
                <ErrorBanner error={safeQuotaError(more.error, text)} />
              )}
            </div>
          )}
          <div className="grid min-w-0 grid-cols-1 items-start gap-4 sm:grid-cols-2">
            <Field
              label={text("仓库", "Repository")}
              hint={text(
                "创建后不能修改所属仓库。",
                "Repository scope cannot be changed after creation.",
              )}
            >
              <Select
                virtual={false}
                aria-label={text("仓库", "Repository")}
                style={{ width: "100%" }}
                value={repositoryId || undefined}
                disabled={Boolean(rule) || action.busy}
                onChange={setRepositoryId}
                placeholder={text("选择仓库", "Choose a repository")}
                options={[
                  ...catalogue.items
                    .filter(
                      (repo) =>
                        repo.state === "active" ||
                        repo.id === rule?.repositoryId,
                    )
                    .map((repo) => ({ value: repo.id, label: repo.name })),
                  ...(rule &&
                  !catalogue.items.some((repo) => repo.id === rule.repositoryId)
                    ? [{ value: rule.repositoryId, label: rule.repositoryId }]
                    : []),
                ]}
              />
            </Field>
            <Field
              label={text("邮件目标", "Email target")}
              hint={text(
                "仅显示名称和配置状态，不读取收件地址。",
                "Only the name and configuration status are shown; recipient addresses are not read.",
              )}
            >
              <Select
                virtual={false}
                aria-label={text("邮件目标", "Email target")}
                style={{ width: "100%" }}
                value={targetId || undefined}
                disabled={action.busy}
                onChange={setTargetId}
                placeholder={text("选择邮件目标", "Choose an email target")}
                options={targets.map((target) => ({
                  value: target.id,
                  disabled: Boolean(
                    rule?.enabled &&
                    (!target.enabled || !target.recipientConfigured),
                  ),
                  label: `${target.name} · ${target.locale === "en" ? "English" : "中文"} · ${target.enabled && target.recipientConfigured ? text("可用", "Ready") : text("未就绪", "Not ready")}`,
                }))}
              />
            </Field>
          </div>
          <fieldset className="grid min-w-0 gap-3 border-0 p-0">
            <legend className="mb-3 text-sm font-semibold text-zinc-100">
              {text("触发警告", "Trigger warning")}
            </legend>
            {fields("warningBasisPoints", "warningForSeconds", [
              "警告",
              "Warning",
            ])}
            <p className="text-xs text-zinc-500">
              {text(
                "用量达到阈值并持续指定时间后触发。",
                "Triggers after usage reaches the threshold for the specified duration.",
              )}
            </p>
          </fieldset>
          <fieldset className="grid min-w-0 gap-3 border-0 p-0">
            <legend className="mb-3 text-sm font-semibold text-zinc-100">
              {text("升级为严重", "Escalate to critical")}
            </legend>
            {fields("criticalBasisPoints", "criticalForSeconds", [
              "严重",
              "Critical",
            ])}
          </fieldset>
          <fieldset className="grid min-w-0 gap-3 border-0 p-0">
            <legend className="mb-3 text-sm font-semibold text-zinc-100">
              {text("恢复", "Recovery")}
            </legend>
            {fields("recoveryBelowBasisPoints", "recoveryForSeconds", [
              "恢复",
              "Recovery",
            ])}
            <p className="text-xs text-zinc-500">
              {text(
                "用量严格低于恢复阈值并持续指定时间后通知恢复。",
                "Recovery is notified after usage stays strictly below this threshold for the specified duration.",
              )}
            </p>
          </fieldset>
          <Field
            label={text("最大样本年龄（秒）", "Maximum sample age (seconds)")}
            hint={text(
              "陈旧或未知数据会中断持续计时，保留已有严重度。",
              "Stale or unknown evidence interrupts hold timing and retains existing severity.",
            )}
          >
            <Input
              aria-label={text(
                "最大样本年龄（秒）",
                "Maximum sample age (seconds)",
              )}
              inputMode="numeric"
              autoComplete="off"
              value={draft.maxSampleAgeSeconds}
              disabled={action.busy}
              onChange={(event) =>
                setDraft((old) => ({
                  ...old,
                  maxSampleAgeSeconds: event.target.value,
                }))
              }
            />
          </Field>
          <p className="text-sm text-zinc-500">
            {rule
              ? text(
                  "保存会重新绑定目标的当前版本，重置持续计时并保留严重度；未领取的旧自动交付会取消，已领取的交付可能完成一次。",
                  "Saving binds the current target version, resets hold timing, and retains severity. Unclaimed old automatic deliveries are cancelled; a claimed delivery may finish once.",
                )
              : text(
                  "创建默认停用，不发送测试邮件。保存后可查看前提并明确启用。",
                  "New rules are disabled by default and send no test email. Review prerequisites and explicitly enable after saving.",
                )}
            {rule?.enabled &&
              ` ${text("此规则已启用，保存后会继续评估并可能产生通知。", "This rule is enabled; saving continues evaluation and may produce notifications.")}`}
          </p>
          <div className="flex flex-wrap gap-2">
            <Button
              htmlType="submit"
              type="primary"
              loading={action.busy}
              disabled={!canSave || action.blocked}
            >
              {rule
                ? text("保存规则", "Save rule")
                : text("创建停用规则", "Create disabled rule")}
            </Button>
            <Button
              aria-label={text("取消", "Cancel")}
              disabled={action.busy}
              onClick={onCancel}
            >
              {text("取消", "Cancel")}
            </Button>
          </div>
        </form>
      </Card>
    </div>
  );
}
