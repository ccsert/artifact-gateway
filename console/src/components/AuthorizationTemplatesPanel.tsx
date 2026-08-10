import { useCallback, useEffect, useMemo, useState } from "react";
import {
  Button,
  Input,
  Modal,
  Popconfirm,
  Select,
  Space,
  Table,
  Tag,
  Typography,
} from "antd";
import type { ColumnsType } from "antd/es/table";
import {
  DeleteOutlined,
  EditOutlined,
  PlusOutlined,
  SafetyCertificateOutlined,
  ThunderboltOutlined,
} from "@ant-design/icons";
import {
  applyAuthorizationTemplate,
  createAuthorizationTemplate,
  deleteAuthorizationTemplate,
  listGrants,
  listAuthorizationTemplates,
  updateAuthorizationTemplate,
} from "../client";
import type {
  AuthorizationTemplate,
  AuthorizationTemplateGrant,
  Repository,
} from "../client";
import { ErrorBanner, Loading } from "./Feedback";
import { Card, CardHeader } from "./Layout";
import { usePreferences } from "../lib/preferences";

type DraftGrant = {
  key: string;
  principal: string;
  scopes: AuthorizationTemplateGrant["scopes"];
  resourcePrefix?: string;
};

function emptyGrant(): DraftGrant {
  return { key: `${Date.now()}-${Math.random()}`, principal: "", scopes: ["repositories:read"] };
}

function toDraft(template: AuthorizationTemplate): DraftGrant[] {
  return template.grants.map((grant) => ({ ...grant, key: crypto.randomUUID() }));
}

type Props = { repositories: Repository[] };

export function AuthorizationTemplatesPanel({ repositories }: Props) {
  const { text } = usePreferences();
  const [templates, setTemplates] = useState<AuthorizationTemplate[] | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [editorOpen, setEditorOpen] = useState(false);
  const [editing, setEditing] = useState<AuthorizationTemplate | null>(null);
  const [draftName, setDraftName] = useState("");
  const [draftDescription, setDraftDescription] = useState("");
  const [draftGrants, setDraftGrants] = useState<DraftGrant[]>([]);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<unknown>(null);
  const [applyTarget, setApplyTarget] = useState<AuthorizationTemplate | null>(null);
  const [applyRepository, setApplyRepository] = useState("");
  const [applying, setApplying] = useState(false);
  const [applyError, setApplyError] = useState<unknown>(null);

  const load = useCallback(async () => {
    setError(null);
    const result = await listAuthorizationTemplates();
    if (result.error || !result.data) {
      setError(result.error ?? new Error(text("加载授权模板失败", "Failed to load authorization templates")));
      return;
    }
    setTemplates(result.data);
  }, [text]);

  useEffect(() => {
    void load();
  }, [load]);

  const openCreate = () => {
    setEditing(null);
    setEditorOpen(true);
    setDraftName("");
    setDraftDescription("");
    setDraftGrants([emptyGrant()]);
    setSaveError(null);
  };

  const openEdit = (template: AuthorizationTemplate) => {
    setEditing(template);
    setEditorOpen(true);
    setDraftName(template.name);
    setDraftDescription(template.description ?? "");
    setDraftGrants(toDraft(template));
    setSaveError(null);
  };

  const normalizedGrants = useMemo(
    () =>
      draftGrants.map(({ principal, scopes, resourcePrefix }) => ({
        principal: principal.trim(),
        scopes,
        ...(resourcePrefix?.trim() ? { resourcePrefix: resourcePrefix.trim() } : {}),
      })),
    [draftGrants],
  );

  const save = async () => {
    if (!draftName.trim() || normalizedGrants.some((grant) => !grant.principal)) {
      setSaveError(new Error(text("模板名称和每条规则的主体不能为空", "Template name and every grant principal are required")));
      return;
    }
    const keys = new Set(normalizedGrants.map((grant) => `${grant.principal}\x00${grant.resourcePrefix ?? ""}`));
    if (keys.size !== normalizedGrants.length) {
      setSaveError(new Error(text("主体和资源前缀不能重复", "Principal and resource prefix pairs must be unique")));
      return;
    }
    setSaving(true);
    setSaveError(null);
    const body = { name: draftName.trim(), description: draftDescription.trim() || undefined, grants: normalizedGrants };
    const result = editing
      ? await updateAuthorizationTemplate({ path: { templateId: editing.id }, headers: { "If-Match": editing.version }, body })
      : await createAuthorizationTemplate({ body });
    setSaving(false);
    if (result.error || !result.data) {
      setSaveError(result.error ?? new Error(text("保存授权模板失败", "Failed to save authorization template")));
      return;
    }
    setEditorOpen(false);
    setEditing(null);
    await load();
  };

  const apply = async () => {
    if (!applyTarget || !applyRepository) return;
    setApplying(true);
    setApplyError(null);
    const current = await listGrants({ path: { repositoryId: applyRepository } });
    if (current.error || !current.data) {
      setApplying(false);
      setApplyError(current.error ?? new Error(text("加载仓库授权版本失败", "Failed to load repository grant version")));
      return;
    }
    const version = current.response?.headers.get("ETag")?.replaceAll('"', "") ?? "1";
    const result = await applyAuthorizationTemplate({
      path: { templateId: applyTarget.id },
      headers: { "If-Match": version },
      body: { repositoryId: applyRepository },
    });
    setApplying(false);
    if (result.error) {
      setApplyError(result.error);
      return;
    }
    setApplyTarget(null);
  };

  const columns: ColumnsType<AuthorizationTemplate> = [
    {
      title: text("模板", "Template"),
      key: "template",
      render: (_, template) => (
        <div>
          <Typography.Text strong>{template.name}</Typography.Text>
          <div className="text-xs text-zinc-500">{template.description || text("无描述", "No description")}</div>
        </div>
      ),
    },
    {
      title: text("规则", "Rules"),
      dataIndex: "grants",
      width: 100,
      render: (grants: AuthorizationTemplateGrant[]) => <Tag>{grants.length}</Tag>,
    },
    {
      title: text("更新时间", "Updated"),
      dataIndex: "updatedAt",
      width: 190,
      render: (value: string) => new Date(value).toLocaleString(),
    },
    {
      title: text("操作", "Actions"),
      key: "actions",
      width: 230,
      render: (_, template) => (
        <Space>
          <Button size="small" icon={<EditOutlined />} onClick={() => openEdit(template)}>
            {text("编辑", "Edit")}
          </Button>
          <Button
            size="small"
            type="primary"
            ghost
            icon={<ThunderboltOutlined />}
            onClick={() => {
              setApplyTarget(template);
              setApplyRepository("");
              setApplyError(null);
            }}
          >
            {text("应用", "Apply")}
          </Button>
          <Popconfirm
            title={text("删除此模板？", "Delete this template?")}
            onConfirm={async () => {
              const result = await deleteAuthorizationTemplate({ path: { templateId: template.id } });
              if (result.error) setError(result.error);
              else await load();
            }}
            okText={text("删除", "Delete")}
            cancelText={text("取消", "Cancel")}
          >
            <Button danger size="small" icon={<DeleteOutlined />} aria-label={text("删除模板", "Delete template")} />
          </Popconfirm>
        </Space>
      ),
    },
  ];

  return (
    <Card className="mt-4" bodyClassName="p-0">
      <CardHeader
        title={text("授权模板", "Authorization templates")}
        extra={
          <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>
            {text("新建模板", "New template")}
          </Button>
        }
      />
      <div className="px-5 py-3 text-xs text-zinc-500">
        <SafetyCertificateOutlined className="mr-2 text-cyan-400" />
        {text("模板只保存可复用规则，不会自动修改仓库；应用时会检查仓库当前版本。", "Templates store reusable rules only. Applying one updates a repository with optimistic version checks.")}
      </div>
      {error ? <div className="px-5 pb-4"><ErrorBanner error={error} onRetry={load} /></div> : !templates ? <Loading /> : (
        <Table<AuthorizationTemplate>
          className="ag-console-table"
          rowKey="id"
          dataSource={templates}
          columns={columns}
          pagination={false}
          scroll={{ x: 900, y: 320 }}
        />
      )}
      <Modal
        open={editorOpen}
        title={editing ? text("编辑授权模板", "Edit authorization template") : text("新建授权模板", "New authorization template")}
        onCancel={() => setEditorOpen(false)}
        destroyOnHidden
        width={980}
        footer={<Space><Button onClick={() => setEditorOpen(false)} disabled={saving}>{text("取消", "Cancel")}</Button><Button type="primary" loading={saving} onClick={() => void save()}>{text("保存", "Save")}</Button></Space>}
      >
        {saveError !== null && <div className="mb-3"><ErrorBanner error={saveError} /></div>}
        <div className="grid grid-cols-[minmax(220px,1fr)_minmax(320px,2fr)] gap-3">
          <Input placeholder={text("模板名称", "Template name")} value={draftName} onChange={(event) => setDraftName(event.target.value)} />
          <Input placeholder={text("用途描述（可选）", "Description (optional)")} value={draftDescription} onChange={(event) => setDraftDescription(event.target.value)} />
        </div>
        <Table<DraftGrant>
          className="mt-4"
          rowKey="key"
          dataSource={draftGrants}
          pagination={false}
          size="small"
          scroll={{ x: 760, y: 300 }}
          columns={[
            { title: text("主体", "Principal"), key: "principal", render: (_, grant) => <Input value={grant.principal} placeholder="user:release" onChange={(event) => setDraftGrants((current) => current.map((item) => item.key === grant.key ? { ...item, principal: event.target.value } : item))} /> },
            { title: text("权限", "Scopes"), key: "scopes", width: 220, render: (_, grant) => <Select className="w-full" value={grant.scopes[0]} options={[{ value: "repositories:read", label: text("读取", "Read") }, { value: "repositories:write", label: text("写入", "Write") }, { value: "repositories:admin", label: text("管理员", "Admin") }, { value: "repositories:intelligence", label: text("制品情报", "Artifact intelligence") }]} onChange={(value) => setDraftGrants((current) => current.map((item) => item.key === grant.key ? { ...item, scopes: [value] } : item))} /> },
            { title: text("资源前缀", "Resource prefix"), key: "prefix", width: 260, render: (_, grant) => <Input className="font-mono" value={grant.resourcePrefix} placeholder={text("例如 org.example；留空表示整个仓库", "For example org.example; blank means entire repository")} onChange={(event) => setDraftGrants((current) => current.map((item) => item.key === grant.key ? { ...item, resourcePrefix: event.target.value } : item))} /> },
            { title: text("操作", "Actions"), key: "actions", width: 70, render: (_, grant) => <Button danger type="text" icon={<DeleteOutlined />} aria-label={text("移除规则", "Remove rule")} onClick={() => setDraftGrants((current) => current.filter((item) => item.key !== grant.key))} /> },
          ]}
        />
        <Button className="mt-3" icon={<PlusOutlined />} onClick={() => setDraftGrants((current) => [...current, emptyGrant()])}>{text("添加规则", "Add rule")}</Button>
      </Modal>
      <Modal
        open={Boolean(applyTarget)}
        title={text("应用授权模板", "Apply authorization template")}
        onCancel={() => setApplyTarget(null)}
        destroyOnHidden
        footer={<Space><Button onClick={() => setApplyTarget(null)} disabled={applying}>{text("取消", "Cancel")}</Button><Button type="primary" loading={applying} disabled={!applyRepository} onClick={() => void apply()}>{text("应用", "Apply")}</Button></Space>}
      >
        {applyError !== null && <div className="mb-3"><ErrorBanner error={applyError} /></div>}
        <p className="mb-3 text-sm text-zinc-500">{text("选择目标仓库。应用会替换该仓库现有授权规则，并保留并发版本保护。", "Choose a target repository. Applying replaces its current grants with optimistic concurrency protection.")}</p>
        <Select className="w-full" showSearch={{ optionFilterProp: "label" }} placeholder={text("选择仓库", "Select repository")} value={applyRepository || undefined} onChange={setApplyRepository} options={repositories.filter((repository) => repository.state === "active").map((repository) => ({ value: repository.id, label: `${repository.name} · ${repository.format}` }))} />
      </Modal>
    </Card>
  );
}
