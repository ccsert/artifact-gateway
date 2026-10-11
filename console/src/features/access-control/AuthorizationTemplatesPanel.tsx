import type { AccessMessageKey } from "../../messages/access.zh-CN";
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
  name: AccessMessageKey;
  description: AccessMessageKey;
  grants: Array<Pick<DraftGrant, "principal" | "scopes" | "resourcePrefix">>;
};

export const AUTHORIZATION_TEMPLATE_PRESETS: TemplatePreset[] = [
  {
    key: "read-only-consumer",
    name: "access.teamReadOnly",

    description: "access.forPrincipalsThatOnlyDownloadSearchAndBrowseArtifacts",

    grants: [{ principal: "", scopes: ["repositories:read"] }],
  },
  {
    key: "release-publisher",
    name: "access.releasePublisher",

    description: "access.forCiPrincipalsThatPublishAndEditArtifacts",

    grants: [{ principal: "", scopes: ["repositories:write"] }],
  },
  {
    key: "security-bot",
    name: "access.artifactIntelligenceBot",

    description:
      "access.onlyAllowsSecurityAndArtifactIntelligenceMetadataWrites",

    grants: [{ principal: "", scopes: ["repositories:intelligence"] }],
  },
  {
    key: "repository-owner",
    name: "access.repositoryOwner",

    description:
      "access.forPrincipalsResponsibleForRepositoryConfigurationGrantsAndLifecycle",

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
  const { t } = usePreferences();
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
      name: t(preset.name),
      description: t(preset.description),
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
        <div className="text-xs font-medium">{t(preset.name)}</div>
        <div className="text-xs text-fg-tertiary">{t(preset.description)}</div>
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
      title: t("access.template"),
      key: "template",
      render: (_, template) => (
        <div>
          <Typography.Text strong>{template.name}</Typography.Text>
          <div className="text-xs text-fg-tertiary">
            {template.description || t("access.noDescription")}
          </div>
        </div>
      ),
    },
    {
      title: t("access.rules"),
      dataIndex: "grants",
      width: 100,
      render: (grants: AuthorizationTemplateGrant[]) => (
        <Tag>{grants.length}</Tag>
      ),
    },
    {
      title: t("access.updated"),
      dataIndex: "updatedAt",
      width: 190,
      render: (value: string) => new Date(value).toLocaleString(),
    },
    {
      title: t("access.actions"),
      key: "actions",
      width: 230,
      render: (_, template) => (
        <Space>
          <Button
            size="small"
            icon={<EditOutlined />}
            onClick={() => openEdit(template)}
          >
            {t("access.edit")}
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
            {t("access.apply")}
          </Button>
          <Popconfirm
            title={t("access.deleteThisTemplate")}
            onConfirm={async () => {
              const result = await deleteAuthorizationTemplate({
                path: { templateId: template.id },
              });
              if (result.error) setActionError(result.error);
              else await load();
            }}
            okText={t("access.delete")}
            cancelText={t("access.cancel")}
          >
            <Button
              danger
              size="small"
              icon={<DeleteOutlined />}
              aria-label={t("access.deleteTemplate")}
            />
          </Popconfirm>
        </Space>
      ),
    },
  ];

  return (
    <Card bodyClassName="p-0">
      <CardHeader
        title={t("access.authorizationTemplates")}
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
                {t("access.builtInTemplates")} <DownOutlined />
              </Button>
            </Dropdown>
            <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>
              {t("access.newTemplate")}
            </Button>
          </Space>
        }
      />
      <div className="px-5 py-3 text-xs text-fg-tertiary">
        <SafetyCertificateOutlined className="mr-2 text-[var(--ag-content-tertiary)]" />
        {t("access.templatesStoreReusableRulesOnlyApplyingOneUpdatesA")}
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
          title={t("access.noAuthorizationTemplatesYet")}
          hint={t("access.templatesReuseASetOfPrincipalScopeAndResource")}
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
