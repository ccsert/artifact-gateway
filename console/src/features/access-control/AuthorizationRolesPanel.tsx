import { useState } from "react";
import {
  Button,
  Input,
  Popconfirm,
  Select,
  Space,
  Tag,
  Typography,
} from "antd";
import type { ColumnsType } from "antd/es/table";
import {
  DeleteOutlined,
  EditOutlined,
  PlusOutlined,
  SafetyCertificateOutlined,
} from "@ant-design/icons";
import {
  createAuthorizationRole,
  deleteAuthorizationRole,
  updateAuthorizationRole,
} from "../../client";
import type { AuthorizationRole } from "../../client";
import { usePreferences } from "../../lib/preferences";
import { EmptyState, ErrorBanner, Loading } from "../../components/ui/Feedback";
import { Card, CardHeader } from "../../components/ui/Layout";
import { ConsoleTable } from "../../components/ui/ConsolePrimitives";
import { Modal } from "../../components/ui/Modal";
import { accessKeys } from "../../lib/query/keys";
import { useAuthorizationRoles, useInvalidateAccess } from "./accessQueries";

export type AuthorizationScope = AuthorizationRole["scopes"][number];

/** Custom roles, shared with the templates panel through the roles query. */
export function AuthorizationRolesPanel() {
  const { t } = usePreferences();
  const rolesQuery = useAuthorizationRoles();
  const roles = rolesQuery.data ?? null;
  const invalidate = useInvalidateAccess();
  const [actionError, setActionError] = useState<unknown>(null);
  const error = actionError ?? rolesQuery.error;
  const [editorOpen, setEditorOpen] = useState(false);
  const [editing, setEditing] = useState<AuthorizationRole | null>(null);
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [scopes, setScopes] = useState<AuthorizationScope[]>([]);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<unknown>(null);

  const scopeOptions: Array<{ value: AuthorizationScope; label: string }> = [
    { value: "repositories:read", label: t("access.read") },
    { value: "repositories:write", label: t("access.write") },
    {
      value: "repositories:intelligence",
      label: t("access.artifactIntelligence"),
    },
    { value: "repositories:admin", label: t("access.admin") },
  ];

  const load = async () => {
    setActionError(null);
    await invalidate(accessKeys.roles());
  };

  const openCreate = () => {
    setEditing(null);
    setName("");
    setDescription("");
    setScopes(["repositories:read"]);
    setSaveError(null);
    setEditorOpen(true);
  };

  const openEdit = (role: AuthorizationRole) => {
    setEditing(role);
    setName(role.name);
    setDescription(role.description ?? "");
    setScopes([...role.scopes]);
    setSaveError(null);
    setEditorOpen(true);
  };

  const save = async () => {
    if (!name.trim() || scopes.length === 0) {
      setSaveError(
        new Error(t("access.roleNameAndAtLeastOnePermissionAreRequired")),
      );
      return;
    }
    setSaving(true);
    setSaveError(null);
    const body = {
      name: name.trim(),
      description: description.trim() || undefined,
      scopes,
    };
    try {
      const result = editing
        ? await updateAuthorizationRole({
            path: { roleId: editing.id },
            headers: { "If-Match": editing.version },
            body,
          })
        : await createAuthorizationRole({ body });
      if (result.error || !result.data) {
        setSaveError(
          result.error ?? new Error(t("access.failedToSaveAuthorizationRole")),
        );
        return;
      }
      setEditorOpen(false);
      await load();
    } catch (nextError) {
      setSaveError(nextError);
    } finally {
      setSaving(false);
    }
  };

  const columns: ColumnsType<AuthorizationRole> = [
    {
      title: t("access.role"),
      key: "role",
      render: (_, role) => (
        <div>
          <Typography.Text strong>{role.name}</Typography.Text>
          <div className="text-xs text-fg-tertiary">
            {role.description || t("access.noDescription")}
          </div>
        </div>
      ),
    },
    {
      title: t("access.permissions"),
      dataIndex: "scopes",
      width: 380,
      render: (values: AuthorizationScope[]) => (
        <Space size={[4, 4]} wrap>
          {values.map((scope) => (
            <Tag key={scope} className="font-mono text-xs">
              {scope.replace("repositories:", "")}
            </Tag>
          ))}
        </Space>
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
      width: 120,
      render: (_, role) => (
        <Space>
          <Button
            type="text"
            icon={<EditOutlined />}
            aria-label={t("access.editRole")}
            onClick={() => openEdit(role)}
          />
          <Popconfirm
            title={t("access.deleteThisRole")}
            description={t(
              "access.savedTemplatesAndRepositoryGrantsWillNotChange",
            )}
            okText={t("access.delete")}
            cancelText={t("access.cancel")}
            okButtonProps={{ danger: true }}
            onConfirm={async () => {
              const result = await deleteAuthorizationRole({
                path: { roleId: role.id },
              });
              if (result.error) {
                setActionError(result.error);
                return;
              }
              await load();
            }}
          >
            <Button
              danger
              type="text"
              icon={<DeleteOutlined />}
              aria-label={t("access.deleteRole")}
            />
          </Popconfirm>
        </Space>
      ),
    },
  ];

  return (
    <Card bodyClassName="p-0">
      <CardHeader
        title={t("access.customAuthorizationRoles")}
        extra={
          <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>
            {t("access.newRole")}
          </Button>
        }
      />
      <div className="px-5 py-3 text-xs text-fg-tertiary">
        <SafetyCertificateOutlined className="mr-2 text-[var(--ag-content-tertiary)]" />
        {t("access.rolesReusePermissionSetsTemplatesAndGrantsCopyA")}
      </div>
      {error ? (
        <div className="px-5 pb-4">
          <ErrorBanner error={error} onRetry={load} />
        </div>
      ) : !roles ? (
        <Loading />
      ) : roles.length === 0 ? (
        <EmptyState
          compact
          title={t("access.noCustomRolesYet")}
          hint={t(
            "access.savePermissionsThatAreCommonlyGrantedTogetherAndReuse",
          )}
        />
      ) : (
        <ConsoleTable<AuthorizationRole>
          rowKey="id"
          dataSource={roles}
          columns={columns}
          pagination={false}
          scroll={{ x: 900, y: 280 }}
        />
      )}
      <Modal
        open={editorOpen}
        title={
          editing
            ? t("access.editAuthorizationRole")
            : t("access.newAuthorizationRole")
        }
        onClose={() => setEditorOpen(false)}
        width={680}
        busy={saving}
        footer={
          <Space>
            <Button onClick={() => setEditorOpen(false)} disabled={saving}>
              {t("access.cancel")}
            </Button>
            <Button type="primary" loading={saving} onClick={() => void save()}>
              {t("access.save")}
            </Button>
          </Space>
        }
      >
        {saveError !== null && (
          <div className="mb-4">
            <ErrorBanner error={saveError} />
          </div>
        )}
        <div className="grid grid-cols-[120px_minmax(0,1fr)] items-start gap-x-4 gap-y-4">
          <label
            htmlFor="authorization-role-name"
            className="pt-2 text-right text-sm text-fg-tertiary"
          >
            {t("access.roleName")}
          </label>
          <Input
            id="authorization-role-name"
            value={name}
            maxLength={128}
            placeholder={t("access.forExampleReleaseReviewer")}
            onChange={(event) => setName(event.target.value)}
          />
          <label
            htmlFor="authorization-role-description"
            className="pt-2 text-right text-sm text-fg-tertiary"
          >
            {t("access.description")}
          </label>
          <Input.TextArea
            id="authorization-role-description"
            value={description}
            maxLength={1000}
            autoSize={{ minRows: 2, maxRows: 4 }}
            placeholder={t("access.describeTheResponsibilityThisRoleServes")}
            onChange={(event) => setDescription(event.target.value)}
          />
          <label
            htmlFor="authorization-role-scopes"
            className="pt-2 text-right text-sm text-fg-tertiary"
          >
            {t("access.permissions2")}
          </label>
          <Select<AuthorizationScope[]>
            id="authorization-role-scopes"
            mode="multiple"
            value={scopes}
            options={scopeOptions}
            maxTagCount="responsive"
            placeholder={t("access.selectOneOrMorePermissions")}
            onChange={setScopes}
          />
        </div>
      </Modal>
    </Card>
  );
}
