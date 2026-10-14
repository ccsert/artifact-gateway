import { useState } from "react";
import type { Repository } from "../../client";
import { ErrorBanner, Loading, isNotFound } from "../../components/ui/Feedback";
import { usePreferences } from "../../lib/preferences";
import { supportsRepositoryFeature } from "../../formats";
import { RepositoryFeatureUnavailable } from "./RepositoryFeatureUnavailable";

import { RetentionPolicyEditor } from "./RetentionPolicyEditor";
import {
  useInvalidateRetentionPolicy,
  useRetentionPolicy,
} from "./repositoryQueries";

/** A repository's retention policy, loaded once and edited per version. */
export function RepositoryRetentionTab({ repo }: { repo: Repository }) {
  const { t } = usePreferences();
  const policyQuery = useRetentionPolicy(repo.id);
  const invalidatePolicy = useInvalidateRetentionPolicy(repo.id);
  const policy = policyQuery.data ?? null;
  const error = policyQuery.error;
  const load = () => void policyQuery.refetch();
  // The notice outlives a save, which mounts the editor for the new version.
  const [notice, setNotice] = useState("");

  if (error !== null)
    return isNotFound(error) ? (
      <RepositoryFeatureUnavailable feature={t("repository.retentionPolicy")} />
    ) : (
      <ErrorBanner error={error} onRetry={load} />
    );
  if (!policy) return <Loading />;

  if (!supportsRepositoryFeature(repo.format, repo.type, "retention")) {
    return (
      <RepositoryFeatureUnavailable
        feature={t("repository.hostedRepositoryRetention")}
      />
    );
  }

  return (
    <RetentionPolicyEditor
      key={policy.version}
      repo={repo}
      policy={policy}
      notice={notice}
      setNotice={setNotice}
      onSaved={invalidatePolicy}
    />
  );
}
