import {
  ApiOutlined,
  KeyOutlined,
  ReloadOutlined,
  RobotOutlined,
  SafetyCertificateOutlined,
  StopOutlined,
} from "@ant-design/icons";
import { Button, Space } from "antd";
import type { ColumnsType } from "antd/es/table";
import type { ServiceAccount, ServiceAccountCredential } from "../../client";
import { Badge, StateBadge } from "../../components/ui/Badge";
import {
  ConsoleTable,
  CopyableValue,
} from "../../components/ui/ConsolePrimitives";
import { EmptyState, ErrorBanner, Loading } from "../../components/ui/Feedback";
import { Card, CardHeader, Pagination } from "../../components/ui/Layout";
import { formatDate } from "../../lib/format";
import { usePreferences } from "../../lib/preferences";

export const credentialState = (credential: ServiceAccountCredential) => {
  if (credential.revokedAt) return "revoked";
  if (
    credential.expiresAt &&
    new Date(credential.expiresAt).getTime() <= Date.now()
  )
    return "expired";
  return "active";
};

/** One account: its stable principal and its rotating credentials. */
export function ServiceAccountDetail({
  account,
  credentials,
  credentialsError,
  nextCredentialsPageToken,
  loadingMoreCredentials,
  onRefresh,
  onMoreCredentials,
  onIssue,
  onRevoke,
  onToggleState,
}: {
  account: ServiceAccount;
  credentials: ServiceAccountCredential[] | null;
  credentialsError: unknown;
  nextCredentialsPageToken: string;
  loadingMoreCredentials: boolean;
  onRefresh: () => void;
  onMoreCredentials: () => void;
  onIssue: () => void;
  onRevoke: (credential: ServiceAccountCredential) => void;
  onToggleState: () => void;
}) {
  const { locale, text } = usePreferences();
  const columns: ColumnsType<ServiceAccountCredential> = [
    {
      title: text("凭据", "Credential"),
      dataIndex: "name",
      key: "name",
      render: (name: string) => (
        <div className="flex items-center gap-2">
          <KeyOutlined className="text-fg-disabled!" />
          <span className="font-medium text-fg">{name}</span>
        </div>
      ),
    },
    {
      title: text("状态", "State"),
      key: "state",
      width: 120,
      render: (_value, credential) => (
        <StateBadge state={credentialState(credential)} />
      ),
    },
    {
      title: text("到期时间", "Expires"),
      dataIndex: "expiresAt",
      key: "expiresAt",
      width: 180,
      render: (value?: string) => (
        <span className="text-xs text-fg-tertiary">
          {formatDate(value, locale)}
        </span>
      ),
    },
    {
      title: text("最后使用", "Last used"),
      dataIndex: "lastUsedAt",
      key: "lastUsedAt",
      width: 180,
      render: (value?: string) => (
        <span className="text-xs text-fg-tertiary">
          {formatDate(value, locale)}
        </span>
      ),
    },
    {
      title: text("操作", "Actions"),
      key: "actions",
      align: "right",
      width: 92,
      render: (_value, credential) =>
        credentialState(credential) === "active" ? (
          <Button
            danger
            type="text"
            size="small"
            aria-label={text(
              `吊销 ${credential.name}`,
              `Revoke ${credential.name}`,
            )}
            onClick={() => onRevoke(credential)}
          >
            {text("吊销", "Revoke")}
          </Button>
        ) : null,
    },
  ];

  return (
    <Card>
      <div className="border-b border-line px-5 py-4">
        <div className="flex flex-wrap items-start justify-between gap-4">
          <div className="flex min-w-0 items-start gap-3">
            <div className="flex size-10 shrink-0 items-center justify-center rounded-xl border border-[var(--ag-border-default)] bg-[var(--ag-surface-hover)] text-[var(--ag-content-secondary)]">
              <RobotOutlined />
            </div>
            <div className="min-w-0">
              <div className="flex flex-wrap items-center gap-2">
                <h2 className="text-base font-semibold text-fg-strong">
                  {account.name}
                </h2>
                <Badge
                  tone={account.state === "active" ? "success" : "neutral"}
                >
                  {account.state}
                </Badge>
              </div>
              <p className="mt-1 text-xs text-fg-tertiary">
                {account.description ||
                  text("未填写用途", "No purpose documented")}
              </p>
            </div>
          </div>
          <Space wrap>
            <Button icon={<ReloadOutlined />} onClick={onRefresh}>
              {text("刷新", "Refresh")}
            </Button>
            <Button
              danger={account.state === "active"}
              icon={
                account.state === "active" ? (
                  <StopOutlined />
                ) : (
                  <SafetyCertificateOutlined />
                )
              }
              onClick={onToggleState}
            >
              {account.state === "active"
                ? text("禁用账号", "Disable account")
                : text("重新启用", "Enable account")}
            </Button>
          </Space>
        </div>
        <div className="mt-4 rounded-lg border border-line bg-surface-translucent px-3 py-2.5">
          <div className="text-xs font-medium uppercase tracking-wider text-fg-disabled">
            {text("稳定授权主体", "Stable grant principal")}
          </div>
          <CopyableValue
            value={`service-account:${account.id}`}
            className="mt-1 max-w-full text-xs text-fg-secondary"
          />
        </div>
      </div>
      <CardHeader
        title={text("可轮换凭据", "Rotating credentials")}
        extra={
          <Button
            type="primary"
            icon={<ApiOutlined />}
            disabled={account.state !== "active"}
            onClick={onIssue}
          >
            {text("签发新凭据", "Issue credential")}
          </Button>
        }
      />
      {credentialsError !== null && (
        <div className="px-5 pt-4">
          <ErrorBanner error={credentialsError} onRetry={onRefresh} />
        </div>
      )}
      {credentials === null ? (
        <div className="p-8">
          <Loading label={text("加载凭据…", "Loading credentials…")} />
        </div>
      ) : credentials.length === 0 ? (
        <div className="p-8">
          <EmptyState
            title={text("暂无凭据", "No credentials")}
            hint={text(
              "签发第一枚短期凭据，并保存到 CI Secret。",
              "Issue the first short-lived credential and store it in a CI secret.",
            )}
          />
        </div>
      ) : (
        <>
          <ConsoleTable<ServiceAccountCredential>
            rowKey="id"
            columns={columns}
            dataSource={credentials}
            pagination={false}
            size="small"
            scroll={{ x: 760 }}
          />
          <Pagination
            hasMore={!!nextCredentialsPageToken}
            loading={loadingMoreCredentials}
            onMore={onMoreCredentials}
            label={text("加载更多凭据", "Load more credentials")}
          />
        </>
      )}
    </Card>
  );
}
