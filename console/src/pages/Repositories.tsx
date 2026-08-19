import { useCallback, useEffect, useState } from "react";
import {
  ClearOutlined,
  DeleteOutlined,
  PlusOutlined,
  SearchOutlined,
} from "@ant-design/icons";
import { Button, Input, Segmented, Select, Space, Table } from "antd";
import type { ColumnsType } from "antd/es/table";
import { Link } from "react-router-dom";
import {
  listRepositories,
  createRepository,
  deleteRepository,
  listRepositoryCapacities,
} from "../client";
import type { Repository, Format, FormatProfile } from "../client";
import { PageHeader, Card, Pagination, Field } from "../components/Layout";
import { Loading, ErrorBanner, EmptyState } from "../components/Feedback";
import { FormatBadge, StateBadge, Badge } from "../components/Badge";
import { Modal, ConfirmDialog, useDisclosure } from "../components/Modal";
import { formatBytes, formatNumber } from "../lib/format";
import {
  CopyableValue,
  FilterBar,
  FilterField,
  MetricStrip,
} from "../components/ConsolePrimitives";
import { usePreferences } from "../lib/preferences";
import {
  loadFormatProfiles,
  repositoryFormats,
  repositoryTypes,
} from "../lib/formatProfiles";

function CreateRepositoryDialog({
  profiles,
  onCreated,
}: {
  profiles: FormatProfile[];
  onCreated: () => void;
}) {
  const { text } = usePreferences();
  const dialog = useDisclosure();
  const [name, setName] = useState("");
  const [format, setFormat] = useState<Format>("oci");
  const [type, setType] = useState<"hosted" | "proxy">("hosted");
  const [endpoint, setEndpoint] = useState("");
  const [allowedHosts, setAllowedHosts] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);

  const formats = repositoryFormats(profiles, type);
  const selectedFormat = formats.includes(format)
    ? format
    : (formats[0] ?? format);
  const availableRepositoryTypes = repositoryTypes(profiles);

  const needsHosts =
    type === "proxy" &&
    (selectedFormat === "raw" ||
      selectedFormat === "conan" ||
      selectedFormat === "npm" ||
      selectedFormat === "pypi" ||
      selectedFormat === "go" ||
      selectedFormat === "apt");

  const submit = async () => {
    setBusy(true);
    setError(null);
    const hosts = allowedHosts
      .split(",")
      .map((h) => h.trim())
      .filter(Boolean);
    const { data, error: err } = await createRepository({
      body: {
        name: name.trim(),
        format: selectedFormat,
        type,
        ...(type === "proxy"
          ? { endpoint: endpoint.trim(), allowedHosts: hosts }
          : {}),
      },
      headers: { "Idempotency-Key": crypto.randomUUID() },
    });
    setBusy(false);
    if (err) {
      setError(err);
      return;
    }
    if (data) {
      dialog.hide();
      setName("");
      setEndpoint("");
      setAllowedHosts("");
      setType("hosted");
      onCreated();
    }
  };

  const endpointPlaceholder: Record<string, string> = {
    oci: "https://registry-1.docker.io",
    maven: "https://repo1.maven.org/maven2",
    raw: "https://raw.githubusercontent.com",
    conan: "https://center.conan.io/v2",
    npm: "https://registry.npmjs.org",
    pypi: "https://pypi.org",
    go: "https://proxy.golang.org",
    apt: "https://deb.debian.org/debian",
  };

  return (
    <>
      <Button
        type="primary"
        icon={<PlusOutlined />}
        disabled={profiles.length === 0}
        onClick={() => {
          setError(null);
          dialog.show();
        }}
      >
        {text("新建仓库", "New repository")}
      </Button>
      <Modal
        open={dialog.open}
        title={text("新建仓库", "New repository")}
        onClose={dialog.hide}
        footer={
          <Space>
            <Button onClick={dialog.hide} disabled={busy}>
              {text("取消", "Cancel")}
            </Button>
            <Button
              type="primary"
              onClick={submit}
              loading={busy}
              disabled={
                busy ||
                profiles.length === 0 ||
                !name.trim() ||
                (type === "proxy" &&
                  (!endpoint.trim() || (needsHosts && !allowedHosts.trim())))
              }
            >
              {text("创建", "Create")}
            </Button>
          </Space>
        }
      >
        <div className="space-y-4">
          {error !== null && <ErrorBanner error={error} />}
          <Field label={text("类型", "Type")} group>
            <Segmented<"hosted" | "proxy">
              block
              value={type}
              onChange={(nextType) => {
                setType(nextType);
                const nextFormats = repositoryFormats(profiles, nextType);
                if (!nextFormats.includes(format) && nextFormats[0]) {
                  setFormat(nextFormats[0]);
                }
              }}
              options={availableRepositoryTypes.map((repositoryType) => ({
                value: repositoryType,
                label:
                  repositoryType === "hosted"
                    ? text("托管 (hosted)", "Hosted")
                    : text("代理 (proxy)", "Proxy"),
              }))}
            />
            <span className="mt-1 block text-xs text-zinc-600">
              {type === "hosted"
                ? text("自己托管制品，可推送", "Host and publish artifacts")
                : text("从上游仓库拉取并缓存", "Fetch and cache from upstream")}
            </span>
          </Field>
          <Field
            label={text("仓库名称", "Repository name")}
            hint={text(
              "小写字母、数字与连字符，例如 team-images",
              "Lowercase letters, numbers, and hyphens, for example team-images",
            )}
          >
            <Input
              className="font-mono"
              placeholder="my-repository"
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
          </Field>
          <Field label={text("格式", "Format")} group>
            <Select<Format>
              className="w-full font-mono"
              showSearch={{ optionFilterProp: "label" }}
              value={selectedFormat}
              onChange={setFormat}
              options={formats.map((candidate) => ({
                value: candidate,
                label: candidate,
              }))}
            />
          </Field>
          {type === "proxy" && (
            <>
              <Field
                label={text("上游地址 endpoint", "Upstream endpoint")}
                hint={text("代理拉取的外部仓库地址", "External proxy source")}
              >
                <Input
                  className="font-mono text-xs"
                  placeholder={
                    endpointPlaceholder[selectedFormat] ??
                    "https://registry.example.com"
                  }
                  value={endpoint}
                  onChange={(e) => setEndpoint(e.target.value)}
                />
              </Field>
              <Field
                label={text(
                  `允许主机 allowedHosts${needsHosts ? "（必填）" : "（可选）"}`,
                  `Allowed hosts${needsHosts ? " (required)" : " (optional)"}`,
                )}
                hint={text(
                  "逗号分隔的主机名；Raw、Conan、npm、PyPI、Go 和 APT 代理必填。PyPI 通常还需允许 files.pythonhosted.org",
                  "Comma-separated hostnames; required for Raw, Conan, npm, PyPI, Go, and APT proxies. PyPI normally also requires files.pythonhosted.org",
                )}
              >
                <Input
                  className="font-mono text-xs"
                  placeholder={
                    selectedFormat === "npm"
                      ? "registry.npmjs.org"
                      : selectedFormat === "pypi"
                        ? "files.pythonhosted.org"
                        : selectedFormat === "go"
                          ? "proxy.golang.org"
                          : selectedFormat === "apt"
                            ? "deb.debian.org"
                            : "repo1.maven.org"
                  }
                  value={allowedHosts}
                  onChange={(e) => setAllowedHosts(e.target.value)}
                />
              </Field>
            </>
          )}
        </div>
      </Modal>
    </>
  );
}

export function RepositoriesPage() {
  const { locale, text } = usePreferences();
  const [items, setItems] = useState<Repository[]>([]);
  const [formatProfiles, setFormatProfiles] = useState<FormatProfile[]>([]);
  const [nextToken, setNextToken] = useState<string | undefined>();
  const [loading, setLoading] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [filter, setFilter] = useState("");
  const [formatFilter, setFormatFilter] = useState<Format | "all">("all");
  type RepositoryStateFilter = Repository["state"] | "all" | "operational";
  const [stateFilter, setStateFilter] =
    useState<RepositoryStateFilter>("operational");
  const [capacities, setCapacities] = useState<
    Record<
      string,
      { usedBytes: number; objectCount: number; quotaBytes: number }
    >
  >({});
  const [toDelete, setToDelete] = useState<Repository | null>(null);
  const [deleting, setDeleting] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const [repositoryResult, capacityResult, profiles] = await Promise.all([
        listRepositories({ query: { pageSize: 100 } }),
        listRepositoryCapacities(),
        loadFormatProfiles(),
      ]);
      const { data, error: err } = repositoryResult;
      if (err) {
        setError(err);
        return;
      }
      const nextItems = data?.items ?? [];
      setItems(nextItems);
      setNextToken(data?.nextPageToken);
      setFormatProfiles(profiles);
      setCapacities(
        Object.fromEntries(
          (capacityResult.data ?? []).map((capacity) => [
            capacity.repositoryId,
            capacity,
          ]),
        ),
      );
    } catch (loadError) {
      setError(loadError);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const loadMore = async () => {
    if (!nextToken) return;
    setLoadingMore(true);
    const { data, error: err } = await listRepositories({
      query: { pageSize: 100, pageToken: nextToken },
    });
    if (err) {
      setLoadingMore(false);
      setError(err);
      return;
    }
    const nextItems = data?.items ?? [];
    setItems((prev) => [...prev, ...nextItems]);
    setNextToken(data?.nextPageToken);
    setLoadingMore(false);
  };

  const confirmDelete = async () => {
    if (!toDelete) return;
    if (toDelete.state !== "active") {
      setToDelete(null);
      return;
    }
    setDeleting(true);
    const { error: err } = await deleteRepository({
      path: { repositoryId: toDelete.id },
    });
    setDeleting(false);
    if (!err) {
      setToDelete(null);
      void load();
    } else {
      setError(err);
    }
  };

  const q = filter.toLowerCase();
  const visible = items
    .filter(
      (r) =>
        !q ||
        r.name.toLowerCase().includes(q) ||
        r.format.includes(q) ||
        (r.type ?? "hosted").includes(q),
    )
    .filter((r) => formatFilter === "all" || r.format === formatFilter)
    .filter((r) =>
      stateFilter === "operational"
        ? r.state !== "deleted"
        : stateFilter === "all" || r.state === stateFilter,
    );

  const activeCount = items.filter((r) => r.state === "active").length;
  const operationalCount = items.filter((r) => r.state !== "deleted").length;
  const proxyCount = items.filter(
    (r) => r.type === "proxy" && r.state !== "deleted",
  ).length;
  const deletedCount = items.filter((r) => r.state === "deleted").length;
  const totalUsedBytes = Object.values(capacities).reduce(
    (sum, value) => sum + value.usedBytes,
    0,
  );
  const columns: ColumnsType<Repository> = [
    {
      title: text("名称", "Name"),
      dataIndex: "name",
      key: "name",
      fixed: "left",
      width: 190,
      sorter: (left, right) => left.name.localeCompare(right.name),
      render: (name: string, repository) => (
        <Link
          to={`/repositories/${repository.id}`}
          className="font-medium text-zinc-100 hover:text-cyan-300"
        >
          {name}
        </Link>
      ),
    },
    {
      title: text("类型", "Type"),
      dataIndex: "type",
      key: "type",
      width: 105,
      render: (type: Repository["type"]) => (
        <Badge tone={type === "proxy" ? "amber" : "cyan"}>
          {type === "proxy" ? "proxy" : "hosted"}
        </Badge>
      ),
    },
    {
      title: text("格式", "Format"),
      dataIndex: "format",
      key: "format",
      width: 105,
      render: (format: Repository["format"]) => <FormatBadge format={format} />,
    },
    {
      title: text("状态", "Status"),
      dataIndex: "state",
      key: "state",
      width: 120,
      render: (state: Repository["state"]) => <StateBadge state={state} />,
    },
    {
      title: text("容量", "Capacity"),
      key: "capacity",
      width: 140,
      defaultSortOrder: "descend",
      sorter: (left, right) =>
        (capacities[left.id]?.usedBytes ?? 0) -
        (capacities[right.id]?.usedBytes ?? 0),
      render: (_value, repository) => {
        const capacity = capacities[repository.id];
        return capacity ? (
          <div>
            <div className="font-mono text-xs text-zinc-300">
              {formatBytes(capacity.usedBytes)}
            </div>
            {capacity.quotaBytes > 0 && (
              <div className="mt-1 text-xs text-zinc-600">
                / {formatBytes(capacity.quotaBytes)}
              </div>
            )}
          </div>
        ) : (
          <span className="text-xs text-zinc-600">—</span>
        );
      },
    },
    {
      title: text("配置", "Configuration"),
      key: "configuration",
      width: 230,
      ellipsis: true,
      render: (_value, repository) => (
        <span
          className="font-mono text-xs text-zinc-500"
          title={
            repository.type === "proxy"
              ? repository.endpoint
              : `v${repository.version}`
          }
        >
          {repository.type === "proxy"
            ? repository.endpoint
            : `v${repository.version}`}
        </span>
      ),
    },
    {
      title: "ID",
      dataIndex: "id",
      key: "id",
      width: 145,
      render: (id: string) => (
        <CopyableValue
          value={id}
          label={`${id.slice(0, 8)}…`}
          className="text-xs text-zinc-500"
        />
      ),
    },
    {
      title: text("操作", "Actions"),
      key: "actions",
      fixed: "right",
      width: 82,
      align: "right",
      render: (_value, repository) =>
        repository.state === "active" ? (
          <Button
            type="text"
            size="small"
            danger
            icon={<DeleteOutlined />}
            className="opacity-40 transition-opacity group-hover:opacity-100 focus:opacity-100"
            aria-label={text(
              `删除 ${repository.name}`,
              `Delete ${repository.name}`,
            )}
            onClick={() => setToDelete(repository)}
          />
        ) : repository.state === "deleting" ? (
          <Badge tone="amber">{text("删除中", "Deleting")}</Badge>
        ) : (
          <span className="text-xs text-zinc-600">—</span>
        ),
    },
  ];

  return (
    <div>
      <PageHeader
        title={text("仓库", "Repositories")}
        description={text(
          "Hosted 与 Proxy Repository 的统一视图",
          "A unified view of hosted and proxy repositories",
        )}
        actions={
          <CreateRepositoryDialog profiles={formatProfiles} onCreated={load} />
        }
      />
      <MetricStrip
        items={[
          {
            label: text("仓库总数", "Repositories"),
            value: operationalCount,
            hint:
              deletedCount > 0
                ? text(`${deletedCount} 个已归档`, `${deletedCount} archived`)
                : text(`${activeCount} 个活跃`, `${activeCount} active`),
          },
          {
            label: text("代理仓库", "Proxy repositories"),
            value: proxyCount,
            hint: text("上游缓存与镜像", "Upstream caches and mirrors"),
          },
          {
            label: text("当前占用", "Storage used"),
            value: totalUsedBytes ? formatBytes(totalUsedBytes) : "—",
            hint: Object.keys(capacities).length
              ? text(
                  `${formatNumber(
                    Object.values(capacities).reduce(
                      (sum, value) => sum + value.objectCount,
                      0,
                    ),
                    locale,
                  )} 个对象`,
                  `${formatNumber(
                    Object.values(capacities).reduce(
                      (sum, value) => sum + value.objectCount,
                      0,
                    ),
                    locale,
                  )} objects`,
                )
              : text("容量未启用", "Capacity unavailable"),
          },
        ]}
      />
      <FilterBar
        className="mt-4 mb-4"
        actions={
          filter || formatFilter !== "all" || stateFilter !== "operational" ? (
            <Button
              type="text"
              icon={<ClearOutlined />}
              onClick={() => {
                setFilter("");
                setFormatFilter("all");
                setStateFilter("operational");
              }}
            >
              {text("清除筛选", "Clear filters")}
            </Button>
          ) : undefined
        }
      >
        <FilterField label={text("搜索", "Search")} className="min-w-[280px]">
          <Input
            allowClear
            prefix={<SearchOutlined />}
            placeholder={text(
              "名称、格式或仓库类型…",
              "Name, format, or repository type…",
            )}
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
          />
        </FilterField>
        <FilterField label={text("格式", "Format")} className="min-w-[150px]">
          <Select<Format | "all">
            className="w-full"
            value={formatFilter}
            onChange={setFormatFilter}
            options={[
              { value: "all", label: text("全部格式", "All formats") },
              ...repositoryFormats(formatProfiles).map((format) => ({
                value: format,
                label: format,
              })),
            ]}
          />
        </FilterField>
        <FilterField label={text("状态", "Status")} className="min-w-[150px]">
          <Select<RepositoryStateFilter>
            className="w-full"
            value={stateFilter}
            onChange={setStateFilter}
            options={[
              {
                value: "operational",
                label: text("运行中与删除中", "Active and deleting"),
              },
              { value: "all", label: text("全部状态", "All statuses") },
              { value: "active", label: text("运行中", "Active") },
              { value: "deleting", label: text("删除中", "Deleting") },
              { value: "deleted", label: text("已删除", "Deleted") },
            ]}
          />
        </FilterField>
      </FilterBar>
      {error !== null ? (
        <ErrorBanner error={error} onRetry={load} />
      ) : loading ? (
        <Loading />
      ) : visible.length === 0 ? (
        <Card>
          <EmptyState
            title={
              items.length === 0
                ? text("暂无仓库", "No repositories")
                : text("暂无符合条件的仓库", "No matching repositories")
            }
            hint={
              items.length === 0
                ? text(
                    "点击右上角「新建仓库」创建第一个仓库",
                    "Use New repository to create the first repository",
                  )
                : deletedCount > 0 && stateFilter === "operational"
                  ? text(
                      "已删除仓库已归档，可在状态筛选中查看",
                      "Deleted repositories are archived and available through the status filter",
                    )
                  : text(
                      "调整筛选条件后重试",
                      "Adjust the filters and try again",
                    )
            }
          />
        </Card>
      ) : (
        <Card>
          {visible.length === 0 ? (
            <EmptyState
              title={text("没有匹配的仓库", "No matching repositories")}
              hint={text(
                "调整筛选条件，或继续加载更多仓库",
                "Adjust the filters or load more repositories",
              )}
            />
          ) : (
            <Table<Repository>
              className="ag-console-table"
              rowKey="id"
              rowClassName={() => "group"}
              size="middle"
              dataSource={visible}
              columns={columns}
              pagination={false}
              scroll={{ x: 1100 }}
            />
          )}
          <Pagination
            hasMore={!!nextToken}
            loading={loadingMore}
            onMore={loadMore}
          />
        </Card>
      )}
      <ConfirmDialog
        open={!!toDelete}
        title={text("删除仓库", "Delete repository")}
        message={
          <>
            {text("确定要删除仓库", "Delete repository")}{" "}
            <span className="font-mono text-zinc-100">{toDelete?.name}</span>{" "}
            {text(
              "吗？仓库会立即停止读写并进入 deleting 状态，后台通常在一分钟内完成处理并标记为已删除。此操作不可撤销，审计元数据会保留。",
              "? Reads and writes stop immediately while the repository enters the deleting state. Background cleanup normally completes within one minute. This cannot be undone; audit metadata is retained.",
            )}
          </>
        }
        confirmLabel={text("删除", "Delete")}
        danger
        busy={deleting}
        onConfirm={confirmDelete}
        onClose={() => setToDelete(null)}
      />
    </div>
  );
}
