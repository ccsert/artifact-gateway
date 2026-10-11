import { useMemo, useState } from "react";
import { AutoComplete, Button, Input, Select, Space, Tag } from "antd";
import { DeleteOutlined, PlusOutlined } from "@ant-design/icons";
import {
  createAuthorizationTemplate,
  updateAuthorizationTemplate,
} from "../../client";
import type {
  AuthorizationRole,
  AuthorizationTemplate,
  Repository,
} from "../../client";
import { ErrorBanner } from "../../components/ui/Feedback";
import { ConsoleTable } from "../../components/ui/ConsolePrimitives";
import { Modal } from "../../components/ui/Modal";
import { usePreferences } from "../../lib/preferences";
import { accessKeys } from "../../lib/query/keys";
import { useInvalidateAccess } from "./accessQueries";
import { ResourcePrefixEditor } from "./ResourcePrefixEditor";
import { emptyGrant, scopesForRole, type DraftGrant } from "./grantDraft";

export type PrincipalOption = { value: string; label: string };

const CUSTOM_ROLE = "__custom__";

/** What the editor opens with: an existing template, a preset, or nothing. */
export interface TemplateDraft {
  template: AuthorizationTemplate | null;
  name: string;
  description: string;
  grants: DraftGrant[];
}

/**
 * Creates or edits one template. Mount it with a new `key` per opening so the
 * draft starts from `draft` every time.
 */
export function TemplateEditorDialog({
  open,
  draft,
  roles,
  principalOptions,
  availableFormats,
  onClose,
}: {
  open: boolean;
  draft: TemplateDraft;
  roles: AuthorizationRole[];
  principalOptions: PrincipalOption[];
  availableFormats: Repository["format"][];
  onClose: () => void;
}) {
  const { text } = usePreferences();
  const invalidate = useInvalidateAccess();
  const editing = draft.template;
  const [draftName, setDraftName] = useState(draft.name);
  const [draftDescription, setDraftDescription] = useState(draft.description);
  const [draftGrants, setDraftGrants] = useState<DraftGrant[]>(draft.grants);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<unknown>(null);

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
    try {
      const result = editing
        ? await updateAuthorizationTemplate({
            path: { templateId: editing.id },
            headers: { "If-Match": editing.version },
            body,
          })
        : await createAuthorizationTemplate({ body });
      if (result.error || !result.data) {
        setSaveError(
          result.error ??
            new Error(
              text("保存授权模板失败", "Failed to save authorization template"),
            ),
        );
        return;
      }
      await invalidate(accessKeys.templates());
      onClose();
    } catch (nextError) {
      setSaveError(nextError);
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal
      open={open}
      title={
        editing
          ? text("编辑授权模板", "Edit authorization template")
          : text("新建授权模板", "New authorization template")
      }
      onClose={() => onClose()}
      wide
      busy={saving}
      footer={
        <Space>
          <Button onClick={() => onClose()} disabled={saving}>
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
      <div className="mt-3 text-xs leading-5 text-fg-tertiary">
        {text(
          "主体支持选择已知用户或 API Key，也可以直接填写 OIDC / CI actor。资源前缀留空表示整个仓库；下拉建议按仓库格式提供常见写法。",
          "Choose a known user or API key, or enter an OIDC / CI actor directly. Leave the resource prefix blank for the entire repository; suggestions follow the repository format.",
        )}
      </div>
      <ConsoleTable<DraftGrant>
        rowKey="key"
        dataSource={draftGrants}
        pagination={false}
        size="small"
        scroll={{ x: 760 }}
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
                                scopes: scopesForRole(selected),
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
                format={grant.selectorFormat ?? availableFormats[0] ?? "maven"}
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
        icon={<PlusOutlined />}
        onClick={() => setDraftGrants((current) => [...current, emptyGrant()])}
      >
        {text("添加规则", "Add rule")}
      </Button>
    </Modal>
  );
}
