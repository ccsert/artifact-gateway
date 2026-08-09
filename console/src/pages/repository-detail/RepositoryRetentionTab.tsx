import { useCallback, useEffect, useState } from "react";
import {
  Alert,
  Button,
  InputNumber,
  Popconfirm,
  Select,
  Space,
  Switch,
  Table,
  Tooltip,
} from "antd";
import type { ColumnsType } from "antd/es/table";
import { DownloadOutlined } from "@ant-design/icons";
import {
  dryRunRepositoryRetention,
  executeRepositoryRetention,
  getRetentionPolicy,
  replaceRetentionPolicy,
} from "../../client";
import type {
  Repository,
  RetentionDryRun,
  RetentionPolicy,
} from "../../client";
import {
  EmptyState,
  ErrorBanner,
  Loading,
  isNotFound,
} from "../../components/Feedback";
import { Card, CardHeader, Field } from "../../components/Layout";
import { useAuth } from "../../lib/auth";
import { downloadCsv } from "../../lib/csv";
import { formatDate, shortDigest } from "../../lib/format";
import { usePreferences } from "../../lib/preferences";
import { RepositoryFeatureUnavailable } from "./RepositoryFeatureUnavailable";

type Localize = (chinese: string, english: string) => string;

const RETENTION_DRY_RUN_PAGE_SIZE = 100;

function retentionFormatCopy(format: Repository["format"], text: Localize) {
  switch (format) {
    case "oci":
      return {
        ageLabel: text("镜像版本保留天数", "Image version retention days"),
        ageHint: text(
          "Manifest 创建超过此天数后，才会进入清理候选。",
          "A manifest becomes eligible after this many days.",
        ),
        minimumLabel: text(
          "每个镜像最少保留版本",
          "Minimum versions per image",
        ),
        minimumHint: text(
          "按镜像名称分组，始终保护最新的这些 manifest。",
          "Group by image name and always protect these newest manifests.",
        ),
        maximumLabel: text(
          "每个镜像最多保留版本",
          "Maximum versions per image",
        ),
        maximumHint: text(
          "0 表示不限制；超过上限的旧 manifest 会进入候选。",
          "Use 0 for no limit. Older manifests beyond the limit become eligible.",
        ),
        matchLabel: text("只清理匹配镜像", "Only clean matching images"),
        matchHint: text(
          "可匹配镜像名、name@digest 或 name:tag；留空表示全部。",
          "Matches image name, name@digest, or name:tag. Leave empty for all images.",
        ),
        protectLabel: text("保护镜像版本", "Protect image versions"),
        protectHint: text(
          "可用镜像名保护全部版本，或用 digest、tag 精确保护。",
          "Use an image name to protect all versions, or a digest/tag for an exact version.",
        ),
        matchPlaceholder: text(
          "如 ^team/backend(@|:)",
          "e.g. ^team/backend(@|:)",
        ),
        protectPlaceholder: text(
          "如 ^team/backend:stable$",
          "e.g. ^team/backend:stable$",
        ),
        candidateName: text("镜像版本", "image versions"),
      };
    case "conan":
      return {
        ageLabel: text(
          "Recipe revision 保留天数",
          "Recipe revision retention days",
        ),
        ageHint: text(
          "Recipe revision 创建超过此天数后，才会进入清理候选。",
          "A recipe revision becomes eligible after this many days.",
        ),
        minimumLabel: text(
          "每个 reference 最少保留版本",
          "Minimum versions per reference",
        ),
        minimumHint: text(
          "按完整 Conan reference 分组，保护最新的 recipe revisions。",
          "Group by full Conan reference and protect the newest recipe revisions.",
        ),
        maximumLabel: text(
          "每个 reference 最多保留版本",
          "Maximum versions per reference",
        ),
        maximumHint: text(
          "0 表示不限制；清理 recipe revision 时会同时隐藏其二进制包。",
          "Use 0 for no limit. Cleaning a recipe revision also hides its binary packages.",
        ),
        matchLabel: text(
          "只清理匹配 reference",
          "Only clean matching references",
        ),
        matchHint: text(
          "可匹配完整 reference 或 reference#recipe-revision。",
          "Matches a full reference or reference#recipe-revision.",
        ),
        protectLabel: text("保护 Conan 版本", "Protect Conan versions"),
        protectHint: text(
          "匹配 reference 可保护全部 revisions，精确坐标只保护一个版本。",
          "A matching reference protects all revisions; an exact coordinate protects one version.",
        ),
        matchPlaceholder: text("如 ^openssl/3\\.", "e.g. ^openssl/3\\."),
        protectPlaceholder: text(
          "如 @release/stable(#|$)",
          "e.g. @release/stable(#|$)",
        ),
        candidateName: "Recipe revision",
      };
    case "raw":
      return {
        ageLabel: text("资产未更新保留天数", "Asset inactivity retention days"),
        ageHint: text(
          "路径资产超过此天数未更新后，才会进入清理候选。",
          "A path asset becomes eligible after it has not been updated for this many days.",
        ),
        minimumLabel: "",
        minimumHint: "",
        maximumLabel: "",
        maximumHint: "",
        matchLabel: text("只清理匹配路径", "Only clean matching paths"),
        matchHint: text(
          "可选 RE2 路径正则；留空表示匹配仓库内全部资产。",
          "Optional RE2 path regex. Leave empty to match every repository asset.",
        ),
        protectLabel: text("保护路径", "Protect paths"),
        protectHint: text(
          "匹配任一正则的路径永不进入清理候选。",
          "Paths matching any regex never become eligible.",
        ),
        matchPlaceholder: text(
          "如 ^releases/nightly/",
          "e.g. ^releases/nightly/",
        ),
        protectPlaceholder: text(
          "如 ^releases/stable/",
          "e.g. ^releases/stable/",
        ),
        candidateName: text("路径资产", "path assets"),
      };
    default:
      return {
        ageLabel: text("发布版本保留天数", "Release version retention days"),
        ageHint: text(
          "发布版本创建超过此天数后，才会进入清理候选。",
          "A release version becomes eligible after this many days.",
        ),
        minimumLabel: text(
          "每个模块最少保留版本",
          "Minimum versions per module",
        ),
        minimumHint: text(
          "按 groupId:artifactId 分组，始终保护最新的这些版本。",
          "Group by groupId:artifactId and always protect the newest versions.",
        ),
        maximumLabel: text(
          "每个模块最多保留版本",
          "Maximum versions per module",
        ),
        maximumHint: text(
          "0 表示不限制；超过上限的旧版本会进入候选。",
          "Use 0 for no limit. Older versions beyond the limit become eligible.",
        ),
        matchLabel: text("只清理匹配坐标", "Only clean matching coordinates"),
        matchHint: text(
          "可选 RE2 正则；留空表示匹配全部 Maven 坐标。",
          "Optional RE2 regex. Leave empty to match all Maven coordinates.",
        ),
        protectLabel: text("保护 Maven 坐标", "Protect Maven coordinates"),
        protectHint: text(
          "匹配任一正则的坐标永不进入清理候选。",
          "Coordinates matching any regex never become eligible.",
        ),
        matchPlaceholder: text("如 ^com\\.example:", "e.g. ^com\\.example:"),
        protectPlaceholder: text(
          "如 ^com\\.example:platform:",
          "e.g. ^com\\.example:platform:",
        ),
        candidateName: text("制品版本", "artifact versions"),
      };
  }
}

function retentionCandidateTypeLabel(
  versionType: RetentionDryRun["candidates"][number]["versionType"],
  format: Repository["format"],
  text: Localize,
) {
  if (versionType === "snapshot") return text("快照构建", "Snapshot build");
  if (versionType === "release") return text("发布版本", "Release version");
  if (versionType === "asset") return text("路径资产", "Path asset");
  return format === "oci"
    ? text("Manifest 版本", "Manifest version")
    : "Recipe revision";
}

export function RepositoryRetentionTab({ repo }: { repo: Repository }) {
  const { token } = useAuth();
  const { text } = usePreferences();
  const [policy, setPolicy] = useState<RetentionPolicy | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [enabled, setEnabled] = useState(false);
  const [keepDays, setKeepDays] = useState(0);
  const [snapshotKeepDays, setSnapshotKeepDays] = useState(0);
  const [minimumVersions, setMinimumVersions] = useState(0);
  const [maximumVersions, setMaximumVersions] = useState(0);
  const [coordinatePatterns, setCoordinatePatterns] = useState<string[]>([]);
  const [protectedPatterns, setProtectedPatterns] = useState<string[]>([]);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<unknown>(null);
  const [dryRun, setDryRun] = useState<RetentionDryRun | null>(null);
  const [dryRunning, setDryRunning] = useState(false);
  const [dryRunLoadingMore, setDryRunLoadingMore] = useState(false);
  const [executing, setExecuting] = useState(false);
  const [exporting, setExporting] = useState(false);
  const [notice, setNotice] = useState("");
  const isMaven = repo.format === "maven";
  const isRaw = repo.format === "raw";
  const copy = retentionFormatCopy(repo.format, text);

  const load = useCallback(async () => {
    setError(null);
    const { data, error: err } = await getRetentionPolicy({
      path: { repositoryId: repo.id },
    });
    if (err) {
      setError(err);
      return;
    }
    if (data) {
      setPolicy(data);
      setEnabled(data.enabled ?? false);
      setKeepDays(data.keepDays);
      setSnapshotKeepDays(data.snapshotKeepDays ?? data.keepDays);
      setMinimumVersions(data.minimumVersions);
      setMaximumVersions(data.maximumVersions ?? 0);
      setCoordinatePatterns(data.coordinatePatterns ?? []);
      setProtectedPatterns(data.protectedPatterns ?? []);
    }
  }, [repo.id]);

  useEffect(() => {
    void load();
  }, [load]);

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
        coordinatePatterns,
        protectedPatterns,
      },
      headers: { "If-Match": policy.version },
    });
    setSaving(false);
    if (err) {
      setSaveError(err);
      return;
    }
    setNotice(text("策略已保存", "Policy saved"));
    setDryRun(null);
    void load();
  };

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
          new Error(
            text(
              "试运行结果已过期或策略已变化，请重新试运行",
              "The dry-run result expired or the policy changed. Run it again.",
            ),
          ),
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
          new Error(
            text(
              "保留策略已变化，当前预览不再有效，请重新试运行",
              "The retention policy changed, so this preview is no longer valid. Run it again.",
            ),
          ),
        );
      } else {
        setSaveError(err);
      }
      return;
    }
    setNotice(
      text(
        "保留执行任务已提交，请在「生命周期任务」标签页查看进度",
        "The retention task was submitted. Track it on the Lifecycle jobs tab.",
      ),
    );
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
          problem?.message ??
            text("导出试运行结果失败", "Failed to export dry-run results"),
        );
      }
      downloadCsv(`${repo.name}-retention.csv`, await response.text());
    } catch (nextError) {
      setSaveError(nextError);
    } finally {
      setExporting(false);
    }
  };

  if (error !== null)
    return isNotFound(error) ? (
      <RepositoryFeatureUnavailable
        feature={text("保留策略", "Retention policy")}
      />
    ) : (
      <ErrorBanner error={error} onRetry={load} />
    );
  if (!policy) return <Loading />;

  if (
    repo.type !== "hosted" ||
    !["maven", "oci", "conan", "raw", "npm", "pypi"].includes(repo.format)
  ) {
    return (
      <RepositoryFeatureUnavailable
        feature={text("Hosted 仓库保留策略", "Hosted repository retention")}
      />
    );
  }

  const dryRunColumns: ColumnsType<RetentionDryRun["candidates"][number]> = [
    {
      title: text("清理单位", "Cleanup unit"),
      dataIndex: "coordinate",
      key: "coordinate",
      width: 360,
      render: (value: string) => (
        <span
          className="block max-w-md truncate font-mono text-xs text-zinc-200"
          title={value}
        >
          {value}
        </span>
      ),
    },
    {
      title: text("摘要", "Digest"),
      dataIndex: "digest",
      key: "digest",
      width: 180,
      render: (value: string) => (
        <span className="font-mono text-xs text-zinc-500">
          {shortDigest(value)}
        </span>
      ),
    },
    {
      title: text("类型", "Type"),
      key: "versionType",
      width: 140,
      render: (_, candidate) => (
        <span className="text-xs text-zinc-400">
          {retentionCandidateTypeLabel(
            candidate.versionType,
            candidate.format,
            text,
          )}
        </span>
      ),
    },
    {
      title: text("原因", "Reason"),
      key: "reasons",
      width: 280,
      render: (_, candidate) => (
        <span className="text-xs text-zinc-400">
          {candidate.reasons
            .map((reason) =>
              reason === "maximum_versions"
                ? text("超过版本上限", "Exceeded version limit")
                : candidate.versionType === "asset"
                  ? text(
                      `已 ${candidate.ageDays} 天未更新`,
                      `Not updated for ${candidate.ageDays} days`,
                    )
                  : text(
                      `已保留 ${candidate.ageDays} 天`,
                      `Retained for ${candidate.ageDays} days`,
                    ),
            )
            .join("、")}
        </span>
      ),
    },
    {
      title: isRaw
        ? text("最后更新时间", "Last updated")
        : text("创建时间", "Created"),
      dataIndex: "createdAt",
      key: "createdAt",
      width: 180,
      render: (value: string) => (
        <span className="whitespace-nowrap text-xs text-zinc-500">
          {formatDate(value)}
        </span>
      ),
    },
  ];

  return (
    <div className="space-y-6">
      {saveError !== null && <ErrorBanner error={saveError} />}
      {notice && <Alert type="success" showIcon title={notice} />}
      {isRaw && (
        <Alert
          type="info"
          showIcon
          title={text("Raw 按路径资产清理", "Raw cleanup by path asset")}
          description={text(
            "Raw 没有版本分组，因此不应用最少或最多版本数；期限按资产最后更新时间计算。",
            "Raw assets are not version-grouped, so minimum and maximum versions do not apply. Age is calculated from the last update.",
          )}
        />
      )}
      <div className="flex items-center justify-between border-b border-zinc-800 pb-4">
        <div>
          <div className="text-sm font-medium text-zinc-200">
            {text("自动清理", "Automatic cleanup")}
          </div>
          <div className="mt-1 text-xs text-zinc-500">
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
      </div>
      <div className="grid max-w-5xl grid-cols-2 gap-4 border-t border-zinc-800 pt-4">
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
        <Card>
          <CardHeader
            title={text(
              `试运行结果：已加载 ${dryRun.candidates.length} / 共 ${dryRun.totalCandidates} 个候选${copy.candidateName}（策略版本 ${dryRun.policyVersion}）`,
              `Dry-run results: loaded ${dryRun.candidates.length} of ${dryRun.totalCandidates} candidate ${copy.candidateName} (policy version ${dryRun.policyVersion})`,
            )}
            extra={
              dryRun.totalCandidates > 0 ? (
                <Tooltip
                  title={text(
                    "导出完整候选集，不受当前分页影响",
                    "Export the complete candidate set, independent of the current page",
                  )}
                >
                  <Button
                    size="small"
                    icon={<DownloadOutlined />}
                    loading={exporting}
                    onClick={() => void exportDryRun()}
                  >
                    {text("导出 CSV", "Export CSV")}
                  </Button>
                </Tooltip>
              ) : undefined
            }
          />
          <div className="flex flex-wrap items-center gap-x-8 gap-y-2 border-b border-zinc-800/80 px-4 py-3 text-xs text-zinc-400">
            <span>
              {text("按期限", "By age")}{" "}
              <strong className="font-medium text-zinc-200">
                {dryRun.summary.reasonCounts.age}
              </strong>
            </span>
            <span>
              {text("超过版本上限", "Exceeded version limit")}{" "}
              <strong className="font-medium text-zinc-200">
                {dryRun.summary.reasonCounts.maximumVersions}
              </strong>
            </span>
            <span>
              {text("类型：", "Types: ")}
              {[
                [
                  text("发布", "Release"),
                  dryRun.summary.versionTypeCounts.release,
                ],
                [
                  text("快照", "Snapshot"),
                  dryRun.summary.versionTypeCounts.snapshot,
                ],
                [
                  text("版本", "Version"),
                  dryRun.summary.versionTypeCounts.version,
                ],
                [text("资产", "Asset"), dryRun.summary.versionTypeCounts.asset],
              ]
                .filter(([, count]) => Number(count) > 0)
                .map(([label, count]) => `${label} ${count}`)
                .join(", ") || text("无", "None")}
            </span>
            <span>
              {text("最早候选", "Oldest candidate")}{" "}
              {formatDate(dryRun.summary.oldestCandidateAt)}
            </span>
          </div>
          {dryRun.candidates.length === 0 ? (
            <EmptyState
              title={text(
                `没有需要清理的${copy.candidateName}`,
                `No ${copy.candidateName} to clean`,
              )}
            />
          ) : (
            <Table<RetentionDryRun["candidates"][number]>
              className="ag-console-table"
              rowKey={(candidate) =>
                `${candidate.format}:${candidate.coordinate}:${candidate.digest}`
              }
              size="middle"
              dataSource={dryRun.candidates}
              columns={dryRunColumns}
              pagination={false}
              scroll={{ x: 1080 }}
            />
          )}
          {dryRun.nextPageToken && (
            <div className="flex justify-center border-t border-zinc-800 px-4 py-3">
              <Button onClick={loadMoreDryRun} loading={dryRunLoadingMore}>
                {text("加载更多候选", "Load more candidates")}
              </Button>
            </div>
          )}
        </Card>
      )}
    </div>
  );
}
