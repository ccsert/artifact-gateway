import type { Translate } from "../../lib/preferences";
import { useState } from "react";
import {
  dryRunRepositoryRetention,
  executeRepositoryRetention,
} from "../../client";
import type { Repository, RetentionDryRun } from "../../client";
import { downloadCsv } from "../../lib/csv";

const RETENTION_DRY_RUN_PAGE_SIZE = 100;

/**
 * The retention dry run: run it, page through its candidates, execute the
 * previewed cleanup, or export it as CSV. Failures and outcomes go to the
 * editor's shared error and notice.
 */
export function useRetentionDryRun({
  repo,
  token,
  t,
  setSaveError,
  setNotice,
}: {
  repo: Repository;
  token: string | null | undefined;
  t: Translate;
  setSaveError: (error: unknown) => void;
  setNotice: (notice: string) => void;
}) {
  const [dryRun, setDryRun] = useState<RetentionDryRun | null>(null);
  const [dryRunning, setDryRunning] = useState(false);
  const [dryRunLoadingMore, setDryRunLoadingMore] = useState(false);
  const [executing, setExecuting] = useState(false);
  const [exporting, setExporting] = useState(false);

  const runDryRun = async () => {
    setDryRunning(true);
    setDryRun(null);
    setSaveError(null);
    const { data, error: err } = await dryRunRepositoryRetention({
      path: { repositoryId: repo.id },
      query: { pageSize: RETENTION_DRY_RUN_PAGE_SIZE },
    });
    setDryRunning(false);
    if (err) {
      setSaveError(err);
      return;
    }
    setDryRun(data ?? null);
  };

  const loadMoreDryRun = async () => {
    if (!dryRun?.nextPageToken || dryRunLoadingMore) return;
    setDryRunLoadingMore(true);
    const { data, error: err } = await dryRunRepositoryRetention({
      path: { repositoryId: repo.id },
      query: {
        pageSize: RETENTION_DRY_RUN_PAGE_SIZE,
        pageToken: dryRun.nextPageToken,
      },
    });
    setDryRunLoadingMore(false);
    if (err) {
      const code = (err as { code?: string } | undefined)?.code;
      if (code === "invalid_page_token") {
        setDryRun(null);
        setSaveError(
          new Error(t("repository.theDryRunResultExpiredOrThePolicy")),
        );
      } else {
        setSaveError(err);
      }
      return;
    }
    if (!data) return;
    setDryRun((current) =>
      current
        ? {
            ...data,
            candidates: [...current.candidates, ...data.candidates],
          }
        : data,
    );
  };

  const execute = async () => {
    if (!dryRun) return;
    setExecuting(true);
    const { error: err } = await executeRepositoryRetention({
      path: { repositoryId: repo.id },
      headers: {
        "Idempotency-Key": crypto.randomUUID(),
        "If-Match": dryRun.policyVersion,
      },
    });
    setExecuting(false);
    if (err) {
      const code = (err as { code?: string } | undefined)?.code;
      if (code === "version_conflict") {
        setDryRun(null);
        setSaveError(
          new Error(t("repository.theRetentionPolicyChangedSoThisPreviewIs")),
        );
      } else {
        setSaveError(err);
      }
      return;
    }
    setNotice(t("repository.theRetentionTaskWasSubmittedTrackItOn"));
    setDryRun(null);
  };

  const exportDryRun = async () => {
    setExporting(true);
    setSaveError(null);
    try {
      const response = await fetch(
        `/api/v2/repositories/${repo.id}/retention:dry-run?output=csv`,
        {
          method: "POST",
          credentials: "include",
          headers: token ? { Authorization: `Bearer ${token}` } : undefined,
        },
      );
      if (!response.ok) {
        const problem = (await response.json().catch(() => null)) as {
          message?: string;
        } | null;
        throw new Error(
          problem?.message ?? t("repository.failedToExportDryRunResults"),
        );
      }
      downloadCsv(`${repo.name}-retention.csv`, await response.text());
    } catch (nextError) {
      setSaveError(nextError);
    } finally {
      setExporting(false);
    }
  };

  return {
    dryRun,
    clear: () => setDryRun(null),
    dryRunning,
    dryRunLoadingMore,
    executing,
    exporting,
    runDryRun,
    loadMoreDryRun,
    execute,
    exportDryRun,
  };
}
