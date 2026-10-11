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
  const { t } = usePreferences();
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
        new Error(t("access.templateNameAndEveryGrantPrincipalAreRequired")),
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
        new Error(t("access.principalAndResourcePrefixPairsMustBeUnique")),
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
            new Error(t("access.failedToSaveAuthorizationTemplate")),
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
          ? t("access.editAuthorizationTemplate")
          : t("access.newAuthorizationTemplate")
      }
      onClose={() => onClose()}
      wide
      busy={saving}
      footer={
        <Space>
          <Button onClick={() => onClose()} disabled={saving}>
            {t("access.cancel")}
          </Button>
          <Button type="primary" loading={saving} onClick={() => void save()}>
            {t("access.save")}
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
          placeholder={t("access.templateName")}
          value={draftName}
          onChange={(event) => setDraftName(event.target.value)}
        />
        <Input
          placeholder={t("access.descriptionOptional")}
          value={draftDescription}
          onChange={(event) => setDraftDescription(event.target.value)}
        />
      </div>
      <div className="mt-3 text-xs leading-5 text-fg-tertiary">
        {t("access.chooseAKnownUserOrApiKeyOrEnter")}
      </div>
      <ConsoleTable<DraftGrant>
        rowKey="key"
        dataSource={draftGrants}
        pagination={false}
        size="small"
        scroll={{ x: 760 }}
        columns={[
          {
            title: t("access.principal2"),
            key: "principal",
            render: (_, grant) => (
              <AutoComplete
                className="w-full"
                allowClear
                options={principalOptions}
                value={grant.principal}
                placeholder={t("access.chooseOrEnterAnActorEGUserRelease")}
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
            title: t("access.scopes"),
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
                        label: t("access.customPermissions"),
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
                          label: t("access.read"),
                        },
                        {
                          value: "repositories:write",
                          label: t("access.write"),
                        },
                        {
                          value: "repositories:admin",
                          label: t("access.admin2"),
                        },
                        {
                          value: "repositories:intelligence",
                          label: t("access.artifactIntelligence"),
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
            title: t("access.resourcePrefix"),
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
            title: t("access.actions"),
            key: "actions",
            width: 70,
            render: (_, grant) => (
              <Button
                danger
                type="text"
                icon={<DeleteOutlined />}
                aria-label={t("access.removeRule")}
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
        {t("access.addRule")}
      </Button>
    </Modal>
  );
}
