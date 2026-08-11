import { useCallback, useEffect, useState } from "react";
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
} from "@ant-design/icons";
import {
  createAuthorizationRole,
  deleteAuthorizationRole,
  listAuthorizationRoles,
  updateAuthorizationRole,
} from "../client";
import type { AuthorizationRole } from "../client";
import { usePreferences } from "../lib/preferences";
import { EmptyState, ErrorBanner, Loading } from "./Feedback";
import { Card, CardHeader } from "./Layout";

export type AuthorizationScope = AuthorizationRole["scopes"][number];

type Props = {
  onChanged?: () => void;
};

export function AuthorizationRolesPanel({ onChanged }: Props) {
  const { text } = usePreferences();
  const [roles, setRoles] = useState<AuthorizationRole[] | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [editorOpen, setEditorOpen] = useState(false);
  const [editing, setEditing] = useState<AuthorizationRole | null>(null);
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [scopes, setScopes] = useState<AuthorizationScope[]>([]);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState<unknown>(null);

  const scopeOptions: Array<{ value: AuthorizationScope; label: string }> = [
    { value: "repositories:read", label: text("读取", "Read") },
    { value: "repositories:write", label: text("写入", "Write") },
    {
      value: "repositories:intelligence",
      label: text("制品情报", "Artifact intelligence"),
    },
    { value: "repositories:admin", label: text("管理", "Admin") },
  ];

  const load = useCallback(async () => {
    setError(null);
    const result = await listAuthorizationRoles();
    if (result.error || !result.data) {
      setError(
        result.error ??
          new Error(
            text("加载授权角色失败", "Failed to load authorization roles"),
          ),
      );
      return;
    }
    setRoles(result.data);
  }, [text]);

  useEffect(() => {
    void load();
  }, [load]);

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
        new Error(
          text(
            "角色名称和至少一项权限不能为空",
            "Role name and at least one permission are required",
          ),
        ),
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
    const result = editing
      ? await updateAuthorizationRole({
          path: { roleId: editing.id },
          headers: { "If-Match": editing.version },
          body,
        })
      : await createAuthorizationRole({ body });
    setSaving(false);
    if (result.error || !result.data) {
      setSaveError(
        result.error ??
          new Error(
            text("保存授权角色失败", "Failed to save authorization role"),
          ),
      );
      return;
    }
    setEditorOpen(false);
    await load();
    onChanged?.();
  };

  const columns: ColumnsType<AuthorizationRole> = [
    {
      title: text("角色", "Role"),
      key: "role",
      render: (_, role) => (
        <div>
          <Typography.Text strong>{role.name}</Typography.Text>
          <div className="text-xs text-zinc-500">
            {role.description || text("无描述", "No description")}
          </div>
        </div>
      ),
    },
    {
      title: text("权限", "Permissions"),
      dataIndex: "scopes",
      width: 380,
      render: (values: AuthorizationScope[]) => (
        <Space size={[4, 4]} wrap>
          {values.map((scope) => (
            <Tag key={scope} className="font-mono text-[11px]">
              {scope.replace("repositories:", "")}
            </Tag>
          ))}
        </Space>
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
      width: 120,
      render: (_, role) => (
        <Space>
          <Button
            type="text"
            icon={<EditOutlined />}
            aria-label={text("编辑角色", "Edit role")}
            onClick={() => openEdit(role)}
          />
          <Popconfirm
            title={text("删除此角色？", "Delete this role?")}
            description={text(
              "已保存的模板和仓库规则不会改变。",
              "Saved templates and repository grants will not change.",
            )}
            okText={text("删除", "Delete")}
            cancelText={text("取消", "Cancel")}
            okButtonProps={{ danger: true }}
            onConfirm={async () => {
              const result = await deleteAuthorizationRole({
                path: { roleId: role.id },
              });
              if (result.error) {
                setError(result.error);
                return;
              }
              await load();
              onChanged?.();
            }}
          >
            <Button
              danger
              type="text"
              icon={<DeleteOutlined />}
              aria-label={text("删除角色", "Delete role")}
            />
          </Popconfirm>
        </Space>
      ),
    },
  ];

  return (
    <Card bodyClassName="p-0">
      <CardHeader
        title={text("自定义授权角色", "Custom authorization roles")}
        extra={
          <Button type="primary" icon={<PlusOutlined />} onClick={openCreate}>
            {text("新建角色", "New role")}
          </Button>
        }
      />
      <div className="px-5 py-3 text-xs text-zinc-500">
        <SafetyCertificateOutlined className="mr-2 text-cyan-400" />
        {text(
          "角色复用一组权限。选入模板或授权规则时会复制权限快照，之后修改或删除角色不会改变已有授权。",
          "Roles reuse permission sets. Templates and grants copy a scope snapshot, so later role edits or deletion never change saved access.",
        )}
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
          title={text("尚未创建自定义角色", "No custom roles yet")}
          hint={text(
            "把经常一起授予的权限保存成角色，编辑模板时可以直接复用。",
            "Save permissions that are commonly granted together and reuse them while editing templates.",
          )}
        />
      ) : (
        <Table<AuthorizationRole>
          className="ag-console-table"
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
            ? text("编辑授权角色", "Edit authorization role")
            : text("新建授权角色", "New authorization role")
        }
        onCancel={() => setEditorOpen(false)}
        destroyOnHidden
        width={680}
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
          <div className="mb-4">
            <ErrorBanner error={saveError} />
          </div>
        )}
        <div className="grid grid-cols-[120px_minmax(0,1fr)] items-start gap-x-4 gap-y-4">
          <label
            htmlFor="authorization-role-name"
            className="pt-2 text-right text-sm text-zinc-500"
          >
            {text("角色名称", "Role name")}
          </label>
          <Input
            id="authorization-role-name"
            value={name}
            maxLength={128}
            placeholder={text(
              "例如 发布审核员",
              "For example Release reviewer",
            )}
            onChange={(event) => setName(event.target.value)}
          />
          <label
            htmlFor="authorization-role-description"
            className="pt-2 text-right text-sm text-zinc-500"
          >
            {text("用途描述", "Description")}
          </label>
          <Input.TextArea
            id="authorization-role-description"
            value={description}
            maxLength={1000}
            autoSize={{ minRows: 2, maxRows: 4 }}
            placeholder={text(
              "说明该角色适用的职责",
              "Describe the responsibility this role serves",
            )}
            onChange={(event) => setDescription(event.target.value)}
          />
          <label
            htmlFor="authorization-role-scopes"
            className="pt-2 text-right text-sm text-zinc-500"
          >
            {text("仓库权限", "Permissions")}
          </label>
          <Select<AuthorizationScope[]>
            id="authorization-role-scopes"
            mode="multiple"
            value={scopes}
            options={scopeOptions}
            maxTagCount="responsive"
            placeholder={text(
              "选择一项或多项权限",
              "Select one or more permissions",
            )}
            onChange={setScopes}
          />
        </div>
      </Modal>
    </Card>
  );
}
