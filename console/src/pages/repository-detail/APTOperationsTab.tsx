import { useCallback, useEffect, useRef, useState } from "react";
import {
  Alert,
  Button,
  Input,
  InputNumber,
  Modal,
  Popconfirm,
  Table,
  Tabs,
  Upload,
} from "antd";
import { ReloadOutlined, UploadOutlined } from "@ant-design/icons";
import {
  applyAptLifecycle,
  exportAptRepositorySnapshot,
  getAptLifecycleState,
  previewAptLifecycle,
  pruneAptSnapshots,
  restoreAptRepositorySnapshot,
} from "../../client";
import type {
  AptLifecyclePackage,
  AptLifecyclePlan,
  AptLifecycleRequest,
  AptLifecycleState,
  AptRepositorySnapshot,
  AptSnapshotHistory,
  Repository,
} from "../../client";
import { Badge, StateBadge } from "../../components/Badge";
import { EmptyState, ErrorBanner, Loading } from "../../components/Feedback";
import { Field } from "../../components/Layout";
import { downloadBlob, sha256Receipt } from "../../lib/download";
import { formatDate, shortDigest } from "../../lib/format";
import { usePreferences } from "../../lib/preferences";
import { RepositoryDistributionTab } from "./RepositoryDistributionTab";

type Review = {
  request: AptLifecycleRequest;
  plan: AptLifecyclePlan;
  key: string;
};

export function APTOperationsTab({
  repo,
  canAdmin,
}: {
  repo: Repository;
  canAdmin: boolean;
}) {
  const { text } = usePreferences();
  const [suite, setSuite] = useState("stable");
  const [query, setQuery] = useState("stable");
  if (!canAdmin)
    return (
      <EmptyState
        title={text(
          "需要仓库管理权限",
          "Repository administrator access required",
        )}
      />
    );
  return (
    <div className="ag-page-stack ag-apt-operations">
      <Alert
        type="info"
        showIcon
        title={text(
          "APT 签名快照 · 操作预览",
          "APT signed snapshots · Operator preview",
        )}
        description={text(
          "成员变更会生成完整索引并签署新快照。应用需要已配置的签名服务；生产密钥托管尚未完成验收。",
          "Membership changes rebuild and sign a complete snapshot. Applying requires a configured signer; production key custody has not been accepted.",
        )}
      />
      <div className="ag-apt-toolbar">
        <Field
          label={text("发行套件", "Suite")}
          hint={text(
            "输入完整套件名，例如 stable 或 bookworm。",
            "Enter the exact suite, for example stable or bookworm.",
          )}
        >
          <Input.Search
            aria-label={text("发行套件", "Suite")}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onSearch={() => {
              if (query.trim()) setSuite(query.trim());
            }}
            enterButton={text("查看", "View")}
          />
        </Field>
      </div>
      <APTSuiteOperations
        key={`${repo.id}:${suite}`}
        repo={repo}
        suite={suite}
      />
    </div>
  );
}

function packageName(item: AptLifecyclePackage) {
  return `${item.revision.package} ${item.revision.version} · ${item.revision.architecture}`;
}

// The Gateway restores an archive only against the exact `sha256:<hex>` receipt
// the operator saved at backup time, so the form validates the shape up front
// instead of letting the server reject an obviously malformed value.
const APT_ARCHIVE_RECEIPT = /^sha256:[0-9a-f]{64}$/;

// The exporter accepts a visible or retired snapshot only; a pruned snapshot
// has lost its index references and the Gateway rejects it with 409.
function archiveExportable(item: AptSnapshotHistory) {
  return item.snapshot.state === "visible" || item.snapshot.state === "retired";
}

function APTArchiveOperations({
  repo,
  snapshots,
  disabled,
  onRestored,
}: {
  repo: Repository;
  snapshots: AptSnapshotHistory[];
  disabled: boolean;
  onRestored: () => Promise<void>;
}) {
  const { text } = usePreferences();
  const [exportingId, setExportingId] = useState("");
  const [file, setFile] = useState<File | null>(null);
  const [receipt, setReceipt] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [notice, setNotice] = useState("");
  const [savedArchive, setSavedArchive] = useState<{
    filename: string;
    digest: string | null;
  } | null>(null);

  const exportable = snapshots.filter(archiveExportable);
  const exportBusy = exportingId !== "" || disabled;
  const receiptValid = APT_ARCHIVE_RECEIPT.test(receipt.trim());
  const restoreReady = Boolean(file) && receiptValid && !busy && !disabled;

  const exportArchive = async (snapshot: AptRepositorySnapshot) => {
    setError(null);
    setNotice("");
    setSavedArchive(null);
    setExportingId(snapshot.id);
    try {
      const response = await exportAptRepositorySnapshot({
        path: { repositoryId: repo.id, snapshotId: snapshot.id },
      });
      if (response.error) throw response.error;
      if (!response.data)
        throw new Error(text("导出响应为空", "Export response is empty"));
      const blob = response.data as Blob;
      const filename = `apt-${snapshot.suite}-${snapshot.sequence}.tar`;
      downloadBlob(filename, blob);
      setSavedArchive({ filename, digest: await sha256Receipt(blob) });
    } catch (err) {
      setError(err);
    } finally {
      setExportingId("");
    }
  };

  const restoreArchive = async () => {
    if (!file || !receiptValid) return;
    setError(null);
    setNotice("");
    setBusy(true);
    try {
      const response = await restoreAptRepositorySnapshot({
        path: { repositoryId: repo.id },
        headers: { "X-Artifact-Archive-Digest": receipt.trim() },
        body: file,
      });
      if (response.error) throw response.error;
      if (!response.data)
        throw new Error(text("恢复响应为空", "Restore response is empty"));
      setFile(null);
      setReceipt("");
      setNotice(
        text(
          `已从归档恢复 ${response.data.suite} 快照 #${response.data.sequence}`,
          `Restored ${response.data.suite} snapshot #${response.data.sequence} from the archive`,
        ),
      );
      await onRestored();
    } catch (err) {
      setError(err);
    } finally {
      setBusy(false);
    }
  };

  return (
    <div className="ag-page-stack">
      <p>
        {text(
          "导出可携带的灾备归档，或在灾难后按独立保存的回执恢复同一套件的历史快照。归档只含公开签名与软件包字节，不含签名私钥。",
          "Export a portable disaster-recovery archive, or restore a historical snapshot of this suite after a disaster using the receipt stored independently. An archive carries public signatures and package bytes only, never private-key material.",
        )}
      </p>
      {error !== null && <ErrorBanner error={error} />}
      {notice && <Alert type="success" showIcon title={notice} />}
      {savedArchive && (
        <Alert
          type={savedArchive.digest ? "success" : "warning"}
          showIcon
          title={text("归档已开始下载", "Archive download started")}
          description={
            savedArchive.digest
              ? text(
                  `文件 ${savedArchive.filename}。请与回执 ${savedArchive.digest} 分开独立保存；恢复时必须原样提交该回执。`,
                  `File ${savedArchive.filename}. Store it separately from the receipt ${savedArchive.digest}; restore requires that exact receipt.`,
                )
              : text(
                  `文件 ${savedArchive.filename}。当前浏览器无法计算摘要，请对归档运行 gateway apt-snapshot verify，并独立保存它输出的 sha256 回执。`,
                  `File ${savedArchive.filename}. This browser cannot compute the digest; run gateway apt-snapshot verify on the archive and store the sha256 receipt it prints.`,
                )
          }
        />
      )}
      <section
        aria-label={text("导出灾备归档", "Export disaster-recovery archive")}
      >
        <h4>{text("导出灾备归档", "Export disaster-recovery archive")}</h4>
        <Table
          className="ag-console-table"
          rowKey={(item) => item.snapshot.id}
          size="small"
          dataSource={exportable}
          scroll={{ x: 720 }}
          pagination={{ pageSize: 10, hideOnSinglePage: true }}
          columns={[
            {
              title: text("快照", "Snapshot"),
              render: (_, item) => <>#{item.snapshot.sequence}</>,
            },
            {
              title: text("状态", "State"),
              render: (_, item) => <StateBadge state={item.snapshot.state} />,
            },
            {
              title: text("发布时间", "Published"),
              render: (_, item) => formatDate(item.snapshot.publishedAt),
            },
            {
              title: text("操作", "Action"),
              render: (_, item) => (
                <Button
                  size="small"
                  loading={exportingId === item.snapshot.id}
                  disabled={exportBusy}
                  onClick={() => void exportArchive(item.snapshot)}
                >
                  {text("导出归档", "Export archive")}
                </Button>
              ),
            },
          ]}
        />
      </section>
      <section aria-label={text("从归档恢复", "Restore from archive")}>
        <h4>{text("从归档恢复", "Restore from archive")}</h4>
        <p>
          {text(
            "仅接受属于本仓库的 Gateway v1 归档。恢复会先校验备份回执、两个 OpenPGP 签名与规范索引；同一归档重复恢复是幂等的，且不会重新签名或提升已退役快照。",
            "Only a Gateway v1 archive belonging to this repository is accepted. Restore first verifies the backup receipt, both OpenPGP signatures, and canonical indexes; replaying the same archive is idempotent and never re-signs or promotes a retired snapshot.",
          )}
        </p>
        <div className="ag-apt-toolbar">
          <Upload
            accept=".tar"
            maxCount={1}
            showUploadList={false}
            disabled={busy}
            beforeUpload={(candidate) => {
              setFile(candidate);
              setError(null);
              setNotice("");
              return false;
            }}
          >
            <Button icon={<UploadOutlined aria-hidden />} disabled={busy}>
              {text("选择归档文件", "Choose archive file")}
            </Button>
          </Upload>
          {file && (
            <>
              <code>{file.name}</code>
              <Button
                type="link"
                size="small"
                disabled={busy}
                onClick={() => setFile(null)}
              >
                {text("移除", "Remove")}
              </Button>
            </>
          )}
        </div>
        <Field
          label={text("备份回执 (sha256)", "Backup receipt (sha256)")}
          hint={text(
            "必须是备份时独立保存的 sha256:<64 位小写十六进制> 回执。请勿根据本次上传的文件现算摘要，服务端会校验两者一致。",
            "Must be the sha256:<64 lowercase hex> receipt stored independently at backup time. Do not derive it from this upload; the server verifies they match.",
          )}
        >
          <Input
            aria-label={text("备份回执 (sha256)", "Backup receipt (sha256)")}
            className="font-mono"
            value={receipt}
            placeholder="sha256:"
            disabled={busy}
            onChange={(event) => setReceipt(event.target.value)}
          />
        </Field>
        <Popconfirm
          title={text("从此归档恢复？", "Restore from this archive?")}
          description={text(
            "恢复会写入软件包与已签名索引。目标套件已有冲突快照或坐标时会被拒绝。",
            "Restore writes packages and signed indexes. A conflicting snapshot or coordinate in the target suite is rejected.",
          )}
          okText={text("恢复归档", "Restore archive")}
          cancelText={text("取消", "Cancel")}
          disabled={!restoreReady}
          onConfirm={() => void restoreArchive()}
        >
          <Button type="primary" loading={busy} disabled={!restoreReady}>
            {text("恢复归档", "Restore archive")}
          </Button>
        </Popconfirm>
      </section>
    </div>
  );
}

function APTSuiteOperations({
  repo,
  suite,
}: {
  repo: Repository;
  suite: string;
}) {
  const { text } = usePreferences();
  const [state, setState] = useState<AptLifecycleState | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [actionError, setActionError] = useState<unknown>(null);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState("");
  const [selected, setSelected] = useState<React.Key[]>([]);
  const [keepLatest, setKeepLatest] = useState(1);
  const [olderThanDays, setOlderThanDays] = useState(30);
  const [review, setReview] = useState<Review | null>(null);
  const alive = useRef(true);
  const generation = useRef(0);
  const inFlight = useRef(false);
  const current = state?.snapshots.find(
    (item) => item.snapshot.state === "visible",
  )?.snapshot;

  const load = useCallback(async () => {
    const version = ++generation.current;
    setLoading(true);
    setError(null);
    setReview(null);
    setSelected([]);
    try {
      const response = await getAptLifecycleState({
        path: { repositoryId: repo.id },
        query: { suite },
      });
      if (!alive.current || version !== generation.current) return;
      if (response.error || !response.data)
        throw (
          response.error ??
          new Error(text("快照响应为空", "Snapshot response is empty"))
        );
      setState(response.data);
    } catch (err) {
      if (alive.current && version === generation.current) setError(err);
    } finally {
      if (alive.current && version === generation.current) setLoading(false);
    }
  }, [repo.id, suite, text]);

  useEffect(() => {
    alive.current = true;
    const requestGeneration = generation;
    void load();
    return () => {
      alive.current = false;
      requestGeneration.current++;
    };
  }, [load]);

  const run = async (action: () => Promise<void>) => {
    if (inFlight.current) return;
    inFlight.current = true;
    setBusy(true);
    setActionError(null);
    setNotice("");
    try {
      await action();
    } catch (err) {
      if (alive.current) setActionError(err);
    } finally {
      inFlight.current = false;
      if (alive.current) setBusy(false);
    }
  };

  const preview = (request: AptLifecycleRequest) =>
    run(async () => {
      const version = generation.current;
      setReview(null);
      const response = await previewAptLifecycle({
        path: { repositoryId: repo.id },
        body: request,
      });
      if (!alive.current || version !== generation.current) return;
      if (response.error || !response.data)
        throw (
          response.error ??
          new Error(text("预览响应为空", "Preview response is empty"))
        );
      if (response.data.expectedSnapshotId !== request.expectedSnapshotId)
        throw new Error(
          text(
            "快照已变化，请刷新后重新预览。",
            "The snapshot changed. Refresh and preview again.",
          ),
        );
      setReview({
        request: {
          ...request,
          ...(request.action === "retention"
            ? { publicationSessionIds: response.data.removeSessionIds }
            : {}),
        },
        plan: response.data,
        key: crypto.randomUUID(),
      });
    });

  const apply = () =>
    run(async () => {
      if (!review) return;
      const response = await applyAptLifecycle({
        path: { repositoryId: repo.id },
        body: review.request,
        headers: { "Idempotency-Key": review.key },
      });
      if (!alive.current) return;
      if (response.error) {
        if (
          response.response?.status === 409 ||
          response.error.status === 409
        ) {
          setReview(null);
          throw new Error(
            text(
              "快照或候选集已变化，或成员受治理限制。请刷新并重新预览；未自动重试。",
              "The snapshot or candidates changed, or governance blocked a member. Refresh and preview again; no automatic retry was made.",
            ),
          );
        }
        throw response.error;
      }
      if (!response.data)
        throw new Error(
          text(
            "未收到应用结果，可使用相同请求重试。",
            "No apply result was received. Retry the same request.",
          ),
        );
      setReview(null);
      setNotice(
        text(
          `已发布 ${suite} 快照 #${response.data.sequence}`,
          `Published ${suite} snapshot #${response.data.sequence}`,
        ),
      );
      await load();
    });

  const prune = (id: string) =>
    run(async () => {
      const response = await pruneAptSnapshots({
        path: { repositoryId: repo.id },
        body: { snapshotIds: [id] },
      });
      if (!alive.current) return;
      if (response.error) throw response.error;
      setNotice(
        text(
          "快照引用已清理；无引用对象由后台异步回收。",
          "Snapshot references pruned; unreferenced objects are reclaimed asynchronously.",
        ),
      );
      await load();
    });

  const request = (
    action: AptLifecycleRequest["action"],
  ): AptLifecycleRequest => ({
    suite,
    expectedSnapshotId: current?.id ?? "",
    action,
  });
  const disabled = busy || loading || error !== null;
  const packageColumns = [
    {
      title: text("软件包", "Package"),
      key: "package",
      render: (_: unknown, item: AptLifecyclePackage) => (
        <div>
          <strong>{packageName(item)}</strong>
          <div className="ag-apt-secondary">{item.component}</div>
        </div>
      ),
    },
    {
      title: text("规范路径 / SHA-256", "Canonical path / SHA-256"),
      key: "identity",
      render: (_: unknown, item: AptLifecyclePackage) => (
        <div className="ag-apt-identity">
          <code>{item.poolPath}</code>
          <code title={item.revision.digest}>
            {shortDigest(item.revision.digest)}
          </code>
        </div>
      ),
    },
  ];
  const removed =
    state?.packages.filter((item) =>
      review?.plan.removeSessionIds.includes(item.publicationSessionId),
    ) ?? [];
  const restored =
    state?.deletions.filter((item) =>
      review?.plan.restoreIds.includes(item.id),
    ) ?? [];
  const changedCount =
    (review?.plan.removeSessionIds.length ?? 0) +
    (review?.plan.restoreIds.length ?? 0);

  return (
    <div className="ag-page-stack">
      <div className="ag-apt-heading">
        <h3>{suite}</h3>
        <Button
          icon={<ReloadOutlined aria-hidden />}
          loading={loading}
          disabled={busy}
          onClick={() => void load()}
        >
          {text("刷新快照", "Refresh snapshots")}
        </Button>
      </div>
      {error !== null && <ErrorBanner error={error} onRetry={load} />}
      {actionError !== null && !review && <ErrorBanner error={actionError} />}
      {notice && <Alert type="success" showIcon title={notice} />}
      {!state ? (
        loading ? (
          <Loading />
        ) : null
      ) : (
        <>
          {current ? (
            <div
              className="ag-apt-current"
              aria-label={text("当前签名快照", "Current signed snapshot")}
            >
              <div className="ag-apt-heading">
                <strong>
                  {text(
                    `当前快照 #${current.sequence}`,
                    `Current snapshot #${current.sequence}`,
                  )}
                </strong>
                <Badge tone="success">{text("可见", "Visible")}</Badge>
              </div>
              <dl className="ag-apt-evidence">
                <div>
                  <dt>{text("快照 ID", "Snapshot ID")}</dt>
                  <dd>
                    <code>{current.id}</code>
                  </dd>
                </div>
                <div>
                  <dt>{text("签名指纹", "Signing fingerprint")}</dt>
                  <dd>
                    <code>{current.keyFingerprint}</code>
                  </dd>
                </div>
                <div>
                  <dt>{text("签名服务", "Signer")}</dt>
                  <dd>{current.signerIdentity}</dd>
                </div>
                <div>
                  <dt>{text("发布时间", "Published")}</dt>
                  <dd>{formatDate(current.publishedAt)}</dd>
                </div>
              </dl>
            </div>
          ) : (
            <EmptyState
              compact
              title={text(
                "此套件暂无可见快照",
                "No visible snapshot in this suite",
              )}
              hint={text(
                "请核对套件名，或通过发布 API 创建首个签名快照。",
                "Check the suite name, or create the first signed snapshot through the publication API.",
              )}
            />
          )}
          <Tabs
            destroyOnHidden
            items={[
              {
                key: "packages",
                label: text(
                  `软件包 (${state.packages.length})`,
                  `Packages (${state.packages.length})`,
                ),
                children: (
                  <div className="ag-page-stack">
                    <div className="ag-apt-heading">
                      <p>
                        {text(
                          "选择软件包，预览从当前套件移除的影响。",
                          "Select packages to preview removal from the current suite.",
                        )}
                      </p>
                      <Button
                        danger
                        disabled={disabled || !current || selected.length === 0}
                        onClick={() =>
                          void preview({
                            ...request("delete"),
                            publicationSessionIds: selected.map(String),
                          })
                        }
                      >
                        {text("预览删除", "Preview deletion")}
                      </Button>
                    </div>
                    <Table
                      className="ag-console-table"
                      rowKey="publicationSessionId"
                      size="small"
                      dataSource={state.packages}
                      columns={packageColumns}
                      scroll={{ x: 640 }}
                      pagination={{ pageSize: 10, hideOnSinglePage: true }}
                      rowSelection={{
                        selectedRowKeys: selected,
                        onChange: setSelected,
                        getCheckboxProps: () => ({
                          disabled: disabled || !current,
                        }),
                      }}
                    />
                    <section
                      className="ag-apt-retention"
                      aria-label={text("保留预览", "Retention preview")}
                    >
                      <h4>{text("按上传时间保留", "Retain by upload time")}</h4>
                      <p>
                        {text(
                          "按组件、包名和架构分组；同时超过保留数量和天数才会入选，不按 Debian 版本排序。",
                          "Grouped by component, package and architecture. Only uploads beyond both limits qualify; this is not Debian version ordering.",
                        )}
                      </p>
                      <div className="ag-apt-toolbar">
                        <Field
                          label={text("保留最新数量", "Keep latest uploads")}
                        >
                          <InputNumber
                            aria-label={text(
                              "保留最新数量",
                              "Keep latest uploads",
                            )}
                            min={1}
                            precision={0}
                            value={keepLatest}
                            disabled={disabled}
                            onChange={(v) => setKeepLatest(v ?? 1)}
                          />
                        </Field>
                        <Field label={text("早于天数", "Older than days")}>
                          <InputNumber
                            aria-label={text("早于天数", "Older than days")}
                            min={1}
                            precision={0}
                            value={olderThanDays}
                            disabled={disabled}
                            onChange={(v) => setOlderThanDays(v ?? 30)}
                          />
                        </Field>
                        <Button
                          disabled={disabled || !current}
                          onClick={() =>
                            void preview({
                              ...request("retention"),
                              keepLatest,
                              olderThanDays,
                            })
                          }
                        >
                          {text("预览保留清理", "Preview retention")}
                        </Button>
                      </div>
                    </section>
                  </div>
                ),
              },
              {
                key: "recovery",
                label: text(
                  `恢复记录 (${state.deletions.length})`,
                  `Recovery (${state.deletions.length})`,
                ),
                children: (
                  <Table
                    className="ag-console-table"
                    rowKey="id"
                    size="small"
                    dataSource={state.deletions}
                    scroll={{ x: 640 }}
                    pagination={{ pageSize: 10, hideOnSinglePage: true }}
                    columns={[
                      {
                        title: text("软件包", "Package"),
                        render: (_, item) => packageName(item.package),
                      },
                      {
                        title: text("状态", "State"),
                        render: (_, item) => <StateBadge state={item.state} />,
                      },
                      {
                        title: text("恢复截止", "Recover until"),
                        render: (_, item) => formatDate(item.restoreUntil),
                      },
                      {
                        title: text("操作", "Action"),
                        render: (_, item) => (
                          <Button
                            size="small"
                            disabled={
                              disabled ||
                              !current ||
                              item.state !== "recoverable" ||
                              Date.parse(item.restoreUntil) <= Date.now()
                            }
                            onClick={() =>
                              void preview({
                                ...request("restore"),
                                deletionIds: [item.id],
                              })
                            }
                          >
                            {text("预览恢复", "Preview recovery")}
                          </Button>
                        ),
                      },
                    ]}
                  />
                ),
              },
              {
                key: "history",
                label: text(
                  `快照历史 (${state.snapshots.length})`,
                  `Snapshots (${state.snapshots.length})`,
                ),
                children: (
                  <Table
                    className="ag-console-table"
                    rowKey={(item) => item.snapshot.id}
                    size="small"
                    dataSource={state.snapshots}
                    scroll={{ x: 720 }}
                    pagination={{ pageSize: 10, hideOnSinglePage: true }}
                    columns={[
                      {
                        title: text("快照", "Snapshot"),
                        render: (_, item) => (
                          <div>
                            #{item.snapshot.sequence}
                            <div className="ag-apt-identity">
                              <code>{item.snapshot.id}</code>
                            </div>
                          </div>
                        ),
                      },
                      {
                        title: text("状态", "State"),
                        render: (_, item) => (
                          <StateBadge state={item.snapshot.state} />
                        ),
                      },
                      {
                        title: text("签名指纹", "Signing fingerprint"),
                        render: (_, item) => (
                          <code className="ag-apt-wrap">
                            {item.snapshot.keyFingerprint}
                          </code>
                        ),
                      },
                      {
                        title: text("最早清理时间", "Prunable after"),
                        render: (_, item) => formatDate(item.prunableAfter),
                      },
                      {
                        title: text("操作", "Action"),
                        render: (_, item) => (
                          <Popconfirm
                            title={text(
                              "清理此退役快照的引用？",
                              "Prune this retired snapshot's references?",
                            )}
                            description={text(
                              "使用此旧索引的客户端将无法继续读取。共享对象仍受其他引用保护。",
                              "Clients using this old index can no longer read it. Other references still protect shared objects.",
                            )}
                            onConfirm={() => prune(item.snapshot.id)}
                            okText={text("清理引用", "Prune references")}
                            cancelText={text("取消", "Cancel")}
                          >
                            <Button
                              size="small"
                              danger
                              disabled={
                                disabled ||
                                item.snapshot.state !== "retired" ||
                                !item.prunableAfter ||
                                Date.parse(item.prunableAfter) > Date.now()
                              }
                            >
                              {text("清理引用", "Prune references")}
                            </Button>
                          </Popconfirm>
                        ),
                      },
                    ]}
                  />
                ),
              },
              {
                key: "archive",
                label: text("灾备归档", "Disaster recovery"),
                children: (
                  <APTArchiveOperations
                    repo={repo}
                    snapshots={state.snapshots}
                    disabled={disabled}
                    onRestored={load}
                  />
                ),
              },
              {
                key: "distribution",
                label: text("晋升 / 复制", "Promote / replicate"),
                children: (
                  <RepositoryDistributionTab
                    key={current?.id ?? "empty"}
                    repo={repo}
                    aptPackages={state.packages}
                    disabled={disabled}
                  />
                ),
              },
            ]}
          />
        </>
      )}
      <Modal
        open={!!review}
        title={text("审阅快照变更", "Review snapshot change")}
        onCancel={() => {
          if (!busy) {
            setReview(null);
            setActionError(null);
          }
        }}
        closable={!busy}
        mask={{ closable: !busy }}
        keyboard={!busy}
        footer={
          <>
            <Button
              disabled={busy}
              onClick={() => {
                setReview(null);
                setActionError(null);
              }}
            >
              {text("取消", "Cancel")}
            </Button>
            <Button
              color={
                review?.request.action === "restore" ? "primary" : "danger"
              }
              variant="outlined"
              loading={busy}
              disabled={!review || changedCount === 0}
              onClick={() => void apply()}
            >
              {text("应用并签署新快照", "Apply and sign new snapshot")}
            </Button>
          </>
        }
      >
        {review && (
          <div className="ag-page-stack">
            {actionError !== null && <ErrorBanner error={actionError} />}
            <p className="ag-apt-wrap">
              {text("基于快照", "Based on snapshot")}{" "}
              <code>{review.plan.expectedSnapshotId}</code>
            </p>
            <Alert
              type="warning"
              showIcon
              title={text(
                `移除 ${review.plan.removeSessionIds.length} 个，恢复 ${review.plan.restoreIds.length} 个，完成后 ${review.plan.remainingPackages} 个软件包`,
                `Remove ${review.plan.removeSessionIds.length}, restore ${review.plan.restoreIds.length}; ${review.plan.remainingPackages} packages remain`,
              )}
              description={text(
                `删除后可恢复 ${review.plan.recoveryDays} 天。提交会生成新索引和签名，当前快照发生变化时必须重新预览。`,
                `Deleted packages can be recovered for ${review.plan.recoveryDays} days. Submission creates new indices and signatures; a changed current snapshot requires a fresh preview.`,
              )}
            />
            {changedCount === 0 ? (
              <p>
                {text(
                  "没有符合条件的变更，无需应用。",
                  "No matching changes; there is nothing to apply.",
                )}
              </p>
            ) : (
              <ul className="ag-apt-change-list">
                {removed.map((item) => (
                  <li key={item.publicationSessionId}>
                    {text("移除", "Remove")} · {packageName(item)}{" "}
                    <code>{item.poolPath}</code>
                  </li>
                ))}
                {restored.map((item) => (
                  <li key={item.id}>
                    {text("恢复", "Restore")} · {packageName(item.package)}{" "}
                    <code>{item.package.poolPath}</code>
                  </li>
                ))}
              </ul>
            )}
          </div>
        )}
      </Modal>
    </div>
  );
}
