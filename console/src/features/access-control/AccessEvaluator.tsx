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
  const { t } = usePreferences();
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
        label: t("access.currentIdentity"),
        detail: `${identity.actor} · ${identity.role ?? t("access.noGlobalRole")}`,
        role: identity.role ?? "none",
        current: true,
      });
    }
    choices.push(
      ...users.filter(isActiveUserPrincipal).map((user) => ({
        value: `user:${user.name}`,
        actor: `user:${user.name}`,
        label: `${t("access.user")} · ${user.name}`,
        detail: `${t("access.globalRole")} ${user.role}`,
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
            detail: `${t("access.globalRole")} ${
              role === "none" ? t("access.none") : role
            }`,
            role,
          };
        }),
      ...serviceAccounts
        .filter((account) => account.state === "active")
        .map((account) => ({
          value: `service-account:${account.id}`,
          actor: `service-account:${account.id}`,
          label: `${t("access.serviceAccount")} · ${account.name}`,
          detail: t("access.noGlobalRoleRepositoryGrantsDecide"),
          role: "none" as const,
        })),
      {
        value: CUSTOM_PRINCIPAL,
        actor: customActor.trim(),
        label: t("access.oidcCustomActor"),
        detail: t(
          "access.enterTheCompleteAuthenticatedActorAndSelectItsGlobal",
        ),
        role: customRole,
      },
    );
    return choices;
  }, [apiKeys, customActor, customRole, identity, serviceAccounts, t, users]);

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
      setEvaluationError(new Error(t("access.selectAPrincipalAndRepository")));
      return;
    }
    const actor =
      selectedPrincipal === CUSTOM_PRINCIPAL
        ? customActor.trim()
        : choice.actor;
    if (!choice.current && !actor) {
      setEvaluationError(
        new Error(
          t("access.enterTheCompleteActorProducedByTheAuthenticationSystem"),
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
      setEvaluationError(err ?? new Error(t("access.accessEvaluationFailed")));
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
              {t("access.accessEvaluation")}
            </div>
            <p className="mt-1 text-xs text-fg-tertiary">
              {t(
                "access.runTheGatewayDecisionChainAcrossGlobalRolesRepository",
              )}
            </p>
          </div>
          {(evaluatorOptionsLoading || identityLoading) && <Spinner />}
        </div>
        <div className="space-y-4 px-5 py-4">
          {evaluatorOptionsError !== null && (
            <ErrorBanner
              error={evaluatorOptionsError}
              onRetry={onRetryDirectory}
            />
          )}
          {serviceAccountsError !== null && (
            <Notice
              tone="warning"
              closable={false}
              title={t("access.someIdentitySourcesAreUnavailable")}
              description={t(
                "access.serviceAccountsAreTemporarilyUnavailableYouCanStillEvaluate",
              )}
              action={
                <Button
                  size="small"
                  icon={<ReloadOutlined />}
                  onClick={onRetryDirectory}
                >
                  {t("access.retry")}
                </Button>
              }
            />
          )}
          <div className="grid grid-cols-[minmax(210px,1.25fr)_124px_minmax(160px,1fr)_minmax(170px,1fr)_auto] items-end gap-3">
            <FilterField label={t("access.principal")}>
              <Select
                className="w-full"
                aria-label={t("access.principal")}
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
            <FilterField label={t("access.globalRole")}>
              <Select
                className="w-full"
                value={effectiveEvaluatorRole}
                disabled={selectedPrincipal !== CUSTOM_PRINCIPAL}
                options={[
                  { value: "none", label: t("access.none2") },
                  { value: "member", label: "member" },
                  { value: "admin", label: "admin" },
                ]}
                onChange={(value: EvaluatorRole) => {
                  setCustomRole(value);
                  setEvaluation(null);
                }}
              />
            </FilterField>
            <FilterField label={t("access.repository")}>
              <Select
                className="w-full"
                showSearch={{ optionFilterProp: "label" }}
                loading={evaluatorOptionsLoading}
                value={selectedRepository || undefined}
                placeholder={t("access.selectRepository")}
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
            <FilterField label={t("access.resourceOptional")}>
              <Input
                className="font-mono"
                allowClear
                placeholder={
                  resourcePlaceholder(selectedRepositoryChoice?.format) ||
                  t("access.blankEvaluatesRepositoryWideAccess")
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
              {t("access.evaluate")}
            </Button>
          </div>
          {selectedPrincipal === CUSTOM_PRINCIPAL && (
            <div className="mt-3 grid grid-cols-[minmax(210px,1.25fr)_124px_minmax(160px,1fr)_minmax(170px,1fr)_auto] gap-3">
              <FilterField
                label={t("access.completeActorIdentifier")}
                className="col-span-2"
              >
                <Input
                  className="font-mono"
                  placeholder={t(
                    "access.forExampleOidcGitlabTeamReleaseOrCiBot",
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
                {t("access.thisMustExactlyMatchTheAuthenticatedActorChooseThe")}
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
                    ? t("access.simulated")
                    : t("access.currentIdentity2")}
                </Badge>
                <span className="font-mono text-fg-secondary">
                  {evaluation.actor}
                </span>
                <span>·</span>
                <span>{evaluation.repository.name}</span>
                <span>·</span>
                <span className="min-w-0 truncate font-mono">
                  {evaluation.resource || t("access.entireRepository")}
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
                {t("access.authorizationOrderAndRoles")}{" "}
                <span className="ml-2 text-fg-disabled">
                  {t("access.howRulesCombine")}
                </span>
              </span>
            ),
            children: (
              <div>
                <p className="mb-4 text-xs leading-5 text-fg-tertiary">
                  {t(
                    "access.repositoryRulesAddPermissionsTheyCannotRevokeAGlobal",
                  )}
                </p>
                <div className="grid grid-cols-4 gap-5">
                  {AUTHORIZATION_STEPS.map((step, index) => (
                    <div key={step.title} className="border-l border-line pl-3">
                      <div className="text-xs font-medium text-fg-secondary">
                        {index + 1}. {t(step.title)}
                      </div>
                      <p className="mt-1 text-xs leading-5 text-fg-tertiary">
                        {t(step.text)}
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
                      <span>{t(item.desc)}</span>
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
