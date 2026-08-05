import { useCallback, useEffect, useState } from "react";
import {
  DatabaseOutlined,
  DeleteOutlined,
  EditOutlined,
  PlusOutlined,
  SearchOutlined,
  SettingOutlined,
} from "@ant-design/icons";
import { Button, Input, Segmented, Select, Space, Switch, Table } from "antd";
import type { ColumnsType } from "antd/es/table";
import {
  listGroups,
  createGroup,
  deleteGroup,
  listGroupMembers,
  replaceGroup,
  replaceGroupMembers,
  listRepositories,
  getGroupCapacity,
} from "../client";
import type {
  Group,
  Format,
  Member,
  Repository,
  GroupCapacityMember,
} from "../client";
import { PageHeader, Card, Pagination, Field } from "../components/Layout";
import {
  Loading,
  ErrorBanner,
  EmptyState,
  isNotFound,
} from "../components/Feedback";
import { Badge, FormatBadge } from "../components/Badge";
import { Modal, ConfirmDialog, useDisclosure } from "../components/Modal";
import { MemberOrderPicker } from "../components/MemberOrderPicker";
import {
  FilterBar,
  FilterField,
  MetricStrip,
} from "../components/ConsolePrimitives";

const FORMATS: Format[] = ["oci", "maven", "conan", "raw"];

function CreateGroupDialog({
  repos,
  onCreated,
}: {
  repos: Repository[];
  onCreated: () => void;
}) {
  const dialog = useDisclosure();
  const [name, setName] = useState("");
  const [format, setFormat] = useState<Format>("oci");
  const [memberIds, setMemberIds] = useState<string[]>([]);
  const [anonymousRead, setAnonymousRead] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);

  const candidates = repos.filter(
    (r) => r.format === format && r.state === "active",
  );

  const submit = async () => {
    setBusy(true);
    setError(null);
    const members: Member[] = memberIds.map((repositoryId, position) => ({
      repositoryId,
      position,
    }));
    const { error: err } = await createGroup({
      body: { name: name.trim(), format, anonymousRead, members },
      headers: { "Idempotency-Key": crypto.randomUUID() },
    });
    setBusy(false);
    if (err) {
      setError(err);
      return;
    }
    dialog.hide();
    setName("");
    setMemberIds([]);
    setAnonymousRead(false);
    onCreated();
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
        新建分组
      </Button>
      <Modal
        open={dialog.open}
        title="新建分组"
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
              disabled={!name.trim()}
            >
              创建
            </Button>
          </Space>
        }
      >
        <div className="space-y-4">
          {error !== null && <ErrorBanner error={error} />}
          <Field label="分组名称">
            <Input
              className="font-mono"
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
          </Field>
          <Field label="格式" group>
            <Segmented<Format>
              block
              className="font-mono"
              value={format}
              options={FORMATS}
              onChange={(nextFormat) => {
                setFormat(nextFormat);
                setMemberIds([]);
              }}
            />
          </Field>
          <Field
            label="成员仓库"
            hint="分组按成员顺序（自上而下）解析制品"
            group
          >
            {candidates.length === 0 ? (
              <div className="rounded-lg border border-zinc-800 px-2 py-3 text-center text-xs text-zinc-600">
                该格式下暂无活跃仓库
              </div>
            ) : (
              <MemberOrderPicker
                candidates={candidates}
                memberIds={memberIds}
                onChange={setMemberIds}
              />
            )}
          </Field>
          <div className="flex items-center justify-between gap-6 rounded-lg border border-zinc-800 bg-zinc-950/40 px-3 py-2.5">
            <div>
              <div className="text-sm font-medium text-zinc-200">
                允许匿名读取
              </div>
              <div className="mt-0.5 text-xs text-zinc-500">
                Group 和成员 Repository
                都允许匿名读取时，匿名请求才会解析该成员。
              </div>
            </div>
            <Switch
              checked={anonymousRead}
              onChange={setAnonymousRead}
              aria-label="允许分组匿名读取"
            />
          </div>
        </div>
      </Modal>
    </>
  );
}

function CapacityDialog({ group }: { group: Group }) {
  const dialog = useDisclosure();
  const [capacity, setCapacity] = useState<
    Awaited<ReturnType<typeof getGroupCapacity>>["data"] | null
  >(null);
  const [error, setError] = useState<unknown>(null);
  const load = async () => {
    setError(null);
    setCapacity(null);
    const result = await getGroupCapacity({ path: { groupId: group.id } });
    if (result.error || !result.data)
      setError(result.error ?? new Error("加载分组容量失败"));
    else setCapacity(result.data);
  };
  const formatBytes = (value: number) =>
    value < 1024 ? `${value} B` : `${(value / 1024 / 1024).toFixed(1)} MB`;
  const columns: ColumnsType<GroupCapacityMember> = [
    {
      title: "位置",
      dataIndex: "position",
      key: "position",
      width: 70,
      render: (position: number) => (
        <span className="text-zinc-500">{position + 1}</span>
      ),
    },
    {
      title: "成员",
      dataIndex: "repositoryId",
      key: "repositoryId",
      width: 180,
      render: (id: string) => (
        <span className="font-mono text-xs text-zinc-400">
          {id.slice(0, 8)}…
        </span>
      ),
    },
    {
      title: "类型",
      dataIndex: "type",
      key: "type",
      width: 110,
      render: (type: GroupCapacityMember["type"]) => (
        <Badge tone={type === "proxy" ? "blue" : "green"}>{type}</Badge>
      ),
    },
    {
      title: "已用",
      dataIndex: "usedBytes",
      key: "usedBytes",
      width: 120,
      render: (value: number) => (
        <span className="text-zinc-300">{formatBytes(value)}</span>
      ),
    },
    {
      title: "对象",
      dataIndex: "objectCount",
      key: "objectCount",
      width: 90,
      render: (value: number) => <span className="text-zinc-300">{value}</span>,
    },
    {
      title: "配额",
      dataIndex: "quotaBytes",
      key: "quotaBytes",
      width: 120,
      render: (value: number | undefined) => (
        <span className="text-zinc-500">
          {value ? formatBytes(value) : "无限制"}
        </span>
      ),
    },
  ];
  return (
    <>
      <Button
        size="small"
        type="text"
        icon={<DatabaseOutlined />}
        onClick={() => {
          dialog.show();
          void load();
        }}
      >
        容量
      </Button>
      <Modal
        open={dialog.open}
        onClose={dialog.hide}
        title={`容量贡献 · ${group.name}`}
      >
        {error !== null && <ErrorBanner error={error} />}
        {!capacity ? (
          <Loading />
        ) : (
          <Table<GroupCapacityMember>
            className="ag-console-table"
            rowKey="repositoryId"
            size="small"
            dataSource={capacity.members}
            columns={columns}
            pagination={false}
          />
        )}
      </Modal>
    </>
  );
}

function RenameGroupDialog({
  group,
  onSaved,
}: {
  group: Group;
  onSaved: () => void;
}) {
  const dialog = useDisclosure();
  const [name, setName] = useState(group.name);
  const [anonymousRead, setAnonymousRead] = useState(group.anonymousRead);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);

  const save = async () => {
    setBusy(true);
    setError(null);
    const { error: err } = await replaceGroup({
      path: { groupId: group.id },
      body: { ...group, name: name.trim(), anonymousRead },
      headers: { "If-Match": group.version },
    });
    setBusy(false);
    if (err) {
      setError(err);
      return;
    }
    dialog.hide();
    onSaved();
  };

  return (
    <>
      <Button
        size="small"
        icon={<SettingOutlined />}
        onClick={() => {
          setName(group.name);
          setAnonymousRead(group.anonymousRead);
          setError(null);
          dialog.show();
        }}
      >
        设置
      </Button>
      <Modal
        open={dialog.open}
        title={`设置分组：${group.name}`}
        onClose={dialog.hide}
        footer={
          <Space>
            <Button onClick={dialog.hide} disabled={busy}>
              取消
            </Button>
            <Button
              type="primary"
              onClick={save}
              loading={busy}
              disabled={!name.trim()}
            >
              保存
            </Button>
          </Space>
        }
      >
        <div className="space-y-4">
          {error !== null && <ErrorBanner error={error} />}
          <Field label="分组名称">
            <Input
              className="font-mono"
              value={name}
              onChange={(e) => setName(e.target.value)}
            />
          </Field>
          <div className="flex items-center justify-between gap-6 rounded-lg border border-zinc-800 bg-zinc-950/40 px-3 py-2.5">
            <div>
              <div className="text-sm font-medium text-zinc-200">
                允许匿名读取
              </div>
              <div className="mt-0.5 text-xs text-zinc-500">
                仍需成员 Repository 自身允许匿名读取。
              </div>
            </div>
            <Switch
              checked={anonymousRead}
              onChange={setAnonymousRead}
              aria-label="允许分组匿名读取"
            />
          </div>
        </div>
      </Modal>
    </>
  );
}

function MembersDialog({
  group,
  repos,
  onSaved,
}: {
  group: Group;
  repos: Repository[];
  onSaved: () => void;
}) {
  const dialog = useDisclosure();
  const [memberIds, setMemberIds] = useState<string[]>([]);
  const [version, setVersion] = useState(group.version);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);

  const candidates = repos.filter(
    (r) => r.format === group.format && r.state === "active",
  );

  const open = async () => {
    setError(null);
    dialog.show();
    const { data, response } = await listGroupMembers({
      path: { groupId: group.id },
    });
    if (data) {
      const sorted = [...data].sort((a, b) => a.position - b.position);
      setMemberIds(sorted.map((m) => m.repositoryId));
    }
    const etag = response?.headers.get("ETag");
    if (etag) setVersion(etag.replaceAll('"', ""));
  };

  const save = async () => {
    setBusy(true);
    setError(null);
    const members: Member[] = memberIds.map((repositoryId, position) => ({
      repositoryId,
      position,
    }));
    const { error: err } = await replaceGroupMembers({
      path: { groupId: group.id },
      body: members,
      headers: { "If-Match": version },
    });
    setBusy(false);
    if (err) {
      setError(err);
      return;
    }
    dialog.hide();
    onSaved();
  };

  return (
    <>
      <Button size="small" icon={<EditOutlined />} onClick={open}>
        编辑成员
      </Button>
      <Modal
        open={dialog.open}
        title={`编辑成员：${group.name}`}
        onClose={dialog.hide}
        footer={
          <Space>
            <Button onClick={dialog.hide} disabled={busy}>
              取消
            </Button>
            <Button type="primary" onClick={save} loading={busy}>
              保存
            </Button>
          </Space>
        }
      >
        <div className="space-y-3">
          {error !== null && <ErrorBanner error={error} />}
          <p className="text-xs text-zinc-500">
            调整成员及其优先级顺序（position 自上而下）。
          </p>
          <MemberOrderPicker
            candidates={candidates}
            memberIds={memberIds}
            onChange={setMemberIds}
          />
        </div>
      </Modal>
    </>
  );
}

export function GroupsPage() {
  const [groups, setGroups] = useState<Group[]>([]);
  const [repos, setRepos] = useState<Repository[]>([]);
  const [nextToken, setNextToken] = useState<string | undefined>();
  const [loading, setLoading] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [toDelete, setToDelete] = useState<Group | null>(null);
  const [deleting, setDeleting] = useState(false);
  const [filter, setFilter] = useState("");
  const [formatFilter, setFormatFilter] = useState<Format | "all">("all");

  const repoName = (id: string) =>
    repos.find((r) => r.id === id)?.name ?? id.slice(0, 8) + "…";
  const repoById = (id: string) => repos.find((r) => r.id === id);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    const [g, r] = await Promise.all([
      listGroups({ query: { pageSize: 100 } }),
      listRepositories({ query: { pageSize: 200 } }),
    ]);
    setLoading(false);
    if (g.error) {
      setError(g.error);
      return;
    }
    setGroups(g.data?.items ?? []);
    setNextToken(g.data?.nextPageToken);
    setRepos(r.data?.items ?? []);
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  const loadMore = async () => {
    if (!nextToken) return;
    setLoadingMore(true);
    const { data, error: err } = await listGroups({
      query: { pageSize: 100, pageToken: nextToken },
    });
    setLoadingMore(false);
    if (err) {
      setError(err);
      return;
    }
    setGroups((prev) => [...prev, ...(data?.items ?? [])]);
    setNextToken(data?.nextPageToken);
  };

  const confirmDelete = async () => {
    if (!toDelete) return;
    setDeleting(true);
    const { error: err } = await deleteGroup({
      path: { groupId: toDelete.id },
    });
    setDeleting(false);
    if (!err) {
      setToDelete(null);
      void load();
    } else {
      setError(err);
    }
  };

  const visibleGroups = groups.filter(
    (group) =>
      (!filter || group.name.toLowerCase().includes(filter.toLowerCase())) &&
      (formatFilter === "all" || group.format === formatFilter),
  );
  const memberCount = groups.reduce(
    (total, group) => total + (group.members?.length ?? 0),
    0,
  );
  const publicGroups = groups.filter((group) => group.anonymousRead).length;
  const columns: ColumnsType<Group> = [
    {
      title: "名称",
      dataIndex: "name",
      key: "name",
      width: 180,
      render: (name: string) => (
        <span className="font-medium text-zinc-100">{name}</span>
      ),
    },
    {
      title: "格式",
      dataIndex: "format",
      key: "format",
      width: 100,
      render: (format: Group["format"]) => <FormatBadge format={format} />,
    },
    {
      title: "访问",
      dataIndex: "anonymousRead",
      key: "anonymousRead",
      width: 140,
      render: (anonymousRead: boolean) => (
        <Badge tone={anonymousRead ? "green" : "zinc"}>
          {anonymousRead ? "anonymous read" : "private"}
        </Badge>
      ),
    },
    {
      title: "成员（按优先级）",
      key: "members",
      width: 440,
      render: (_value, group) => (
        <div className="flex flex-wrap gap-1">
          {[...(group.members ?? [])]
            .sort((a, b) => a.position - b.position)
            .map((member) => {
              const repository = repoById(member.repositoryId);
              return (
                <span
                  key={member.repositoryId}
                  className="rounded-md bg-zinc-800 px-2 py-0.5 font-mono text-[11px] text-zinc-300"
                  title={member.repositoryId}
                >
                  {member.position + 1}. {repoName(member.repositoryId)} ·{" "}
                  {repository?.type ?? "hosted"} ·{" "}
                  {repository?.anonymousRead ? "anon" : "private"}
                </span>
              );
            })}
          {(group.members ?? []).length === 0 && (
            <span className="text-xs text-zinc-600">无成员</span>
          )}
        </div>
      ),
    },
    {
      title: "版本",
      dataIndex: "version",
      key: "version",
      width: 90,
      render: (version: string) => (
        <span className="font-mono text-xs text-zinc-500">v{version}</span>
      ),
    },
    {
      title: "",
      key: "actions",
      width: 280,
      align: "right",
      render: (_value, group) => (
        <div className="flex items-center justify-end gap-2">
          <RenameGroupDialog group={group} onSaved={load} />
          <MembersDialog group={group} repos={repos} onSaved={load} />
          <CapacityDialog group={group} />
          <Button
            type="text"
            size="small"
            danger
            icon={<DeleteOutlined />}
            aria-label={`删除分组 ${group.name}`}
            onClick={() => setToDelete(group)}
          />
        </div>
      ),
    },
  ];

  return (
    <div>
      <PageHeader
        title="分组"
        description="将多个同格式仓库聚合为统一入口"
        actions={<CreateGroupDialog repos={repos} onCreated={load} />}
      />
      {error !== null ? (
        isNotFound(error) ? (
          <Card>
            <EmptyState
              title="分组功能未启用"
              hint="当前后端构建尚未挂载分组管理端点（返回 404）"
            />
          </Card>
        ) : (
          <ErrorBanner error={error} onRetry={load} />
        )
      ) : loading ? (
        <Loading />
      ) : groups.length === 0 ? (
        <Card>
          <EmptyState title="暂无分组" hint="创建分组以聚合多个仓库" />
        </Card>
      ) : (
        <>
          <MetricStrip
            items={[
              {
                label: "分组总数",
                value: groups.length,
                hint: `${memberCount} 个成员引用`,
              },
              {
                label: "匿名可读",
                value: publicGroups,
                hint: publicGroups
                  ? "仍需成员仓库允许匿名读取"
                  : "全部为私有入口",
                tone: publicGroups ? "success" : "default",
              },
              {
                label: "覆盖格式",
                value: new Set(groups.map((group) => group.format)).size,
                hint: "按同格式仓库解析",
              },
            ]}
          />
          <Card>
            <FilterBar
              className="border-x-0 border-t-0 rounded-none"
              actions={
                filter || formatFilter !== "all" ? (
                  <Button
                    type="text"
                    onClick={() => {
                      setFilter("");
                      setFormatFilter("all");
                    }}
                  >
                    清除筛选
                  </Button>
                ) : undefined
              }
            >
              <FilterField label="搜索" className="min-w-[260px]">
                <Input
                  allowClear
                  prefix={<SearchOutlined />}
                  placeholder="搜索分组名称…"
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
                    ...FORMATS.map((format) => ({
                      value: format,
                      label: format,
                    })),
                  ]}
                />
              </FilterField>
            </FilterBar>
            {visibleGroups.length === 0 ? (
              <EmptyState title="没有匹配的分组" hint="调整筛选条件后重试" />
            ) : (
              <Table<Group>
                className="ag-console-table"
                rowKey="id"
                size="middle"
                dataSource={visibleGroups}
                columns={columns}
                pagination={false}
                scroll={{ x: 1200 }}
              />
            )}
            <Pagination
              hasMore={!!nextToken}
              loading={loadingMore}
              onMore={loadMore}
            />
          </Card>
        </>
      )}
      <ConfirmDialog
        open={!!toDelete}
        title="删除分组"
        message={
          <>
            确定删除分组{" "}
            <span className="font-mono text-zinc-100">{toDelete?.name}</span>{" "}
            吗？成员仓库本身不会被删除。
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
