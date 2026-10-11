import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import {
  CloudOutlined,
  LaptopOutlined,
  ReloadOutlined,
  StopOutlined,
} from "@ant-design/icons";
import {
  App,
  Button,
  Popconfirm,
  Space,
  Switch,
  Tag,
  Tooltip,
  Typography,
  theme,
} from "antd";
import { revokeUserSession } from "../../../client";
import type { ListUserSessionsResponse, UserSession } from "../../../client";
import { Badge } from "../../../components/ui/Badge";
import {
  EmptyState,
  ErrorBanner,
  Loading,
} from "../../../components/ui/Feedback";
import { formatDate } from "../../../lib/format";
import { usePreferences } from "../../../lib/preferences";
import { identityKeys } from "../../../lib/query/keys";
import { useUserSessions } from "../userQueries";

interface UserSessionsPanelProps {
  userId: string;
}

/** One user's sessions; the drawer invalidates them after account-wide revokes. */
export function UserSessionsPanel({ userId }: UserSessionsPanelProps) {
  const { message } = App.useApp();
  const { token } = theme.useToken();
  const { locale, t } = usePreferences();
  const queryClient = useQueryClient();
  const [includeInactive, setIncludeInactive] = useState(false);
  const sessionsQuery = useUserSessions(userId, includeInactive);
  const items = sessionsQuery.data?.items ?? [];
  const loading = sessionsQuery.isFetching;
  const [revoking, setRevoking] = useState("");
  const [actionError, setActionError] = useState<unknown>(null);
  const error = actionError ?? sessionsQuery.error;

  const load = () => {
    setActionError(null);
    return sessionsQuery.refetch();
  };

  const revoke = async (session: UserSession) => {
    setRevoking(session.id);
    setActionError(null);
    try {
      const { data, error: requestError } = await revokeUserSession({
        path: { userId, sessionId: session.id },
      });
      if (requestError || !data) {
        setActionError(
          requestError ?? new Error(t("identity.failedToRevokeSession")),
        );
        return;
      }
      queryClient.setQueryData<ListUserSessionsResponse>(
        [...identityKeys.userSessions(userId), includeInactive],
        (current) =>
          current && {
            ...current,
            items: includeInactive
              ? current.items.map((item) => (item.id === data.id ? data : item))
              : current.items.filter((item) => item.id !== data.id),
          },
      );
      void message.success(t("identity.sessionRevoked"));
    } catch (nextError) {
      setActionError(nextError);
    } finally {
      setRevoking("");
    }
  };

  return (
    <section>
      <div className="mb-3 flex items-start justify-between gap-3">
        <div className="min-w-0">
          <h2 className="text-sm font-semibold">
            {t("identity.userSessions")}
          </h2>
          <Typography.Text type="secondary" className="text-xs">
            {t("identity.revokeAnIndividualClientExpiredHistoryIsRetainedFor")}
          </Typography.Text>
        </div>
        <Space size={8} wrap>
          <Space size={6}>
            <Typography.Text type="secondary" className="text-xs">
              {t("identity.showHistory")}
            </Typography.Text>
            <Switch
              size="small"
              checked={includeInactive}
              onChange={setIncludeInactive}
              aria-label={t("identity.showInactiveSessions")}
            />
          </Space>
          <Tooltip title={t("identity.refreshSessions")}>
            <Button
              size="small"
              icon={<ReloadOutlined />}
              aria-label={t("identity.refreshSessions")}
              onClick={() => void load()}
            />
          </Tooltip>
        </Space>
      </div>

      {error ? <ErrorBanner error={error} onRetry={() => void load()} /> : null}
      <div
        role="list"
        aria-busy={loading}
        style={{
          overflow: "hidden",
          border: `${token.lineWidth}px ${token.lineType} ${token.colorBorderSecondary}`,
          borderRadius: token.borderRadius,
          background: token.colorBgContainer,
        }}
      >
        {loading ? (
          <Loading />
        ) : items.length === 0 ? (
          <EmptyState
            compact
            title={
              includeInactive
                ? t("identity.noSessionHistory")
                : t("identity.noActiveSessions")
            }
          />
        ) : (
          items.map((session, index) => {
            const expired = new Date(session.expiresAt).getTime() <= Date.now();
            const inactive = Boolean(session.revokedAt) || expired;
            return (
              <div
                key={session.id}
                role="listitem"
                className="flex items-center gap-3 px-3 py-2.5"
                style={
                  index === 0
                    ? undefined
                    : { borderTop: `1px solid ${token.colorBorderSecondary}` }
                }
              >
                <div className="min-w-0 flex-1 py-1">
                  <div className="mb-1 flex items-center gap-2">
                    {session.kind === "oidc" ? (
                      <CloudOutlined aria-hidden />
                    ) : (
                      <LaptopOutlined aria-hidden />
                    )}
                    <Badge
                      tone={
                        session.kind === "oidc" ? "visualization-5" : "neutral"
                      }
                    >
                      {session.kind === "oidc"
                        ? "OIDC"
                        : t("identity.localSignIn")}
                    </Badge>
                    {session.current ? (
                      <Tag color="success">{t("identity.current")}</Tag>
                    ) : null}
                    {session.revokedAt ? (
                      <Tag>{t("identity.revoked2")}</Tag>
                    ) : expired ? (
                      <Tag>{t("identity.expired")}</Tag>
                    ) : null}
                  </div>
                  <Typography.Text
                    className="block max-w-[520px]"
                    ellipsis={{ tooltip: session.userAgent || undefined }}
                  >
                    {session.userAgent || t("identity.unknownClient")}
                  </Typography.Text>
                  <div className="mt-1 flex flex-wrap gap-x-4 gap-y-1 text-xs">
                    <Typography.Text type="secondary">
                      {t("identity.address")}：
                      {session.ipAddress || t("identity.unknown")}
                    </Typography.Text>
                    <Typography.Text type="secondary">
                      {t("identity.created2")}：
                      {formatDate(session.createdAt, locale)}
                    </Typography.Text>
                    <Typography.Text type="secondary">
                      {t("identity.expires2")}：
                      {formatDate(session.expiresAt, locale)}
                    </Typography.Text>
                  </div>
                </div>
                <div className="shrink-0">
                  <Popconfirm
                    title={t("identity.revokeThisSession")}
                    description={
                      session.current
                        ? t("identity.thisIsTheCurrentSessionYouWillNeedTo")
                        : t(
                            "identity.theCorrespondingClientWillBeSignedOutImmediately",
                          )
                    }
                    okText={t("identity.revoke4")}
                    cancelText={t("identity.cancel")}
                    okButtonProps={{ danger: true }}
                    disabled={inactive}
                    onConfirm={() => revoke(session)}
                  >
                    <Tooltip title={t("identity.revokeSession")}>
                      <Button
                        danger
                        type="text"
                        size="small"
                        icon={<StopOutlined />}
                        loading={revoking === session.id}
                        disabled={inactive}
                        aria-label={t("identity.revokeSession")}
                      />
                    </Tooltip>
                  </Popconfirm>
                </div>
              </div>
            );
          })
        )}
      </div>
    </section>
  );
}
