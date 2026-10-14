import type { Repository } from "../../client";
import { ErrorBanner, Loading, isNotFound } from "../../components/ui/Feedback";
import { usePreferences } from "../../lib/preferences";
import { APTSigningStatePanel } from "./APTSigningStatePanel";
import { RepositoryFeatureUnavailable } from "./RepositoryFeatureUnavailable";
import { formatDescriptor } from "../../formats";

import { AdmissionPolicyCard } from "./AdmissionPolicyCard";
import { QuarantineReadPolicyCard } from "./QuarantineReadPolicyCard";
import {
  useAptSigningState,
  useQuarantineReadPolicy,
  useSecurityPolicy,
} from "./repositoryQueries";

/**
 * Repository guardrails: hosted APT shows its signing state; other
 * repositories edit quarantined reads and promotion admission.
 */
export function RepositorySecurityTab({
  repo,
  publicationScanning,
}: {
  repo: Repository;
  publicationScanning: boolean;
}) {
  const { t } = usePreferences();
  const aptSigningApplicable =
    Boolean(formatDescriptor(repo.format)?.signedIndexes) &&
    repo.type === "hosted";
  const signing = useAptSigningState(repo.id, aptSigningApplicable);
  const policyQuery = useSecurityPolicy(repo.id, !aptSigningApplicable);
  const readQuery = useQuarantineReadPolicy(repo.id, !aptSigningApplicable);
  const policy = policyQuery.data ?? null;
  const error = policyQuery.error;
  const readPolicy = readQuery.data ?? null;
  const readError = readQuery.error;

  if (aptSigningApplicable) {
    if (signing.error !== null) {
      return isNotFound(signing.error) ? (
        <RepositoryFeatureUnavailable
          feature={t("repository.aPTSigningState")}
        />
      ) : (
        <ErrorBanner
          error={signing.error}
          onRetry={() => void signing.refetch()}
        />
      );
    }
    if (!signing.data) return <Loading />;
    return <APTSigningStatePanel state={signing.data} />;
  }

  if (error !== null) {
    return isNotFound(error) ? (
      <RepositoryFeatureUnavailable
        feature={t("repository.securityAdmissionPolicy")}
      />
    ) : (
      <ErrorBanner error={error} onRetry={() => void policyQuery.refetch()} />
    );
  }
  if (!policy || (!readPolicy && readError === null)) return <Loading />;

  return (
    <div className="grid items-start gap-4 xl:grid-cols-[minmax(320px,0.78fr)_minmax(0,1.55fr)]">
      <div className="border-b border-line pb-4 xl:col-span-2">
        <div className="text-xs font-semibold text-[var(--ag-content-tertiary)]">
          {t("repository.repositoryGuardrails")}
        </div>
        <h2 className="mt-1.5 text-lg font-semibold tracking-tight text-fg-strong">
          {t("repository.admissionAndQuarantinedReads")}
        </h2>
        <p className="mt-1.5 max-w-3xl text-sm leading-6 text-fg-tertiary">
          {t("repository.twoIndependentGuardrailsLiveHereTheReadPolicy")}
        </p>
      </div>

      <QuarantineReadPolicyCard
        // Remount when the policy first arrives, so the draft starts from it.
        key={`${repo.id}:${readPolicy ? "loaded" : "missing"}`}
        repo={repo}
        readPolicy={readPolicy}
        loadError={readError}
        onRetry={() => void readQuery.refetch()}
      />

      <AdmissionPolicyCard
        key={repo.id}
        repo={repo}
        policy={policy}
        publicationScanning={publicationScanning}
      />
    </div>
  );
}
