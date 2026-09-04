import { useCallback, useEffect, useState } from "react";
import {
  CloudOutlined,
  LaptopOutlined,
  ReloadOutlined,
  StopOutlined,
} from "@ant-design/icons";
import {
  App,
  Button,
  Empty,
  Popconfirm,
  Space,
  Spin,
  Switch,
  Tag,
  Tooltip,
  Typography,
  theme,
} from "antd";
import { listUserSessions, revokeUserSession } from "../../client";
import type { UserSession } from "../../client";
import { Badge } from "../../components/Badge";
import { ErrorBanner } from "../../components/Feedback";
import { formatDate } from "../../lib/format";
import { usePreferences } from "../../lib/preferences";

interface UserSessionsPanelProps {
  userId: string;
  refreshKey?: number;
}

export function UserSessionsPanel({
  userId,
  refreshKey = 0,
}: UserSessionsPanelProps) {
  const { message } = App.useApp();
  const { token } = theme.useToken();
  const { locale, text } = usePreferences();
  const [items, setItems] = useState<UserSession[]>([]);
  const [includeInactive, setIncludeInactive] = useState(false);
  const [loading, setLoading] = useState(true);
  const [revoking, setRevoking] = useState("");
  const [error, setError] = useState<unknown>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    const { data, error: requestError } = await listUserSessions({
      path: { userId },
      query: { includeInactive },
    });
    setLoading(false);
    if (requestError || !data) {
      setError(
        requestError ??
          new Error(text("加载会话失败", "Failed to load sessions")),
      );
      return;
    }
    setItems(data.items);
  }, [includeInactive, text, userId]);

  useEffect(() => {
    void load();
  }, [load, refreshKey]);

  const revoke = async (session: UserSession) => {
    setRevoking(session.id);
    setError(null);
    const { data, error: requestError } = await revokeUserSession({
      path: { userId, sessionId: session.id },
    });
    setRevoking("");
    if (requestError || !data) {
      setError(
        requestError ??
          new Error(text("撤销会话失败", "Failed to revoke session")),
      );
      return;
    }
    setItems((current) =>
      includeInactive
        ? current.map((item) => (item.id === data.id ? data : item))
        : current.filter((item) => item.id !== data.id),
    );
    void message.success(text("会话已撤销", "Session revoked"));
  };

  return (
    <section>
      <div className="mb-3 flex items-start justify-between gap-3">
        <div className="min-w-0">
          <h2 className="text-sm font-semibold">
            {text("活动会话", "User sessions")}
          </h2>
          <Typography.Text type="secondary" className="text-xs">
            {text(
              "单独撤销异常客户端；历史记录会在到期后保留 30 天。",
              "Revoke an individual client. Expired history is retained for 30 days.",
            )}
          </Typography.Text>
        </div>
        <Space size={8} wrap>
          <Space size={6}>
            <Typography.Text type="secondary" className="text-xs">
              {text("显示历史", "Show history")}
            </Typography.Text>
            <Switch
              size="small"
              checked={includeInactive}
              onChange={setIncludeInactive}
              aria-label={text("显示失效会话", "Show inactive sessions")}
            />
          </Space>
          <Tooltip title={text("刷新会话", "Refresh sessions")}>
            <Button
              size="small"
              icon={<ReloadOutlined />}
              aria-label={text("刷新会话", "Refresh sessions")}
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
          <div className="flex min-h-24 items-center justify-center">
            <Spin size="small" />
          </div>
        ) : items.length === 0 ? (
          <div className="py-4">
            <Empty
              image={Empty.PRESENTED_IMAGE_SIMPLE}
              description={text(
                includeInactive ? "没有会话记录" : "当前没有活动会话",
                includeInactive ? "No session history" : "No active sessions",
              )}
            />
          </div>
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
                        : text("本地登录", "Local sign-in")}
                    </Badge>
                    {session.current ? (
                      <Tag color="success">{text("当前", "Current")}</Tag>
                    ) : null}
                    {session.revokedAt ? (
                      <Tag>{text("已撤销", "Revoked")}</Tag>
                    ) : expired ? (
                      <Tag>{text("已过期", "Expired")}</Tag>
                    ) : null}
                  </div>
                  <Typography.Text
                    className="block max-w-[520px]"
                    ellipsis={{ tooltip: session.userAgent || undefined }}
                  >
                    {session.userAgent || text("未知客户端", "Unknown client")}
                  </Typography.Text>
                  <div className="mt-1 flex flex-wrap gap-x-4 gap-y-1 text-xs">
                    <Typography.Text type="secondary">
                      {text("地址", "Address")}：
                      {session.ipAddress || text("未知", "Unknown")}
                    </Typography.Text>
                    <Typography.Text type="secondary">
                      {text("创建", "Created")}：
                      {formatDate(session.createdAt, locale)}
                    </Typography.Text>
                    <Typography.Text type="secondary">
                      {text("到期", "Expires")}：
                      {formatDate(session.expiresAt, locale)}
                    </Typography.Text>
                  </div>
                </div>
                <div className="shrink-0">
                  <Popconfirm
                    title={text("撤销这个会话？", "Revoke this session?")}
                    description={text(
                      session.current
                        ? "这是当前会话，撤销后需要重新登录。"
                        : "对应客户端的登录状态将立即失效。",
                      session.current
                        ? "This is the current session. You will need to sign in again."
                        : "The corresponding client will be signed out immediately.",
                    )}
                    okText={text("撤销", "Revoke")}
                    cancelText={text("取消", "Cancel")}
                    okButtonProps={{ danger: true }}
                    disabled={inactive}
                    onConfirm={() => revoke(session)}
                  >
                    <Tooltip title={text("撤销会话", "Revoke session")}>
                      <Button
                        danger
                        type="text"
                        size="small"
                        icon={<StopOutlined />}
                        loading={revoking === session.id}
                        disabled={inactive}
                        aria-label={text("撤销会话", "Revoke session")}
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
