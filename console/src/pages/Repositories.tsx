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
  getRepositoryCapacity,
} from "../client";
import type { Repository, Format } from "../client";
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

const FORMATS: Format[] = ["oci", "maven", "conan", "raw"];

function CreateRepositoryDialog({ onCreated }: { onCreated: () => void }) {
  const dialog = useDisclosure();
  const [name, setName] = useState("");
  const [format, setFormat] = useState<Format>("oci");
  const [type, setType] = useState<"hosted" | "proxy">("hosted");
  const [endpoint, setEndpoint] = useState("");
  const [allowedHosts, setAllowedHosts] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);

  const needsHosts =
    type === "proxy" && (format === "raw" || format === "conan");

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
        format,
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
  };

  return (
    <>
      <Button
        type="primary"
        icon={<PlusOutlined />}
        onClick={() => {
          setError(null);
          dialog.show();
        }}
      >
        新建仓库
      </Button>
      <Modal
        open={dialog.open}
        title="新建仓库"
        onClose={dialog.hide}
        footer={
          <Space>
            <Button onClick={dialog.hide} disabled={busy}>
              取消
            </Button>
            <Button
              type="primary"
              onClick={submit}
              loading={busy}
              disabled={
                busy ||
                !name.trim() ||
                (type === "proxy" &&
                  (!endpoint.trim() || (needsHosts && !allowedHosts.trim())))
              }
            >
              创建
            </Button>
          </Space>
        }
      >
        <div className="space-y-4">
          {error !== null && <ErrorBanner error={error} />}
          <Field label="类型" group>
            <Segmented<"hosted" | "proxy">
              block
              value={type}
              onChange={setType}
              options={[
                { value: "hosted", label: "托管 (hosted)" },
                { value: "proxy", label: "代理 (proxy)" },
              ]}
            />
            <span className="mt-1 block text-xs text-zinc-600">
              {type === "hosted"
                ? "自己托管制品，可推送"
                : "从上游仓库拉取并缓存"}
            </span>
          </Field>
          <Field
            label="仓库名称"
            hint="小写字母、数字与连字符，例如 team-images"
          >
            <Input
              className="font-mono"
              placeholder="my-repository"
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
          </Field>
          <Field label="格式" group>
            <Segmented<Format>
              block
              className="font-mono"
              value={format}
              onChange={setFormat}
              options={FORMATS}
            />
          </Field>
          {type === "proxy" && (
            <>
              <Field label="上游地址 endpoint" hint="代理拉取的外部仓库地址">
                <Input
                  className="font-mono text-xs"
                  placeholder={endpointPlaceholder[format]}
                  value={endpoint}
                  onChange={(e) => setEndpoint(e.target.value)}
                />
              </Field>
              <Field
                label={`允许主机 allowedHosts${needsHosts ? "（必填）" : "（可选）"}`}
                hint="逗号分隔的主机名；raw/conan 代理必填"
              >
                <Input
                  className="font-mono text-xs"
                  placeholder="repo1.maven.org"
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
  const [items, setItems] = useState<Repository[]>([]);
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
    const { data, error: err } = await listRepositories({
      query: { pageSize: 100 },
    });
    setLoading(false);
    if (err) {
      setError(err);
      return;
    }
    const nextItems = data?.items ?? [];
    setItems(nextItems);
    setNextToken(data?.nextPageToken);
    const activeItems = nextItems.filter(
      (repository) => repository.state === "active",
    );
    const capacityResults = await Promise.all(
      activeItems.map(async (repository) => {
        const result = await getRepositoryCapacity({
          path: { repositoryId: repository.id },
        });
        return result.data ? ([repository.id, result.data] as const) : null;
      }),
    );
    setCapacities(
      Object.fromEntries(
        capacityResults.filter(
          (entry): entry is NonNullable<typeof entry> => entry !== null,
        ),
      ),
    );
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
    const activeItems = nextItems.filter(
      (repository) => repository.state === "active",
    );
    const capacityResults = await Promise.all(
      activeItems.map(async (repository) => {
        const result = await getRepositoryCapacity({
          path: { repositoryId: repository.id },
        });
        return result.data ? ([repository.id, result.data] as const) : null;
      }),
    );
    setCapacities((previous) => ({
      ...previous,
      ...Object.fromEntries(
        capacityResults.filter(
          (entry): entry is NonNullable<typeof entry> => entry !== null,
        ),
      ),
    }));
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
      title: "名称",
      dataIndex: "name",
      key: "name",
      fixed: "left",
      width: 190,
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
      title: "类型",
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
      title: "格式",
      dataIndex: "format",
      key: "format",
      width: 105,
      render: (format: Repository["format"]) => <FormatBadge format={format} />,
    },
    {
      title: "状态",
      dataIndex: "state",
      key: "state",
      width: 120,
      render: (state: Repository["state"]) => <StateBadge state={state} />,
    },
    {
      title: "容量",
      key: "capacity",
      width: 140,
      render: (_value, repository) => {
        const capacity = capacities[repository.id];
        return capacity ? (
          <div>
            <div className="font-mono text-xs text-zinc-300">
              {formatBytes(capacity.usedBytes)}
            </div>
            {capacity.quotaBytes > 0 && (
              <div className="mt-1 text-[10px] text-zinc-600">
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
      title: "配置",
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
      title: "",
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
            aria-label={`删除 ${repository.name}`}
            onClick={() => setToDelete(repository)}
          />
        ) : repository.state === "deleting" ? (
          <Badge tone="amber">删除中</Badge>
        ) : (
          <span className="text-xs text-zinc-600">—</span>
        ),
    },
  ];

  return (
    <div>
      <PageHeader
        title="仓库"
        description="Hosted 与 Proxy Repository 的统一视图"
        actions={<CreateRepositoryDialog onCreated={load} />}
      />
      <MetricStrip
        items={[
          {
            label: "仓库总数",
            value: operationalCount,
            hint:
              deletedCount > 0
                ? `${deletedCount} 个已归档`
                : `${activeCount} 个活跃`,
          },
          { label: "代理仓库", value: proxyCount, hint: "上游缓存与镜像" },
          {
            label: "当前占用",
            value: totalUsedBytes ? formatBytes(totalUsedBytes) : "—",
            hint: Object.keys(capacities).length
              ? `${formatNumber(Object.values(capacities).reduce((sum, value) => sum + value.objectCount, 0))} 个对象`
              : "容量未启用",
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
              清除筛选
            </Button>
          ) : undefined
        }
      >
        <FilterField label="搜索" className="min-w-[280px]">
          <Input
            allowClear
            prefix={<SearchOutlined />}
            placeholder="名称、格式或仓库类型…"
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
          />
        </FilterField>
        <FilterField label="格式" className="min-w-[150px]">
          <Select<Format | "all">
            className="w-full"
            value={formatFilter}
            onChange={setFormatFilter}
            options={[
              { value: "all", label: "全部格式" },
              ...FORMATS.map((format) => ({ value: format, label: format })),
            ]}
          />
        </FilterField>
        <FilterField label="状态" className="min-w-[150px]">
          <Select<RepositoryStateFilter>
            className="w-full"
            value={stateFilter}
            onChange={setStateFilter}
            options={[
              { value: "operational", label: "运行中与删除中" },
              { value: "all", label: "全部状态" },
              { value: "active", label: "运行中" },
              { value: "deleting", label: "删除中" },
              { value: "deleted", label: "已删除" },
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
            title={items.length === 0 ? "暂无仓库" : "暂无符合条件的仓库"}
            hint={
              items.length === 0
                ? "点击右上角「新建仓库」创建第一个仓库"
                : deletedCount > 0 && stateFilter === "operational"
                  ? "已删除仓库已归档，可在状态筛选中查看"
                  : "调整筛选条件后重试"
            }
          />
        </Card>
      ) : (
        <Card>
          {visible.length === 0 ? (
            <EmptyState
              title="没有匹配的仓库"
              hint="调整筛选条件，或继续加载更多仓库"
            />
          ) : (
            <Table<Repository>
              className="ag-console-table"
              rowKey="id"
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
        title="删除仓库"
        message={
          <>
            确定要删除仓库{" "}
            <span className="font-mono text-zinc-100">{toDelete?.name}</span>{" "}
            吗？仓库会立即停止读写并进入 deleting
            状态，后台通常在一分钟内完成处理并标记为已删除。此操作不可撤销，审计元数据会保留。
          </>
        }
        confirmLabel="删除"
        danger
        busy={deleting}
        onConfirm={confirmDelete}
        onClose={() => setToDelete(null)}
      />
    </div>
  );
}
