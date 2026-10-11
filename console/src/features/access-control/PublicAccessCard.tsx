import { Popconfirm, Switch } from "antd";
import type { AnonymousAccessPolicy, Repository } from "../../client";
import { Badge } from "../../components/ui/Badge";
import { ErrorBanner, Notice } from "../../components/ui/Feedback";
import { Card } from "../../components/ui/Layout";
import { usePreferences } from "../../lib/preferences";
import { useReplaceAnonymousPolicy } from "./accessQueries";

/** The global anonymous-read gate and how many repositories are public. */
export function PublicAccessCard({
  anonymousPolicy,
  loadError,
  canManageAnonymousPolicy,
  repositories,
  directoryError,
  onRetry,
}: {
  anonymousPolicy: AnonymousAccessPolicy | null;
  loadError: unknown;
  canManageAnonymousPolicy: boolean;
  repositories: Repository[];
  directoryError: unknown;
  onRetry: () => void;
}) {
  const { t } = usePreferences();
  const repositoryCount = repositories.length;
  const publicRepositoryCount = repositories.filter(
    (repository) => repository.anonymousRead,
  ).length;
  const replacePolicy = useReplaceAnonymousPolicy();
  const savingAnonymousPolicy = replacePolicy.isPending;
  const anonymousPolicyError = replacePolicy.error ?? loadError ?? null;
  const updateAnonymousPolicy = (enabled: boolean) => {
    if (!anonymousPolicy || savingAnonymousPolicy) return;
    replacePolicy.mutate({ ...anonymousPolicy, enabled });
  };

  return (
    <Card className="ag-public-access-card" bodyClassName="p-5">
      <div className="grid gap-4">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1">
            <h2 className="text-sm font-semibold text-fg-strong">
              {t("access.publicAccessBoundary")}
            </h2>
            <span className="text-xs text-fg-tertiary">
              {t("access.repositoriesPublic", {
                publicRepositoryCount: String(publicRepositoryCount),
                repositoryCount: String(repositoryCount),
              })}
            </span>
          </div>
          {anonymousPolicy && canManageAnonymousPolicy ? (
            <Popconfirm
              title={
                anonymousPolicy.enabled
                  ? t("access.disableGlobalAnonymousReads")
                  : t("access.enableGlobalAnonymousReads")
              }
              description={
                anonymousPolicy.enabled
                  ? t("access.allUnauthenticatedProtocolReadsWillBeRejected")
                  : t(
                      "access.artifactsAllowedByRepositoryOrGroupPolicyBecomeReadable",
                    )
              }
              okText={t("access.continue")}
              cancelText={t("access.cancel")}
              okButtonProps={{
                danger: anonymousPolicy.enabled,
                loading: savingAnonymousPolicy,
              }}
              onConfirm={() => updateAnonymousPolicy(!anonymousPolicy.enabled)}
            >
              <Switch
                checked={anonymousPolicy.enabled}
                loading={savingAnonymousPolicy}
                aria-label={t("access.toggleGlobalAnonymousReads")}
                onChange={() => undefined}
              />
            </Popconfirm>
          ) : canManageAnonymousPolicy ? (
            anonymousPolicyError === null ? (
              <span className="text-xs text-fg-tertiary">
                {t("access.loading")}
              </span>
            ) : null
          ) : (
            <Badge tone="neutral">
              {anonymousPolicy
                ? anonymousPolicy.enabled
                  ? t("access.globalGateOnReadOnly")
                  : t("access.globalGateOffReadOnly")
                : t("access.readOnly")}
            </Badge>
          )}
        </div>
        <Notice
          tone="info"
          title={t(
            "access.anonymousReadsRequireGlobalRepositoryAndApplicableGroupOpt",
          )}
        />
        {directoryError !== null && (
          <ErrorBanner error={directoryError} onRetry={onRetry} />
        )}
        {anonymousPolicyError !== null && (
          <ErrorBanner
            error={anonymousPolicyError}
            onRetry={() => {
              replacePolicy.reset();
              onRetry();
            }}
            tone={anonymousPolicy ? "warning" : "error"}
          />
        )}
      </div>
    </Card>
  );
}
