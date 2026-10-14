import { useState } from "react";
import { Button, InputNumber, Popconfirm, Select, Space, Switch } from "antd";
import { replaceRetentionPolicy } from "../../client";
import type { Repository, RetentionPolicy } from "../../client";
import { ErrorBanner, Notice } from "../../components/ui/Feedback";
import { Field } from "../../components/ui/Layout";
import { useAuth } from "../../lib/auth";
import { usePreferences } from "../../lib/preferences";
import { formatDescriptor } from "../../formats";

import { retentionFormatCopy } from "./retentionCopy";
import { RetentionDryRunCard } from "./RetentionDryRunCard";
import { useRetentionDryRun } from "./useRetentionDryRun";

/**
 * The policy form, its dry run and execution. Mounted per policy version, so
 * the drafts start from the saved policy each time it changes.
 */
export function RetentionPolicyEditor({
  repo,
  policy,
  notice,
  setNotice,
  onSaved,
}: {
  repo: Repository;
  policy: RetentionPolicy;
  notice: string;
  setNotice: (notice: string) => void;
  onSaved: () => unknown;
}) {
  const { token } = useAuth();
  const { t } = usePreferences();
  const [enabled, setEnabled] = useState(policy.enabled ?? false);
  const [keepDays, setKeepDays] = useState(policy.keepDays);
  const [snapshotKeepDays, setSnapshotKeepDays] = useState(
    policy.snapshotKeepDays ?? policy.keepDays,
  );
  const [minimumVersions, setMinimumVersions] = useState(
    policy.minimumVersions,
  );
  const [maximumVersions, setMaximumVersions] = useState(
    policy.maximumVersions ?? 0,
  );
  const [keepDownloadedDays, setKeepDownloadedDays] = useState(
    policy.keepDownloadedDays ?? 0,
  );
  const [coordinatePatterns, setCoordinatePatterns] = useState<string[]>(
    policy.coordinatePatterns ?? [],
  );
  const [protectedPatterns, setProtectedPatterns] = useState<string[]>(
    policy.protectedPatterns ?? [],
  );
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<unknown>(null);
  const {
    dryRun,
    clear: clearDryRun,
    dryRunning,
    dryRunLoadingMore,
    executing,
    exporting,
    runDryRun,
    loadMoreDryRun,
    execute,
    exportDryRun,
  } = useRetentionDryRun({ repo, token, t, setSaveError, setNotice });
  const retentionModel =
    formatDescriptor(repo.format)?.retentionModel ?? "versions";
  const isMaven = retentionModel === "snapshots";
  const isRaw = retentionModel === "paths";
  const copy = retentionFormatCopy(repo.format, t);

  const save = async () => {
    if (!policy) return;
    if (!isRaw && maximumVersions > 0 && maximumVersions < minimumVersions) {
      setSaveError(
        new Error(t("repository.maximumVersionsMustBe0OrGreaterThan")),
      );
      return;
    }
    setSaving(true);
    setSaveError(null);
    setNotice("");
    try {
      const { error: err } = await replaceRetentionPolicy({
        path: { repositoryId: repo.id },
        body: {
          ...policy,
          enabled,
          keepDays,
          snapshotKeepDays: isMaven
            ? snapshotKeepDays
            : (policy.snapshotKeepDays ?? policy.keepDays),
          minimumVersions: isRaw ? policy.minimumVersions : minimumVersions,
          maximumVersions: isRaw
            ? (policy.maximumVersions ?? 0)
            : maximumVersions,
          keepDownloadedDays,
          coordinatePatterns,
          protectedPatterns,
        },
        headers: { "If-Match": policy.version },
      });
      if (err) {
        setSaveError(err);
        return;
      }
      setNotice(t("repository.policySaved"));
      clearDryRun();
      void onSaved();
    } catch (nextError) {
      setSaveError(nextError);
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="space-y-6">
      {saveError !== null && <ErrorBanner error={saveError} />}
      {notice && (
        <Notice tone="success" title={notice} onClose={() => setNotice("")} />
      )}
      {isRaw && (
        <Notice
          tone="info"
          closable={false}
          title={t("repository.rawCleanupByPathAsset")}
          description={t("repository.rawAssetsAreNotVersionGroupedSoMinimum")}
        />
      )}
      <div className="flex items-center justify-between border-b border-line pb-4">
        <div>
          <div className="text-sm font-medium text-fg">
            {t("repository.automaticCleanup")}
          </div>
          <div className="mt-1 text-xs text-fg-tertiary">
            {t("repository.whenDisabledNoScheduledOrManualCleanupTask")}
          </div>
        </div>
        <Switch
          checked={enabled}
          checkedChildren={t("repository.enabled")}
          unCheckedChildren={t("repository.disabled")}
          onChange={setEnabled}
        />
      </div>
      <div
        className={`grid max-w-5xl gap-4 ${!isMaven && !isRaw ? "grid-cols-3" : "grid-cols-2"}`}
      >
        <Field label={copy.ageLabel} hint={copy.ageHint}>
          <Space.Compact block>
            <InputNumber
              min={1}
              max={36500}
              precision={0}
              className="w-full"
              value={keepDays}
              onChange={(value) => setKeepDays(value ?? 0)}
            />
            <Space.Addon>{t("repository.days")}</Space.Addon>
          </Space.Compact>
        </Field>
        {isMaven && (
          <Field
            label={t("repository.snapshotRetentionDays")}
            hint={t("repository.mavenSNAPSHOTVersionsCanUseARetentionPeriod")}
          >
            <Space.Compact block>
              <InputNumber
                min={1}
                max={36500}
                precision={0}
                className="w-full"
                value={snapshotKeepDays}
                onChange={(value) => setSnapshotKeepDays(value ?? 0)}
              />
              <Space.Addon>{t("repository.days")}</Space.Addon>
            </Space.Compact>
          </Field>
        )}
        {!isRaw && (
          <Field label={copy.minimumLabel} hint={copy.minimumHint}>
            <Space.Compact block>
              <InputNumber
                min={1}
                max={100000}
                precision={0}
                className="w-full"
                value={minimumVersions}
                onChange={(value) => setMinimumVersions(value ?? 0)}
              />
              <Space.Addon>{t("repository.items")}</Space.Addon>
            </Space.Compact>
          </Field>
        )}
        {!isRaw && (
          <Field label={copy.maximumLabel} hint={copy.maximumHint}>
            <Space.Compact block>
              <InputNumber
                min={0}
                max={100000}
                precision={0}
                className="w-full"
                value={maximumVersions}
                onChange={(value) => setMaximumVersions(value ?? 0)}
              />
              <Space.Addon>{t("repository.items")}</Space.Addon>
            </Space.Compact>
          </Field>
        )}
        <Field
          label={t("repository.downloadProtectionDays")}
          hint={t(
            "repository.artifactsDownloadedWithinTheWindowSkipCleanupEven",
          )}
        >
          <Space.Compact block>
            <InputNumber
              min={0}
              max={36500}
              precision={0}
              className="w-full"
              value={keepDownloadedDays}
              onChange={(value) => setKeepDownloadedDays(value ?? 0)}
            />
            <Space.Addon>{t("repository.days")}</Space.Addon>
          </Space.Compact>
        </Field>
      </div>
      <div className="grid max-w-5xl grid-cols-2 gap-4 border-t border-line pt-4">
        <Field label={copy.matchLabel} hint={copy.matchHint}>
          <Select
            mode="tags"
            className="w-full font-mono text-xs"
            value={coordinatePatterns}
            onChange={setCoordinatePatterns}
            tokenSeparators={[",", " "]}
            maxTagCount="responsive"
            placeholder={copy.matchPlaceholder}
          />
        </Field>
        <Field label={copy.protectLabel} hint={copy.protectHint}>
          <Select
            mode="tags"
            className="w-full font-mono text-xs"
            value={protectedPatterns}
            onChange={setProtectedPatterns}
            tokenSeparators={[",", " "]}
            maxTagCount="responsive"
            placeholder={copy.protectPlaceholder}
          />
        </Field>
      </div>
      <Space>
        <Button type="primary" onClick={save} loading={saving}>
          {t("repository.savePolicy")}
        </Button>
        <Button onClick={runDryRun} loading={dryRunning} disabled={!enabled}>
          {t("repository.dryRun")}
        </Button>
        {dryRun && dryRun.candidates.length > 0 && (
          <Popconfirm
            title={t("repository.runRetentionCleanup")}
            description={t(
              "repository.thisWillCleanAllCandidateThePolicyVersion",
              {
                totalCandidates: dryRun.totalCandidates,
                candidateName: copy.candidateName,
              },
            )}
            okText={t("repository.runCleanup")}
            cancelText={t("repository.cancel")}
            okButtonProps={{ danger: true, loading: executing }}
            onConfirm={execute}
          >
            <Button danger loading={executing} disabled={!enabled}>
              {t("repository.runCleanup2", {
                totalCandidates: dryRun.totalCandidates,
              })}
            </Button>
          </Popconfirm>
        )}
      </Space>
      {dryRun && (
        <RetentionDryRunCard
          repo={repo}
          dryRun={dryRun}
          copy={copy}
          dryRunLoadingMore={dryRunLoadingMore}
          loadMoreDryRun={() => void loadMoreDryRun()}
          isRaw={isRaw}
          exporting={exporting}
          exportDryRun={exportDryRun}
        />
      )}
    </div>
  );
}
