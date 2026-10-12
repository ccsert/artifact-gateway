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
  const { text } = usePreferences();
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
  } = useRetentionDryRun({ repo, token, text, setSaveError, setNotice });
  const retentionModel =
    formatDescriptor(repo.format)?.retentionModel ?? "versions";
  const isMaven = retentionModel === "snapshots";
  const isRaw = retentionModel === "paths";
  const copy = retentionFormatCopy(repo.format, text);

  const save = async () => {
    if (!policy) return;
    if (!isRaw && maximumVersions > 0 && maximumVersions < minimumVersions) {
      setSaveError(
        new Error(
          text(
            "最多保留版本数必须为 0，或不小于最少保留版本数",
            "Maximum versions must be 0 or greater than or equal to minimum versions.",
          ),
        ),
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
      setNotice(text("策略已保存", "Policy saved"));
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
          title={text("Raw 按路径资产清理", "Raw cleanup by path asset")}
          description={text(
            "Raw 没有版本分组，因此不应用最少或最多版本数；期限按资产最后更新时间计算。",
            "Raw assets are not version-grouped, so minimum and maximum versions do not apply. Age is calculated from the last update.",
          )}
        />
      )}
      <div className="flex items-center justify-between border-b border-line pb-4">
        <div>
          <div className="text-sm font-medium text-fg">
            {text("自动清理", "Automatic cleanup")}
          </div>
          <div className="mt-1 text-xs text-fg-tertiary">
            {text(
              "关闭时不会创建定时或手动清理任务，已有墓碑不受影响。",
              "When disabled, no scheduled or manual cleanup task is created. Existing tombstones are unaffected.",
            )}
          </div>
        </div>
        <Switch
          checked={enabled}
          checkedChildren={text("已启用", "Enabled")}
          unCheckedChildren={text("已停用", "Disabled")}
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
            <Space.Addon>{text("天", "days")}</Space.Addon>
          </Space.Compact>
        </Field>
        {isMaven && (
          <Field
            label={text("快照版本保留天数", "Snapshot retention days")}
            hint={text(
              "Maven SNAPSHOT 可使用独立于发布版本的保留期限。",
              "Maven SNAPSHOT versions can use a retention period separate from releases.",
            )}
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
              <Space.Addon>{text("天", "days")}</Space.Addon>
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
              <Space.Addon>{text("个", "items")}</Space.Addon>
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
              <Space.Addon>{text("个", "items")}</Space.Addon>
            </Space.Compact>
          </Field>
        )}
        <Field
          label={text("下载保护天数", "Download protection days")}
          hint={text(
            "窗口内被下载过的制品即使到期也跳过清理；0 表示关闭。依据生命周期下载统计。",
            "Artifacts downloaded within the window skip cleanup even when expired; 0 disables it. Based on lifecycle download usage.",
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
            <Space.Addon>{text("天", "days")}</Space.Addon>
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
          {text("保存策略", "Save policy")}
        </Button>
        <Button onClick={runDryRun} loading={dryRunning} disabled={!enabled}>
          {text("试运行", "Dry run")}
        </Button>
        {dryRun && dryRun.candidates.length > 0 && (
          <Popconfirm
            title={text("确认执行保留清理？", "Run retention cleanup?")}
            description={text(
              `将清理全部 ${dryRun.totalCandidates} 个候选${copy.candidateName}；执行前会再次校验策略版本。`,
              `This will clean all ${dryRun.totalCandidates} candidate ${copy.candidateName}. The policy version is checked again before execution.`,
            )}
            okText={text("执行清理", "Run cleanup")}
            cancelText={text("取消", "Cancel")}
            okButtonProps={{ danger: true, loading: executing }}
            onConfirm={execute}
          >
            <Button danger loading={executing} disabled={!enabled}>
              {text(
                `执行清理（${dryRun.totalCandidates} 个）`,
                `Run cleanup (${dryRun.totalCandidates})`,
              )}
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
