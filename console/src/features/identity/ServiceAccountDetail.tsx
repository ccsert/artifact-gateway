import { Link } from "react-router-dom";
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
import { StateBadge } from "../../components/ui/Badge";
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
  const { locale, t } = usePreferences();
  const columns: ColumnsType<ServiceAccountCredential> = [
    {
      title: t("identity.credential"),
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
      title: t("identity.state"),
      key: "state",
      width: 120,
      render: (_value, credential) => (
        <StateBadge state={credentialState(credential)} subject="identity" />
      ),
    },
    {
      title: t("identity.expires"),
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
      title: t("identity.lastUsed"),
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
      title: t("identity.actions"),
      key: "actions",
      align: "right",
      width: 92,
      render: (_value, credential) =>
        credentialState(credential) === "active" ? (
          <Button
            danger
            type="text"
            size="small"
            aria-label={t("identity.revoke2", {
              credential: String(credential.name),
            })}
            onClick={() => onRevoke(credential)}
          >
            {t("identity.revoke")}
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
                <StateBadge state={account.state} subject="identity" />
              </div>
              <p className="mt-1 text-xs text-fg-tertiary">
                {account.description || t("identity.noPurposeDocumented")}
              </p>
            </div>
          </div>
          <Space wrap>
            <Button icon={<ReloadOutlined />} onClick={onRefresh}>
              {t("identity.refresh")}
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
                ? t("identity.disableAccount")
                : t("identity.enableAccount")}
            </Button>
          </Space>
        </div>
        <div className="mt-4 rounded-lg border border-line bg-surface-translucent px-3 py-2.5">
          <div className="text-xs font-medium text-fg-disabled">
            {t("identity.stableGrantPrincipal")}
          </div>
          <CopyableValue
            value={`service-account:${account.id}`}
            className="mt-1 max-w-full text-xs text-fg-secondary"
          />
          {/* Rotation is verified elsewhere: where the principal is granted,
              and whether the new credential is already in use. */}
          <div className="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-xs">
            <Link
              to={`/access?principal=${encodeURIComponent(`service-account:${account.id}`)}`}
            >
              {t("identity.viewGrants")}
            </Link>
            <Link
              to={`/audits?actor=${encodeURIComponent(`service-account:${account.id}`)}`}
            >
              {t("identity.viewRecentAudits")}
            </Link>
          </div>
        </div>
      </div>
      <CardHeader
        title={t("identity.rotatingCredentials")}
        extra={
          <Button
            type="primary"
            icon={<ApiOutlined />}
            disabled={account.state !== "active"}
            onClick={onIssue}
          >
            {t("identity.issueCredential")}
          </Button>
        }
      />
      {credentialsError !== null && (
        <div className="px-5 pt-4">
          <ErrorBanner
            error={credentialsError}
            onRetry={onRefresh}
            tone={credentials === null ? "error" : "warning"}
          />
        </div>
      )}
      {credentials === null ? (
        credentialsError === null ? (
          <div className="p-8">
            <Loading label={t("identity.loadingCredentials")} />
          </div>
        ) : null
      ) : credentials.length === 0 ? (
        <div className="p-8">
          <EmptyState
            title={t("identity.noCredentials")}
            hint={t("identity.issueTheFirstShortLivedCredentialAndStoreIt")}
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
            label={t("identity.loadMoreCredentials")}
          />
        </>
      )}
    </Card>
  );
}
