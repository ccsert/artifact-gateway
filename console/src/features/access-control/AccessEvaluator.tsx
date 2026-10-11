import { useMemo, useState } from "react";
import { Badge } from "../../components/ui/Badge";
import { Button, Collapse, Input, Select } from "antd";
import { ExperimentOutlined, ReloadOutlined } from "@ant-design/icons";
import { getRepositoryEffectiveAccess } from "../../client";
import type {
  ApiKey,
  Repository,
  RepositoryEffectiveAccess,
  ServiceAccount,
  User,
} from "../../client";
import { Card } from "../../components/ui/Layout";
import { ErrorBanner, Notice, Spinner } from "../../components/ui/Feedback";
import { FilterField } from "../../components/ui/ConsolePrimitives";
import { useAuth } from "../../lib/auth";
import {
  isActiveApiKeyPrincipal,
  isActiveUserPrincipal,
} from "../../lib/accessPrincipals";
import { usePreferences } from "../../lib/preferences";
import { AccessDecisionSummary } from "./AccessDecisionSummary";
import {
  AUTHORIZATION_STEPS,
  CURRENT_PRINCIPAL,
  CUSTOM_PRINCIPAL,
  ROLE_REFERENCE,
  resourcePlaceholder,
  strongestRole,
  type EvaluatorPrincipal,
  type EvaluatorRole,
} from "./accessModel";

/** Runs the Gateway's real decision chain for a chosen principal and resource. */
export function AccessEvaluator({
  users,
  apiKeys,
  serviceAccounts,
  repositories,
  evaluatorOptionsLoading,
  evaluatorOptionsError,
  serviceAccountsError,
  onRetryDirectory,
}: {
  users: User[];
  apiKeys: ApiKey[];
  serviceAccounts: ServiceAccount[];
  repositories: Repository[];
  evaluatorOptionsLoading: boolean;
  evaluatorOptionsError: unknown;
  serviceAccountsError: unknown;
  onRetryDirectory: () => void;
}) {
  const { identity, identityLoading } = useAuth();
  const { locale, text } = usePreferences();
  const english = locale === "en-US";
  const [selectedPrincipal, setSelectedPrincipal] = useState(CURRENT_PRINCIPAL);
  const [customActor, setCustomActor] = useState("");
  const [customRole, setCustomRole] = useState<EvaluatorRole>("none");
  const [chosenRepository, setSelectedRepository] = useState("");
  // The chosen repository while it is still active, otherwise the first one.
  const selectedRepository = repositories.some(
    (repository) => repository.id === chosenRepository,
  )
    ? chosenRepository
    : (repositories[0]?.id ?? "");
  const [resource, setResource] = useState("");
  const [evaluation, setEvaluation] =
    useState<RepositoryEffectiveAccess | null>(null);
  const [evaluationError, setEvaluationError] = useState<unknown>(null);
  const [evaluating, setEvaluating] = useState(false);

  const evaluatorPrincipals = useMemo<EvaluatorPrincipal[]>(() => {
    const choices: EvaluatorPrincipal[] = [];
    if (identity) {
      choices.push({
        value: CURRENT_PRINCIPAL,
        actor: identity.actor,
        label: text("当前登录身份", "Current identity"),
        detail: `${identity.actor} · ${identity.role ?? text("无全局角色", "no global role")}`,
        role: identity.role ?? "none",
        current: true,
      });
    }
    choices.push(
      ...users.filter(isActiveUserPrincipal).map((user) => ({
        value: `user:${user.name}`,
        actor: `user:${user.name}`,
        label: `${text("用户", "User")} · ${user.name}`,
        detail: `${text("全局角色", "Global role")} ${user.role}`,
        role: user.role,
      })),
      ...apiKeys
        .filter((key) => isActiveApiKeyPrincipal(key))
        .map((key) => {
          const role = strongestRole(key.roles);
          return {
            value: `api-key:${key.id}`,
            actor: `api-key:${key.id}`,
            label: `API Key · ${key.name}`,
            detail: `${text("全局角色", "Global role")} ${
              role === "none" ? text("无", "none") : role
            }`,
            role,
          };
        }),
      ...serviceAccounts
        .filter((account) => account.state === "active")
        .map((account) => ({
          value: `service-account:${account.id}`,
          actor: `service-account:${account.id}`,
          label: `${text("服务账号", "Service account")} · ${account.name}`,
          detail: text(
            "无全局角色，由仓库授权决定",
            "No global role; repository grants decide",
          ),
          role: "none" as const,
        })),
      {
        value: CUSTOM_PRINCIPAL,
        actor: customActor.trim(),
        label: text("OIDC / 自定义 actor", "OIDC / custom actor"),
        detail: text(
          "输入认证后产生的完整 actor，并选择其全局角色",
          "Enter the complete authenticated actor and select its global role",
        ),
        role: customRole,
      },
    );
    return choices;
  }, [
    apiKeys,
    customActor,
    customRole,
    identity,
    serviceAccounts,
    text,
    users,
  ]);

  const selectedPrincipalChoice = evaluatorPrincipals.find(
    (choice) => choice.value === selectedPrincipal,
  );
  const selectedRepositoryChoice = repositories.find(
    (repository) => repository.id === selectedRepository,
  );
  const effectiveEvaluatorRole =
    selectedPrincipal === CUSTOM_PRINCIPAL
      ? customRole
      : (selectedPrincipalChoice?.role ?? "none");

  const runEvaluation = async () => {
    const choice = selectedPrincipalChoice;
    if (!choice || !selectedRepository) {
      setEvaluationError(
        new Error(
          text("请选择主体和仓库", "Select a principal and repository"),
        ),
      );
      return;
    }
    const actor =
      selectedPrincipal === CUSTOM_PRINCIPAL
        ? customActor.trim()
        : choice.actor;
    if (!choice.current && !actor) {
      setEvaluationError(
        new Error(
          text(
            "请输入认证系统产生的完整 actor",
            "Enter the complete actor produced by the authentication system",
          ),
        ),
      );
      return;
    }
    setEvaluating(true);
    setEvaluationError(null);
    const { data, error: err } = await getRepositoryEffectiveAccess({
      path: { repositoryId: selectedRepository },
      query: choice.current
        ? { resource: resource.trim() || undefined }
        : {
            actor,
            role:
              effectiveEvaluatorRole === "none"
                ? undefined
                : effectiveEvaluatorRole,
            resource: resource.trim() || undefined,
          },
    });
    setEvaluating(false);
    if (err || !data) {
      setEvaluationError(
        err ?? new Error(text("权限检查失败", "Access evaluation failed")),
      );
      return;
    }
    setEvaluation(data);
  };

  return (
    <div className="ag-page-stack">
      <Card bodyClassName="p-0">
        <div className="flex items-start justify-between gap-6 border-b border-line px-5 py-4">
          <div>
            <div className="flex items-center gap-2 text-sm font-semibold text-fg-strong">
              <ExperimentOutlined className="text-[var(--ag-content-tertiary)]" />
              {text("权限检查", "Access evaluation")}
            </div>
            <p className="mt-1 text-xs text-fg-tertiary">
              {text(
                "使用网关真实判定链检查全局角色、仓库授权和资源前缀叠加后的结果。",
                "Run the Gateway decision chain across global roles, repository grants, and resource prefixes.",
              )}
            </p>
          </div>
          {(evaluatorOptionsLoading || identityLoading) && <Spinner />}
        </div>
        <div className="space-y-4 px-5 py-4">
          {evaluatorOptionsError !== null && (
            <ErrorBanner error={evaluatorOptionsError} />
          )}
          {serviceAccountsError !== null && (
            <Notice
              tone="warning"
              closable={false}
              title={text(
                "部分身份来源不可用",
                "Some identity sources are unavailable",
              )}
              description={text(
                "服务账号暂时不可用，仍可检查当前身份、用户、API Key 和自定义 actor。",
                "Service accounts are temporarily unavailable. You can still evaluate the current identity, users, API keys, and custom actors.",
              )}
              action={
                <Button
                  size="small"
                  icon={<ReloadOutlined />}
                  onClick={onRetryDirectory}
                >
                  {text("重试", "Retry")}
                </Button>
              }
            />
          )}
          <div className="grid grid-cols-[minmax(210px,1.25fr)_124px_minmax(160px,1fr)_minmax(170px,1fr)_auto] items-end gap-3">
            <FilterField label={text("授权主体", "Principal")}>
              <Select
                className="w-full"
                aria-label={text("授权主体", "Principal")}
                showSearch={{ optionFilterProp: "label" }}
                loading={evaluatorOptionsLoading || identityLoading}
                value={selectedPrincipal}
                options={evaluatorPrincipals.map((choice) => ({
                  value: choice.value,
                  label: `${choice.label} · ${choice.detail}`,
                }))}
                onChange={(value) => {
                  setSelectedPrincipal(value);
                  setEvaluation(null);
                  setEvaluationError(null);
                }}
              />
            </FilterField>
            <FilterField label={text("全局角色", "Global role")}>
              <Select
                className="w-full"
                value={effectiveEvaluatorRole}
                disabled={selectedPrincipal !== CUSTOM_PRINCIPAL}
                options={[
                  { value: "none", label: text("无", "None") },
                  { value: "member", label: "member" },
                  { value: "admin", label: "admin" },
                ]}
                onChange={(value: EvaluatorRole) => {
                  setCustomRole(value);
                  setEvaluation(null);
                }}
              />
            </FilterField>
            <FilterField label={text("仓库", "Repository")}>
              <Select
                className="w-full"
                showSearch={{ optionFilterProp: "label" }}
                loading={evaluatorOptionsLoading}
                value={selectedRepository || undefined}
                placeholder={text("选择仓库", "Select repository")}
                options={repositories.map((repository) => ({
                  value: repository.id,
                  label: `${repository.name} · ${repository.format}`,
                }))}
                onChange={(value) => {
                  setSelectedRepository(value);
                  setResource("");
                  setEvaluation(null);
                  setEvaluationError(null);
                }}
              />
            </FilterField>
            <FilterField
              label={text("具体资源（可选）", "Resource (optional)")}
            >
              <Input
                className="font-mono"
                allowClear
                placeholder={
                  resourcePlaceholder(selectedRepositoryChoice?.format) ||
                  text(
                    "留空检查整个仓库",
                    "Blank evaluates repository-wide access",
                  )
                }
                value={resource}
                onChange={(event) => {
                  setResource(event.target.value);
                  setEvaluation(null);
                  setEvaluationError(null);
                }}
                onPressEnter={() => void runEvaluation()}
              />
            </FilterField>
            <Button
              type="primary"
              icon={<ExperimentOutlined />}
              loading={evaluating}
              disabled={evaluatorOptionsLoading || !selectedRepository}
              onClick={() => void runEvaluation()}
            >
              {text("检查", "Evaluate")}
            </Button>
          </div>
          {selectedPrincipal === CUSTOM_PRINCIPAL && (
            <div className="mt-3 grid grid-cols-[minmax(210px,1.25fr)_124px_minmax(160px,1fr)_minmax(170px,1fr)_auto] gap-3">
              <FilterField
                label={text("完整 actor 标识", "Complete actor identifier")}
                className="col-span-2"
              >
                <Input
                  className="font-mono"
                  placeholder={text(
                    "例如 oidc:gitlab:team/release 或 ci-bot",
                    "For example: oidc:gitlab:team/release or ci-bot",
                  )}
                  value={customActor}
                  onChange={(event) => {
                    setCustomActor(event.target.value);
                    setEvaluation(null);
                    setEvaluationError(null);
                  }}
                  onPressEnter={() => void runEvaluation()}
                />
              </FilterField>
              <div className="col-span-3 flex items-end pb-2 text-xs text-fg-disabled">
                {text(
                  "该值必须与认证完成后产生的 actor 完全一致；OIDC 角色需按实际映射结果选择。",
                  "This must exactly match the authenticated actor; choose the global role produced by the OIDC mapping.",
                )}
              </div>
            </div>
          )}
          {evaluationError !== null && (
            <div className="mt-4">
              <ErrorBanner error={evaluationError} />
            </div>
          )}
          {evaluation && (
            <div className="mt-4 border-t border-line pt-4">
              <div className="mb-3 flex min-w-0 items-center gap-2 text-xs text-fg-tertiary">
                <Badge tone={evaluation.simulated ? "info" : "success"}>
                  {evaluation.simulated
                    ? text("模拟结果", "Simulated")
                    : text("当前身份", "Current identity")}
                </Badge>
                <span className="font-mono text-fg-secondary">
                  {evaluation.actor}
                </span>
                <span>·</span>
                <span>{evaluation.repository.name}</span>
                <span>·</span>
                <span className="min-w-0 truncate font-mono">
                  {evaluation.resource || text("整个仓库", "Entire repository")}
                </span>
              </div>
              <AccessDecisionSummary access={evaluation} />
            </div>
          )}
        </div>
      </Card>
      <Collapse
        ghost
        items={[
          {
            key: "authorization",
            label: (
              <span className="text-xs text-fg-secondary">
                {text(
                  "权限判定顺序与角色能力",
                  "Authorization order and roles",
                )}{" "}
                <span className="ml-2 text-fg-disabled">
                  {text("了解规则如何叠加", "How rules combine")}
                </span>
              </span>
            ),
            children: (
              <div>
                <p className="mb-4 text-xs leading-5 text-fg-tertiary">
                  {text(
                    "仓库规则只追加权限，不能撤销全局角色。",
                    "Repository rules add permissions; they cannot revoke a global role.",
                  )}
                </p>
                <div className="grid grid-cols-4 gap-5">
                  {AUTHORIZATION_STEPS.map((step, index) => (
                    <div key={step.title} className="border-l border-line pl-3">
                      <div className="text-xs font-medium text-fg-secondary">
                        {index + 1}. {english ? step.titleEn : step.title}
                      </div>
                      <p className="mt-1 text-xs leading-5 text-fg-tertiary">
                        {english ? step.textEn : step.text}
                      </p>
                    </div>
                  ))}
                </div>
                <div className="mt-5 grid grid-cols-3 gap-3 border-t border-line pt-4">
                  {ROLE_REFERENCE.map((item) => (
                    <div
                      key={item.role}
                      className="flex items-start gap-2 text-xs text-fg-secondary"
                    >
                      <Badge tone={item.tone}>{item.role}</Badge>
                      <span>{english ? item.descEn : item.desc}</span>
                    </div>
                  ))}
                </div>
              </div>
            ),
          },
        ]}
      />
    </div>
  );
}
