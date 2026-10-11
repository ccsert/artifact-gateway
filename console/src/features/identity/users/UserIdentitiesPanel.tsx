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
  const { locale, t } = usePreferences();
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
          requestError ?? new Error(t("identity.failedToLinkIdentity")),
        );
        return;
      }
      updateItems((current) => [...current, data]);
      setSubject("");
      setOpen(false);
      void message.success(t("identity.identityLinked"));
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
    void message.success(t("identity.identityUnlinked"));
  };

  return (
    <section>
      <div className="mb-3 flex items-center justify-between gap-3">
        <div>
          <h2 className="text-sm font-semibold text-fg-strong">
            {t("identity.externalIdentities")}
          </h2>
          <p className="mt-1 text-xs text-fg-tertiary">
            {t("identity.linkedOidcSignInsUseThisAccountSState")}
          </p>
        </div>
        <Space size={6}>
          <Button
            size="small"
            icon={<ReloadOutlined />}
            aria-label={t("identity.refreshExternalIdentities")}
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
            {t("identity.linkIdentity")}
          </Button>
        </Space>
      </div>

      {error ? <ErrorBanner error={error} onRetry={() => void load()} /> : null}
      {loading ? (
        <Loading />
      ) : items.length === 0 ? (
        <EmptyState compact title={t("identity.noExternalIdentityLinked")} />
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
                    <Tag color="success">{t("identity.emailVerified")}</Tag>
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
                  {t("identity.lastSignIn2")}：
                  {identity.lastLoginAt
                    ? formatDate(identity.lastLoginAt, locale)
                    : t("identity.never2")}
                </div>
              </div>
              <Popconfirm
                title={t("identity.unlinkThisIdentity")}
                description={t(
                  "identity.thisOidcIdentityWillNoLongerEnterThisAccount",
                )}
                okText={t("identity.unlink")}
                cancelText={t("identity.cancel")}
                okButtonProps={{ danger: true }}
                onConfirm={() => unlink(identity)}
              >
                <Button
                  size="small"
                  danger
                  type="text"
                  icon={<DeleteOutlined />}
                  aria-label={t("identity.unlinkIdentity")}
                />
              </Popconfirm>
            </div>
          ))}
        </div>
      )}

      <Modal
        open={open}
        title={t("identity.linkOidcIdentity")}
        onClose={() => setOpen(false)}
        busy={busy}
        footer={
          <Space>
            <Button onClick={() => setOpen(false)} disabled={busy}>
              {t("identity.cancel")}
            </Button>
            <Button
              type="primary"
              loading={busy}
              disabled={!issuer.trim() || !subject.trim()}
              onClick={() => void link()}
            >
              {t("identity.link")}
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
          title={t("identity.theIdentityProviderIsReadFromTheCurrentOidc")}
          description={
            issuer
              ? `${t("identity.issuer")}: ${issuer}`
              : t("identity.noOidcProviderIsConfiguredSoIdentitiesCannotBe")
          }
        />
        <Form layout="vertical">
          <Form.Item label="Issuer">
            <Input value={issuer} readOnly aria-label="Issuer" />
          </Form.Item>
          <Form.Item
            label="Subject"
            extra={t("identity.enterTheSubClaimFromTheOidcIdToken")}
            required
          >
            <Input
              value={subject}
              onChange={(event) => setSubject(event.target.value)}
              placeholder={t("identity.forExample00u123")}
              aria-label="Subject"
            />
          </Form.Item>
        </Form>
      </Modal>
    </section>
  );
}
