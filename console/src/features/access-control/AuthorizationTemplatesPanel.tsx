import { useMemo, useState } from "react";
import { Button, Dropdown, Popconfirm, Space, Tag, Typography } from "antd";
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
import { deleteAuthorizationTemplate } from "../../client";
import type {
  AuthorizationTemplate,
  AuthorizationTemplateGrant,
  Repository,
} from "../../client";
import { EmptyState, ErrorBanner, Loading } from "../../components/ui/Feedback";
import { Card, CardHeader } from "../../components/ui/Layout";
import { ConsoleTable } from "../../components/ui/ConsolePrimitives";
import { usePreferences } from "../../lib/preferences";
import { accessKeys } from "../../lib/query/keys";
import {
  useAuthorizationRoles,
  useAuthorizationTemplates,
  useInvalidateAccess,
} from "./accessQueries";
import { ApplyTemplateDialog } from "./ApplyTemplateDialog";
import { inferResourcePrefixFormat } from "./ResourcePrefixEditor";
import { emptyGrant, type DraftGrant } from "./grantDraft";
import {
  TemplateEditorDialog,
  type PrincipalOption,
  type TemplateDraft,
} from "./TemplateEditorDialog";

export type { PrincipalOption } from "./TemplateEditorDialog";

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
};

export function AuthorizationTemplatesPanel({
  repositories,
  principalOptions = [],
}: Props) {
  const { text } = usePreferences();
  const templatesQuery = useAuthorizationTemplates();
  const rolesQuery = useAuthorizationRoles();
  const templates = templatesQuery.data ?? null;
  const roles = rolesQuery.data ?? [];
  const invalidate = useInvalidateAccess();
  const [actionError, setActionError] = useState<unknown>(null);
  const error = actionError ?? templatesQuery.error ?? rolesQuery.error;
  // Each opening gets a new session so the dialog starts from a fresh draft
  // while a closing dialog keeps its content through the exit animation.
  const [editor, setEditor] = useState<{
    session: number;
    open: boolean;
    draft: TemplateDraft;
  } | null>(null);
  const [applying, setApplying] = useState<{
    session: number;
    open: boolean;
    template: AuthorizationTemplate;
  } | null>(null);

  const availableFormats = useMemo(
    () =>
      Array.from(new Set(repositories.map((repository) => repository.format))),
    [repositories],
  );

  const load = async () => {
    setActionError(null);
    await Promise.all([
      invalidate(accessKeys.templates()),
      invalidate(accessKeys.roles()),
    ]);
  };

  const openEditor = (draft: TemplateDraft) =>
    setEditor((current) => ({
      session: (current?.session ?? 0) + 1,
      open: true,
      draft,
    }));

  const openCreate = () =>
    openEditor({
      template: null,
      name: "",
      description: "",
      grants: [emptyGrant()],
    });

  const applyPreset = (presetKey: string) => {
    const preset = AUTHORIZATION_TEMPLATE_PRESETS.find(
      (item) => item.key === presetKey,
    );
    if (!preset) return;
    openEditor({
      template: null,
      name: text(preset.name, preset.nameEn),
      description: text(preset.description, preset.descriptionEn),
      grants: preset.grants.map((grant) => ({
        ...grant,
        key: `${Date.now()}-${Math.random()}`,
      })),
    });
  };

  const presetMenuItems = AUTHORIZATION_TEMPLATE_PRESETS.map((preset) => ({
    key: preset.key,
    label: (
      <div className="min-w-[220px]">
        <div className="text-xs font-medium">
          {text(preset.name, preset.nameEn)}
        </div>
        <div className="text-xs text-fg-tertiary">
          {text(preset.description, preset.descriptionEn)}
        </div>
      </div>
    ),
  }));

  const openEdit = (template: AuthorizationTemplate) =>
    openEditor({
      template,
      name: template.name,
      description: template.description ?? "",
      grants: toDraft(template, availableFormats),
    });

  const columns: ColumnsType<AuthorizationTemplate> = [
    {
      title: text("模板", "Template"),
      key: "template",
      render: (_, template) => (
        <div>
          <Typography.Text strong>{template.name}</Typography.Text>
          <div className="text-xs text-fg-tertiary">
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
            onClick={() =>
              setApplying((current) => ({
                session: (current?.session ?? 0) + 1,
                open: true,
                template,
              }))
            }
          >
            {text("应用", "Apply")}
          </Button>
          <Popconfirm
            title={text("删除此模板？", "Delete this template?")}
            onConfirm={async () => {
              const result = await deleteAuthorizationTemplate({
                path: { templateId: template.id },
              });
              if (result.error) setActionError(result.error);
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
      <div className="px-5 py-3 text-xs text-fg-tertiary">
        <SafetyCertificateOutlined className="mr-2 text-[var(--ag-content-tertiary)]" />
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
        <ConsoleTable<AuthorizationTemplate>
          rowKey="id"
          dataSource={templates}
          columns={columns}
          pagination={false}
          scroll={{ x: 900, y: 320 }}
        />
      )}
      {editor && (
        <TemplateEditorDialog
          key={editor.session}
          open={editor.open}
          draft={editor.draft}
          roles={roles}
          principalOptions={principalOptions}
          availableFormats={availableFormats}
          onClose={() =>
            setEditor((current) => current && { ...current, open: false })
          }
        />
      )}
      {applying && (
        <ApplyTemplateDialog
          key={applying.session}
          open={applying.open}
          template={applying.template}
          repositories={repositories}
          onClose={() =>
            setApplying((current) => current && { ...current, open: false })
          }
        />
      )}
    </Card>
  );
}
