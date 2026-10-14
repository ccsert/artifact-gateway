import { useState } from "react";
import { Button, Switch, Tag } from "antd";
import { replaceQuarantineReadPolicy } from "../../client";
import type { QuarantineReadPolicy, Repository } from "../../client";
import { ErrorBanner, Notice } from "../../components/ui/Feedback";
import { usePreferences } from "../../lib/preferences";
import { PolicyCard, ScopeFact } from "./RepositorySecurityPrimitives";

import { quarantineReadScope } from "./securityPolicyParts";
import { useSetQuarantineReadPolicy } from "./repositoryQueries";

/** Whether quarantined artifacts in this repository can still be downloaded. */
export function QuarantineReadPolicyCard({
  repo,
  readPolicy,
  loadError,
  onRetry,
}: {
  repo: Repository;
  readPolicy: QuarantineReadPolicy | null;
  loadError: unknown;
  onRetry: () => void;
}) {
  const { t } = usePreferences();
  const setStoredPolicy = useSetQuarantineReadPolicy(repo.id);
  const [readEnabled, setReadEnabled] = useState(readPolicy?.enabled ?? false);
  // Save failures are local; a failed read comes from the query.
  const [saveError, setReadError] = useState<unknown>(null);
  const readError = saveError ?? loadError;
  const [readSaving, setReadSaving] = useState(false);
  const [readNotice, setReadNotice] = useState("");
  const [draftSource, setDraftSource] = useState({
    repositoryId: repo.id,
    version: readPolicy?.version,
  });
  if (
    draftSource.repositoryId !== repo.id ||
    draftSource.version !== readPolicy?.version
  ) {
    if (draftSource.repositoryId !== repo.id) setReadNotice("");
    setDraftSource({ repositoryId: repo.id, version: readPolicy?.version });
    setReadEnabled(readPolicy?.enabled ?? false);
    setReadError(null);
  }
  const updateReadEnabled = (enabled: boolean) => {
    setReadNotice("");
    setReadError(null);
    setReadEnabled(enabled);
  };

  const saveReadPolicy = async () => {
    if (!readPolicy) return;
    setReadSaving(true);
    setReadError(null);
    setReadNotice("");
    try {
      const { data, error: err } = await replaceQuarantineReadPolicy({
        path: { repositoryId: repo.id },
        headers: { "If-Match": readPolicy.version },
        body: { version: readPolicy.version, enabled: readEnabled },
      });
      if (err) {
        setReadError(err);
        return;
      }
      setReadNotice(t("repository.quarantineReadPolicySaved"));
      if (data) {
        setStoredPolicy(data);
        setReadEnabled(data.enabled);
      }
    } catch (nextError) {
      setReadError(nextError);
    } finally {
      setReadSaving(false);
    }
  };

  const readDirty = readPolicy ? readPolicy.enabled !== readEnabled : false;
  const readScope = quarantineReadScope(repo.format, t);

  return (
    <PolicyCard
      eyebrow={t("repository.readGuardrail")}
      title={t("repository.quarantinedArtifactReads")}
      description={t(
        "repository.controlsWhetherClientsMayContinueReadingQuarantinedArtifacts",
      )}
      status={
        <>
          {readPolicy ? (
            <Tag color={readEnabled ? "warning" : "default"}>
              {readEnabled
                ? t("repository.readsBlocked")
                : t("repository.readsAllowed")}
            </Tag>
          ) : (
            <Tag color="error">{t("repository.unavailable2")}</Tag>
          )}
          {readDirty && (
            <Tag color="processing">{t("repository.unsavedChanges")}</Tag>
          )}
          {readPolicy && (
            <Switch
              aria-label={t("repository.blockQuarantinedArtifactReads")}
              checked={readEnabled}
              onChange={updateReadEnabled}
            />
          )}
        </>
      }
    >
      {readError !== null && (
        <ErrorBanner error={readError} onRetry={onRetry} />
      )}
      {readPolicy && (
        <>
          {readNotice && (
            <Notice
              tone="success"
              title={readNotice}
              onClose={() => setReadNotice("")}
            />
          )}
          <div className="grid gap-px overflow-hidden rounded-lg border border-line bg-[var(--ag-border-subtle)] sm:grid-cols-2">
            <ScopeFact
              label={t("repository.affectedRequests")}
              value="GET / HEAD"
            />
            <ScopeFact
              label={t("repository.currentFormatBoundary")}
              value={readScope.boundary}
            />
            <ScopeFact
              label={t("repository.discoveryMetadata")}
              value={readScope.metadata}
            />
            <ScopeFact
              label={t("repository.whenUsedByAGroup")}
              value={t("repository.noLowerPriorityFallback")}
            />
          </div>
          <div className="flex flex-col gap-4 border-t border-line pt-4">
            <p className="max-w-2xl text-xs leading-5 text-fg-tertiary">
              {t(
                "repository.readsRemainAllowedByDefaultForUpgradeCompatibility",
              )}
            </p>
            <div className="flex flex-wrap items-center justify-between gap-3">
              <span className="text-xs text-fg-disabled">
                {t("repository.readPolicyVersion", {
                  version: readPolicy.version,
                })}
              </span>
              <Button
                type="primary"
                danger={readEnabled}
                loading={readSaving}
                disabled={!readDirty}
                onClick={saveReadPolicy}
              >
                {t("repository.saveReadPolicy")}
              </Button>
            </div>
          </div>
        </>
      )}
    </PolicyCard>
  );
}
