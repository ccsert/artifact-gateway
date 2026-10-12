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
  const { text } = usePreferences();
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
          feature={text("APT 签名状态", "APT signing state")}
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
        feature={text("安全准入策略", "Security admission policy")}
      />
    ) : (
      <ErrorBanner error={error} onRetry={() => void policyQuery.refetch()} />
    );
  }
  if (!policy || (!readPolicy && readError === null)) return <Loading />;

  return (
    <div className="grid items-start gap-4 xl:grid-cols-[minmax(320px,0.78fr)_minmax(0,1.55fr)]">
      <div className="border-b border-line pb-4 xl:col-span-2">
        <div className="text-xs font-semibold uppercase tracking-[0.16em] text-[var(--ag-content-tertiary)]">
          {text("仓库安全防线", "Repository guardrails")}
        </div>
        <h2 className="mt-1.5 text-lg font-semibold tracking-tight text-fg-strong">
          {text("安全准入与隔离读取", "Admission and quarantined reads")}
        </h2>
        <p className="mt-1.5 max-w-3xl text-sm leading-6 text-fg-tertiary">
          {text(
            "这里有两条彼此独立的防线：读取策略决定已隔离制品能否被下载；晋升准入决定制品能否进入当前仓库。",
            "Two independent guardrails live here: the read policy decides whether quarantined artifacts can be downloaded, while admission decides whether artifacts may enter this repository.",
          )}
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
