import { useCallback, useEffect, useState } from "react";
import {
  DeleteOutlined,
  LinkOutlined,
  PlusOutlined,
  ReloadOutlined,
} from "@ant-design/icons";
import {
  Alert,
  App,
  Button,
  Empty,
  Form,
  Input,
  Modal,
  Popconfirm,
  Space,
  Spin,
  Tag,
} from "antd";
import {
  createUserIdentity,
  deleteUserIdentity,
  getOidcSettings,
  listUserIdentities,
} from "../../client";
import type { CreateUserIdentity, UserIdentity } from "../../client";
import { ErrorBanner } from "../../components/Feedback";
import { formatDate } from "../../lib/format";
import { usePreferences } from "../../lib/preferences";

interface UserIdentitiesPanelProps {
  userId: string;
}

export function UserIdentitiesPanel({ userId }: UserIdentitiesPanelProps) {
  const { message } = App.useApp();
  const { locale, text } = usePreferences();
  const [items, setItems] = useState<UserIdentity[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<unknown>(null);
  const [open, setOpen] = useState(false);
  const [issuer, setIssuer] = useState("");
  const [subject, setSubject] = useState("");
  const [busy, setBusy] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    const [identitiesResult, settingsResult] = await Promise.all([
      listUserIdentities({ path: { userId } }),
      getOidcSettings(),
    ]);
    setLoading(false);
    if (identitiesResult.error || !identitiesResult.data) {
      setError(
        identitiesResult.error ??
          new Error(text("加载身份失败", "Failed to load identities")),
      );
      return;
    }
    setItems(identitiesResult.data.items);
    setIssuer(settingsResult.data?.issuer ?? "");
  }, [text, userId]);

  useEffect(() => {
    void load();
  }, [load]);

  const link = async () => {
    const body: CreateUserIdentity = {
      issuer: issuer.trim(),
      subject: subject.trim(),
    };
    if (!body.issuer || !body.subject) return;
    setBusy(true);
    setError(null);
    const { data, error: requestError } = await createUserIdentity({
      path: { userId },
      body,
    });
    setBusy(false);
    if (requestError || !data) {
      setError(
        requestError ??
          new Error(text("绑定身份失败", "Failed to link identity")),
      );
      return;
    }
    setItems((current) => [...current, data]);
    setSubject("");
    setOpen(false);
    void message.success(text("身份已绑定", "Identity linked"));
  };

  const unlink = async (identity: UserIdentity) => {
    setError(null);
    const { error: requestError } = await deleteUserIdentity({
      path: { userId, identityId: identity.id },
    });
    if (requestError) {
      setError(requestError);
      return;
    }
    setItems((current) => current.filter((item) => item.id !== identity.id));
    void message.success(text("身份已解绑", "Identity unlinked"));
  };

  return (
    <section>
      <div className="mb-3 flex items-center justify-between gap-3">
        <div>
          <h2 className="text-sm font-semibold text-zinc-100">
            {text("外部身份", "External identities")}
          </h2>
          <p className="mt-1 text-xs text-zinc-500">
            {text(
              "绑定后，OIDC 登录会使用此账户的状态、角色和会话撤销策略。",
              "Linked OIDC sign-ins use this account's state, role, and session revocation policy.",
            )}
          </p>
        </div>
        <Space size={6}>
          <Button
            size="small"
            icon={<ReloadOutlined />}
            aria-label={text("刷新外部身份", "Refresh external identities")}
            onClick={() => void load()}
          />
          <Button
            size="small"
            type="primary"
            icon={<PlusOutlined />}
            onClick={() => {
              setError(null);
              setSubject("");
              setOpen(true);
            }}
          >
            {text("绑定身份", "Link identity")}
          </Button>
        </Space>
      </div>

      {error ? <ErrorBanner error={error} onRetry={() => void load()} /> : null}
      {loading ? (
        <div className="flex justify-center py-6">
          <Spin />
        </div>
      ) : items.length === 0 ? (
        <Empty
          image={Empty.PRESENTED_IMAGE_SIMPLE}
          description={text("尚未绑定外部身份", "No external identity linked")}
        />
      ) : (
        <div className="space-y-2">
          {items.map((identity) => (
            <div
              key={identity.id}
              className="flex items-start justify-between gap-3 border border-zinc-800/80 bg-zinc-950/20 px-3 py-3"
            >
              <div className="min-w-0">
                <div className="flex items-center gap-2">
                  <LinkOutlined className="text-cyan-400" />
                  <Tag color="blue">OIDC</Tag>
                  {identity.emailVerified ? (
                    <Tag color="green">
                      {text("邮箱已验证", "Email verified")}
                    </Tag>
                  ) : null}
                </div>
                <div
                  className="mt-2 truncate text-xs text-zinc-400"
                  title={identity.issuer}
                >
                  {identity.issuer}
                </div>
                <div className="mt-1 break-all font-mono text-xs text-zinc-200">
                  {identity.subject}
                </div>
                <div className="mt-2 text-xs text-zinc-500">
                  {text("最近登录", "Last sign-in")}：
                  {identity.lastLoginAt
                    ? formatDate(identity.lastLoginAt, locale)
                    : text("尚未登录", "Never")}
                </div>
              </div>
              <Popconfirm
                title={text("解绑这个身份？", "Unlink this identity?")}
                description={text(
                  "解绑后，该 OIDC 身份不能再进入此账户。",
                  "This OIDC identity will no longer enter this account.",
                )}
                okText={text("解绑", "Unlink")}
                cancelText={text("取消", "Cancel")}
                okButtonProps={{ danger: true }}
                onConfirm={() => unlink(identity)}
              >
                <Button
                  size="small"
                  danger
                  type="text"
                  icon={<DeleteOutlined />}
                  aria-label={text("解绑身份", "Unlink identity")}
                />
              </Popconfirm>
            </div>
          ))}
        </div>
      )}

      <Modal
        open={open}
        title={text("绑定 OIDC 身份", "Link OIDC identity")}
        confirmLoading={busy}
        okText={text("绑定", "Link")}
        cancelText={text("取消", "Cancel")}
        okButtonProps={{ disabled: !issuer.trim() || !subject.trim() }}
        onOk={() => void link()}
        onCancel={() => setOpen(false)}
      >
        <Alert
          className="mb-4"
          type="info"
          showIcon
          title={text(
            "身份提供方已从当前 OIDC 配置读取。",
            "The identity provider is read from the current OIDC configuration.",
          )}
          description={
            issuer
              ? `${text("Issuer", "Issuer")}: ${issuer}`
              : text(
                  "尚未配置 OIDC 提供方，暂时无法绑定身份。",
                  "No OIDC provider is configured, so identities cannot be linked yet.",
                )
          }
        />
        <Form layout="vertical">
          <Form.Item label="Issuer">
            <Input value={issuer} readOnly aria-label="Issuer" />
          </Form.Item>
          <Form.Item
            label="Subject"
            extra={text(
              "填写 OIDC ID Token 中的 sub 值，这是提供方用户的稳定唯一标识。",
              "Enter the sub claim from the OIDC ID Token, the provider's stable unique user identifier.",
            )}
            required
          >
            <Input
              value={subject}
              onChange={(event) => setSubject(event.target.value)}
              placeholder={text("例如：00u123…", "For example: 00u123…")}
              aria-label="Subject"
            />
          </Form.Item>
        </Form>
      </Modal>
    </section>
  );
}
