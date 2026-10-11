import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import {
  DeleteOutlined,
  LinkOutlined,
  PlusOutlined,
  ReloadOutlined,
} from "@ant-design/icons";
import { Alert, App, Button, Form, Input, Popconfirm, Space, Tag } from "antd";
import { createUserIdentity, deleteUserIdentity } from "../../../client";
import type {
  CreateUserIdentity,
  ListUserIdentitiesResponse,
  UserIdentity,
} from "../../../client";
import { Badge } from "../../../components/ui/Badge";
import {
  EmptyState,
  ErrorBanner,
  Loading,
} from "../../../components/ui/Feedback";
import { Modal } from "../../../components/ui/Modal";
import { formatDate } from "../../../lib/format";
import { usePreferences } from "../../../lib/preferences";
import { identityKeys } from "../../../lib/query/keys";
import { useOidcIssuer, useUserIdentities } from "../userQueries";

interface UserIdentitiesPanelProps {
  userId: string;
}

export function UserIdentitiesPanel({ userId }: UserIdentitiesPanelProps) {
  const { message } = App.useApp();
  const { locale, text } = usePreferences();
  const queryClient = useQueryClient();
  const identitiesQuery = useUserIdentities(userId);
  const issuerQuery = useOidcIssuer();
  const issuer = issuerQuery.data ?? "";
  const items = identitiesQuery.data?.items ?? [];
  const loading = identitiesQuery.isFetching;
  const [actionError, setActionError] = useState<unknown>(null);
  const error = actionError ?? identitiesQuery.error;
  // A failed link belongs to the dialog that started it; the panel banner
  // stays reserved for loading the identity list.
  const [linkError, setLinkError] = useState<unknown>(null);
  const [open, setOpen] = useState(false);
  const [subject, setSubject] = useState("");
  const [busy, setBusy] = useState(false);
  const updateItems = (change: (items: UserIdentity[]) => UserIdentity[]) =>
    queryClient.setQueryData<ListUserIdentitiesResponse>(
      identityKeys.userIdentities(userId),
      (current) => current && { ...current, items: change(current.items) },
    );

  const load = () => {
    setActionError(null);
    return Promise.all([identitiesQuery.refetch(), issuerQuery.refetch()]);
  };

  const link = async () => {
    const body: CreateUserIdentity = {
      issuer: issuer.trim(),
      subject: subject.trim(),
    };
    if (!body.issuer || !body.subject) return;
    setBusy(true);
    setLinkError(null);
    try {
      const { data, error: requestError } = await createUserIdentity({
        path: { userId },
        body,
      });
      if (requestError || !data) {
        setLinkError(
          requestError ??
            new Error(text("绑定身份失败", "Failed to link identity")),
        );
        return;
      }
      updateItems((current) => [...current, data]);
      setSubject("");
      setOpen(false);
      void message.success(text("身份已绑定", "Identity linked"));
    } catch (nextError) {
      setLinkError(nextError);
    } finally {
      setBusy(false);
    }
  };

  const unlink = async (identity: UserIdentity) => {
    setActionError(null);
    const { error: requestError } = await deleteUserIdentity({
      path: { userId, identityId: identity.id },
    });
    if (requestError) {
      setActionError(requestError);
      return;
    }
    updateItems((current) => current.filter((item) => item.id !== identity.id));
    void message.success(text("身份已解绑", "Identity unlinked"));
  };

  return (
    <section>
      <div className="mb-3 flex items-center justify-between gap-3">
        <div>
          <h2 className="text-sm font-semibold text-fg-strong">
            {text("外部身份", "External identities")}
          </h2>
          <p className="mt-1 text-xs text-fg-tertiary">
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
              setActionError(null);
              setLinkError(null);
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
        <Loading />
      ) : items.length === 0 ? (
        <EmptyState
          compact
          title={text("尚未绑定外部身份", "No external identity linked")}
        />
      ) : (
        <div className="space-y-2">
          {items.map((identity) => (
            <div
              key={identity.id}
              className="flex items-start justify-between gap-3 border border-line bg-surface-translucent px-3 py-3"
            >
              <div className="min-w-0">
                <div className="flex items-center gap-2">
                  <LinkOutlined className="text-[var(--ag-link)]" />
                  <Badge tone="visualization-5">OIDC</Badge>
                  {identity.emailVerified ? (
                    <Tag color="success">
                      {text("邮箱已验证", "Email verified")}
                    </Tag>
                  ) : null}
                </div>
                <div
                  className="mt-2 truncate text-xs text-fg-secondary"
                  title={identity.issuer}
                >
                  {identity.issuer}
                </div>
                <div className="mt-1 break-all font-mono text-xs text-fg">
                  {identity.subject}
                </div>
                <div className="mt-2 text-xs text-fg-tertiary">
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
        onClose={() => setOpen(false)}
        busy={busy}
        footer={
          <Space>
            <Button onClick={() => setOpen(false)} disabled={busy}>
              {text("取消", "Cancel")}
            </Button>
            <Button
              type="primary"
              loading={busy}
              disabled={!issuer.trim() || !subject.trim()}
              onClick={() => void link()}
            >
              {text("绑定", "Link")}
            </Button>
          </Space>
        }
      >
        {linkError !== null && (
          <div className="mb-4">
            <ErrorBanner error={linkError} />
          </div>
        )}
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
