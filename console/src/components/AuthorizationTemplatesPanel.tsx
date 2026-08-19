import { useCallback, useEffect, useMemo, useState } from "react";
import {
  AutoComplete,
  Button,
  Dropdown,
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
  AppstoreOutlined,
  DeleteOutlined,
  DownOutlined,
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
  listAuthorizationRoles,
  listAuthorizationTemplates,
  updateAuthorizationTemplate,
} from "../client";
import type {
  AuthorizationTemplate,
  AuthorizationTemplateGrant,
  AuthorizationRole,
  Repository,
} from "../client";
import { EmptyState, ErrorBanner, Loading } from "./Feedback";
import { Card, CardHeader } from "./Layout";
import { usePreferences } from "../lib/preferences";
import {
  inferResourcePrefixFormat,
  ResourcePrefixEditor,
} from "./ResourcePrefixEditor";

type DraftGrant = {
  key: string;
  principal: string;
  scopes: AuthorizationTemplateGrant["scopes"];
  resourcePrefix?: string;
  roleId?: string;
  selectorFormat?: Repository["format"];
};

export type PrincipalOption = { value: string; label: string };

type TemplatePreset = {
  key: string;
  name: string;
  nameEn: string;
  description: string;
  descriptionEn: string;
  grants: Array<Pick<DraftGrant, "principal" | "scopes" | "resourcePrefix">>;
};

export const AUTHORIZATION_TEMPLATE_PRESETS: TemplatePreset[] = [
  {
    key: "read-only-consumer",
    name: "团队只读",
    nameEn: "Team read-only",
    description: "适合下载、搜索和浏览制品的主体。",
    descriptionEn:
      "For principals that only download, search, and browse artifacts.",
    grants: [{ principal: "", scopes: ["repositories:read"] }],
  },
  {
    key: "release-publisher",
    name: "发布机器人",
    nameEn: "Release publisher",
    description: "适合 CI 发布和编辑制品的主体。",
    descriptionEn: "For CI principals that publish and edit artifacts.",
    grants: [{ principal: "", scopes: ["repositories:write"] }],
  },
  {
    key: "security-bot",
    name: "制品情报机器人",
    nameEn: "Artifact intelligence bot",
    description: "只允许写入安全与制品情报元数据。",
    descriptionEn:
      "Only allows security and artifact-intelligence metadata writes.",
    grants: [{ principal: "", scopes: ["repositories:intelligence"] }],
  },
  {
    key: "repository-owner",
    name: "仓库管理员",
    nameEn: "Repository owner",
    description: "适合负责仓库配置、授权和制品生命周期的主体。",
    descriptionEn:
      "For principals responsible for repository configuration, grants, and lifecycle.",
    grants: [{ principal: "", scopes: ["repositories:admin"] }],
  },
];

function emptyGrant(): DraftGrant {
  return {
    key: `${Date.now()}-${Math.random()}`,
    principal: "",
    scopes: ["repositories:read"],
  };
}

const CUSTOM_ROLE = "__custom__";

function toDraft(
  template: AuthorizationTemplate,
  formats: Repository["format"][],
): DraftGrant[] {
  return template.grants.map((grant) => ({
    ...grant,
    key: crypto.randomUUID(),
    selectorFormat: inferResourcePrefixFormat(
      grant.resourcePrefix ?? "",
      formats,
    ),
  }));
}

type Props = {
  repositories: Repository[];
  principalOptions?: PrincipalOption[];
  rolesRevision?: number;
};

export function AuthorizationTemplatesPanel({
  repositories,
  principalOptions = [],
  rolesRevision = 0,
}: Props) {
  const { text } = usePreferences();
  const [templates, setTemplates] = useState<AuthorizationTemplate[] | null>(
    null,
  );
  const [error, setError] = useState<unknown>(null);
  const [roles, setRoles] = useState<AuthorizationRole[]>([]);
  const [editorOpen, setEditorOpen] = useState(false);
  const [editing, setEditing] = useState<AuthorizationTemplate | null>(null);
  const [draftName, setDraftName] = useState("");
  const [draftDescription, setDraftDescription] = useState("");
  const [draftGrants, setDraftGrants] = useState<DraftGrant[]>([]);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<unknown>(null);
  const [applyTarget, setApplyTarget] = useState<AuthorizationTemplate | null>(
    null,
  );
  const [applyRepository, setApplyRepository] = useState("");
  const [applying, setApplying] = useState(false);
  const [applyError, setApplyError] = useState<unknown>(null);

  const availableFormats = useMemo(
    () =>
      Array.from(new Set(repositories.map((repository) => repository.format))),
    [repositories],
  );

  const load = useCallback(async () => {
    setError(null);
    const [templateResult, roleResult] = await Promise.all([
      listAuthorizationTemplates(),
      listAuthorizationRoles(),
    ]);
    if (
      templateResult.error ||
      !templateResult.data ||
      roleResult.error ||
      !roleResult.data
    ) {
      setError(
        templateResult.error ??
          roleResult.error ??
          new Error(
            text(
              "加载授权模板或角色失败",
              "Failed to load authorization templates or roles",
            ),
          ),
      );
      return;
    }
    setTemplates(templateResult.data);
    setRoles(roleResult.data);
  }, [text]);

  useEffect(() => {
    void load();
  }, [load, rolesRevision]);

  const openCreate = () => {
    setEditing(null);
    setEditorOpen(true);
    setDraftName("");
    setDraftDescription("");
    setDraftGrants([emptyGrant()]);
    setSaveError(null);
  };

  const applyPreset = (presetKey: string) => {
    const preset = AUTHORIZATION_TEMPLATE_PRESETS.find(
      (item) => item.key === presetKey,
    );
    if (!preset) return;
    setEditing(null);
    setEditorOpen(true);
    setDraftName(text(preset.name, preset.nameEn));
    setDraftDescription(text(preset.description, preset.descriptionEn));
    setDraftGrants(
      preset.grants.map((grant) => ({
        ...grant,
        key: `${Date.now()}-${Math.random()}`,
      })),
    );
    setSaveError(null);
  };

  const presetMenuItems = AUTHORIZATION_TEMPLATE_PRESETS.map((preset) => ({
    key: preset.key,
    label: (
      <div className="min-w-[220px]">
        <div className="text-xs font-medium">
          {text(preset.name, preset.nameEn)}
        </div>
        <div className="text-xs text-zinc-500">
          {text(preset.description, preset.descriptionEn)}
        </div>
      </div>
    ),
  }));

  const openEdit = (template: AuthorizationTemplate) => {
    setEditing(template);
    setEditorOpen(true);
    setDraftName(template.name);
    setDraftDescription(template.description ?? "");
    setDraftGrants(toDraft(template, availableFormats));
    setSaveError(null);
  };

  const normalizedGrants = useMemo(
    () =>
      draftGrants.map(({ principal, scopes, resourcePrefix }) => ({
        principal: principal.trim(),
        scopes,
        ...(resourcePrefix?.trim()
          ? { resourcePrefix: resourcePrefix.trim() }
          : {}),
      })),
    [draftGrants],
  );

  const save = async () => {
    if (
      !draftName.trim() ||
      normalizedGrants.some((grant) => !grant.principal)
    ) {
      setSaveError(
        new Error(
          text(
            "模板名称和每条规则的主体不能为空",
            "Template name and every grant principal are required",
          ),
        ),
      );
      return;
    }
    const keys = new Set(
      normalizedGrants.map(
        (grant) => `${grant.principal}\x00${grant.resourcePrefix ?? ""}`,
      ),
    );
    if (keys.size !== normalizedGrants.length) {
      setSaveError(
        new Error(
          text(
            "主体和资源前缀不能重复",
            "Principal and resource prefix pairs must be unique",
          ),
        ),
      );
      return;
    }
    setSaving(true);
    setSaveError(null);
    const body = {
      name: draftName.trim(),
      description: draftDescription.trim() || undefined,
      grants: normalizedGrants,
    };
    const result = editing
      ? await updateAuthorizationTemplate({
          path: { templateId: editing.id },
          headers: { "If-Match": editing.version },
          body,
        })
      : await createAuthorizationTemplate({ body });
    setSaving(false);
    if (result.error || !result.data) {
      setSaveError(
        result.error ??
          new Error(
            text("保存授权模板失败", "Failed to save authorization template"),
          ),
      );
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
    const current = await listGrants({
      path: { repositoryId: applyRepository },
    });
    if (current.error || !current.data) {
      setApplying(false);
      setApplyError(
        current.error ??
          new Error(
            text(
              "加载仓库授权版本失败",
              "Failed to load repository grant version",
            ),
          ),
      );
      return;
    }
    const version =
      current.response?.headers.get("ETag")?.replaceAll('"', "") ?? "1";
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
          <div className="text-xs text-zinc-500">
            {template.description || text("无描述", "No description")}
          </div>
        </div>
      ),
    },
    {
      title: text("规则", "Rules"),
      dataIndex: "grants",
      width: 100,
      render: (grants: AuthorizationTemplateGrant[]) => (
        <Tag>{grants.length}</Tag>
      ),
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
          <Button
            size="small"
            icon={<EditOutlined />}
            onClick={() => openEdit(template)}
          >
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
              const result = await deleteAuthorizationTemplate({
                path: { templateId: template.id },
              });
              if (result.error) setError(result.error);
              else await load();
            }}
            okText={text("删除", "Delete")}
            cancelText={text("取消", "Cancel")}
          >
            <Button
              danger
              size="small"
              icon={<DeleteOutlined />}
              aria-label={text("删除模板", "Delete template")}
            />
          </Popconfirm>
        </Space>
      ),
    },
  ];

  return (
    <Card bodyClassName="p-0">
      <CardHeader
        title={text("授权模板", "Authorization templates")}
        extra={
          <Space>
            <Dropdown
              trigger={["click"]}
              menu={{
                items: presetMenuItems,
                onClick: ({ key }) => applyPreset(String(key)),
              }}
            >
              <Button icon={<AppstoreOutlined />}>
                {text("内置模板", "Built-in templates")} <DownOutlined />
              </Button>
            </Dropdown>
            <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>
              {text("新建模板", "New template")}
            </Button>
          </Space>
        }
      />
      <div className="px-5 py-3 text-xs text-zinc-500">
        <SafetyCertificateOutlined className="mr-2 text-cyan-400" />
        {text(
          "模板只保存可复用规则，不会自动修改仓库；应用时会检查仓库当前版本。",
          "Templates store reusable rules only. Applying one updates a repository with optimistic version checks.",
        )}
      </div>
      {error ? (
        <div className="px-5 pb-4">
          <ErrorBanner error={error} onRetry={load} />
        </div>
      ) : !templates ? (
        <Loading />
      ) : templates.length === 0 ? (
        <EmptyState
          compact
          title={text("尚未创建授权模板", "No authorization templates yet")}
          hint={text(
            "模板适合复用一组主体、权限和资源前缀规则。",
            "Templates reuse a set of principal, scope, and resource-prefix rules.",
          )}
        />
      ) : (
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
        title={
          editing
            ? text("编辑授权模板", "Edit authorization template")
            : text("新建授权模板", "New authorization template")
        }
        onCancel={() => setEditorOpen(false)}
        destroyOnHidden
        width={980}
        footer={
          <Space>
            <Button onClick={() => setEditorOpen(false)} disabled={saving}>
              {text("取消", "Cancel")}
            </Button>
            <Button type="primary" loading={saving} onClick={() => void save()}>
              {text("保存", "Save")}
            </Button>
          </Space>
        }
      >
        {saveError !== null && (
          <div className="mb-3">
            <ErrorBanner error={saveError} />
          </div>
        )}
        <div className="grid grid-cols-[minmax(220px,1fr)_minmax(320px,2fr)] gap-3">
          <Input
            placeholder={text("模板名称", "Template name")}
            value={draftName}
            onChange={(event) => setDraftName(event.target.value)}
          />
          <Input
            placeholder={text("用途描述（可选）", "Description (optional)")}
            value={draftDescription}
            onChange={(event) => setDraftDescription(event.target.value)}
          />
        </div>
        <div className="mt-3 text-xs leading-5 text-zinc-500">
          {text(
            "主体支持选择已知用户或 API Key，也可以直接填写 OIDC / CI actor。资源前缀留空表示整个仓库；下拉建议按仓库格式提供常见写法。",
            "Choose a known user or API key, or enter an OIDC / CI actor directly. Leave the resource prefix blank for the entire repository; suggestions follow the repository format.",
          )}
        </div>
        <Table<DraftGrant>
          className="mt-4"
          rowKey="key"
          dataSource={draftGrants}
          pagination={false}
          size="small"
          scroll={{ x: 760, y: 300 }}
          columns={[
            {
              title: text("主体", "Principal"),
              key: "principal",
              render: (_, grant) => (
                <AutoComplete
                  className="w-full"
                  allowClear
                  options={principalOptions}
                  value={grant.principal}
                  placeholder={text(
                    "选择或输入 actor，例如 user:release",
                    "Choose or enter an actor, e.g. user:release",
                  )}
                  onChange={(value) =>
                    setDraftGrants((current) =>
                      current.map((item) =>
                        item.key === grant.key
                          ? { ...item, principal: value }
                          : item,
                      ),
                    )
                  }
                />
              ),
            },
            {
              title: text("权限", "Scopes"),
              key: "scopes",
              width: 280,
              render: (_, grant) => {
                const role = roles.find((item) => item.id === grant.roleId);
                return (
                  <div className="space-y-2">
                    <Select
                      className="w-full"
                      showSearch={{ optionFilterProp: "label" }}
                      value={grant.roleId ?? CUSTOM_ROLE}
                      options={[
                        {
                          value: CUSTOM_ROLE,
                          label: text("自定义权限", "Custom permissions"),
                        },
                        ...roles.map((item) => ({
                          value: item.id,
                          label: item.name,
                        })),
                      ]}
                      onChange={(value) =>
                        setDraftGrants((current) =>
                          current.map((item) => {
                            if (item.key !== grant.key) return item;
                            if (value === CUSTOM_ROLE)
                              return { ...item, roleId: undefined };
                            const selected = roles.find(
                              (candidate) => candidate.id === value,
                            );
                            return selected
                              ? {
                                  ...item,
                                  roleId: selected.id,
                                  scopes: [...selected.scopes],
                                }
                              : item;
                          }),
                        )
                      }
                    />
                    {role ? (
                      <div className="flex flex-wrap gap-1">
                        {role.scopes.map((scope) => (
                          <Tag key={scope} className="m-0 text-xs">
                            {scope.replace("repositories:", "")}
                          </Tag>
                        ))}
                      </div>
                    ) : (
                      <Select
                        className="w-full"
                        mode="multiple"
                        maxTagCount="responsive"
                        value={grant.scopes}
                        options={[
                          {
                            value: "repositories:read",
                            label: text("读取", "Read"),
                          },
                          {
                            value: "repositories:write",
                            label: text("写入", "Write"),
                          },
                          {
                            value: "repositories:admin",
                            label: text("管理员", "Admin"),
                          },
                          {
                            value: "repositories:intelligence",
                            label: text("制品情报", "Artifact intelligence"),
                          },
                        ]}
                        onChange={(value) =>
                          setDraftGrants((current) =>
                            current.map((item) =>
                              item.key === grant.key
                                ? { ...item, scopes: value }
                                : item,
                            ),
                          )
                        }
                      />
                    )}
                  </div>
                );
              },
            },
            {
              title: text("资源前缀", "Resource prefix"),
              key: "prefix",
              width: 420,
              render: (_, grant) => (
                <ResourcePrefixEditor
                  format={
                    grant.selectorFormat ?? availableFormats[0] ?? "maven"
                  }
                  value={grant.resourcePrefix}
                  formats={availableFormats}
                  onFormatChange={(format) =>
                    setDraftGrants((current) =>
                      current.map((item) =>
                        item.key === grant.key
                          ? { ...item, selectorFormat: format }
                          : item,
                      ),
                    )
                  }
                  onChange={(value) =>
                    setDraftGrants((current) =>
                      current.map((item) =>
                        item.key === grant.key
                          ? { ...item, resourcePrefix: value }
                          : item,
                      ),
                    )
                  }
                />
              ),
            },
            {
              title: text("操作", "Actions"),
              key: "actions",
              width: 70,
              render: (_, grant) => (
                <Button
                  danger
                  type="text"
                  icon={<DeleteOutlined />}
                  aria-label={text("移除规则", "Remove rule")}
                  onClick={() =>
                    setDraftGrants((current) =>
                      current.filter((item) => item.key !== grant.key),
                    )
                  }
                />
              ),
            },
          ]}
        />
        <Button
          className="mt-3"
          icon={<PlusOutlined />}
          onClick={() =>
            setDraftGrants((current) => [...current, emptyGrant()])
          }
        >
          {text("添加规则", "Add rule")}
        </Button>
      </Modal>
      <Modal
        open={Boolean(applyTarget)}
        title={text("应用授权模板", "Apply authorization template")}
        onCancel={() => setApplyTarget(null)}
        destroyOnHidden
        footer={
          <Space>
            <Button onClick={() => setApplyTarget(null)} disabled={applying}>
              {text("取消", "Cancel")}
            </Button>
            <Button
              type="primary"
              loading={applying}
              disabled={!applyRepository}
              onClick={() => void apply()}
            >
              {text("应用", "Apply")}
            </Button>
          </Space>
        }
      >
        {applyError !== null && (
          <div className="mb-3">
            <ErrorBanner error={applyError} />
          </div>
        )}
        <p className="mb-3 text-sm text-zinc-500">
          {text(
            "选择目标仓库。应用会替换该仓库现有授权规则，并保留并发版本保护。",
            "Choose a target repository. Applying replaces its current grants with optimistic concurrency protection.",
          )}
        </p>
        <Select
          className="w-full"
          showSearch={{ optionFilterProp: "label" }}
          placeholder={text("选择仓库", "Select repository")}
          value={applyRepository || undefined}
          onChange={setApplyRepository}
          options={repositories
            .filter((repository) => repository.state === "active")
            .map((repository) => ({
              value: repository.id,
              label: `${repository.name} · ${repository.format}`,
            }))}
        />
      </Modal>
    </Card>
  );
}
