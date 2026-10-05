import { useCallback, useRef, useState } from "react";
import { Button } from "antd";
import { ReloadOutlined } from "@ant-design/icons";
import {
  getEmailNotificationCapability,
  listEmailTargets,
  listEmailDeliveries,
  type EmailTarget,
} from "../../client";
import { Card, CardHeader } from "../../components/ui/Layout";
import {
  EmptyState,
  ErrorBanner,
  Loading,
  Notice,
} from "../../components/ui/Feedback";
import { useAuth } from "../../lib/auth";
import { platformCapabilities } from "../../lib/authorization";
import { usePreferences } from "../../lib/preferences";
import { quotaResult, useQuotaSnapshot } from "./useQuotaSnapshot";
import { safeEmailError } from "./emailPresentation";
import { isQuotaAuthError, quotaLabel } from "./quotaAlertPresentation";
import { QuotaBadge } from "./QuotaBadge";
import { EmailTargetEditor } from "./EmailTargetEditor";
import { EmailTargetAction } from "./EmailTargetAction";
import { EmailTemplatePreview } from "./EmailTemplatePreview";
import { EmailDeliveryResults } from "./EmailDeliveryResults";

export function EmailNotificationsPanel() {
  const { identity } = useAuth();
  const { text } = usePreferences();
  if (!platformCapabilities(identity).platformAdmin)
    return (
      <EmptyState
        title={text(
          "仅平台管理员可管理邮件通知",
          "Only platform administrators can manage email notifications",
        )}
      />
    );
  return <AuthorizedEmailNotifications />;
}
function AuthorizedEmailNotifications() {
  const { text } = usePreferences();
  const [editor, setEditor] = useState<{ target?: EmailTarget }>();
  const [confirmation, setConfirmation] = useState<{
    target: EmailTarget;
    kind: "test" | "enable" | "disable";
  }>();
  const [notice, setNotice] = useState<"saved" | "queued">();
  const [authBlocked, setAuthBlocked] = useState<unknown>();
  const authGeneration = useRef(0);
  const blockAuthorization = useCallback((error: unknown) => {
    authGeneration.current++;
    setEditor(undefined);
    setConfirmation(undefined);
    setAuthBlocked(error);
  }, []);
  const snapshot = useQuotaSnapshot(async (signal) => {
    const read = async <T,>(
      request: Promise<{ data?: T; error?: unknown }>,
    ) => {
      try {
        return quotaResult(await request);
      } catch (error) {
        if (!signal.aborted && isQuotaAuthError(error))
          blockAuthorization(error);
        throw error;
      }
    };
    const [targets, capability, deliveries] = await Promise.all([
      read(listEmailTargets({ signal })),
      read(getEmailNotificationCapability({ signal })),
      read(listEmailDeliveries({ signal, query: { limit: 50 } })),
    ]);
    return {
      targets,
      capability,
      deliveries,
    };
  }, "email");
  const refresh = async () => {
    const generation = authGeneration.current;
    if ((await snapshot.refresh()) && generation === authGeneration.current)
      setAuthBlocked(undefined);
  };
  if (authBlocked)
    return (
      <ErrorBanner
        error={safeEmailError(authBlocked, text, "read")}
        onRetry={() => void refresh()}
      />
    );
  if (!snapshot.data)
    return snapshot.error ? (
      <ErrorBanner
        error={safeEmailError(snapshot.error, text, "read")}
        onRetry={() => void refresh()}
      />
    ) : (
      <Loading />
    );
  const { targets, capability, deliveries } = snapshot.data;
  const writable = !snapshot.error && !snapshot.loading;
  const done = (sent = false) => {
    setEditor(undefined);
    setConfirmation(undefined);
    setNotice(sent ? "queued" : "saved");
    void snapshot.refresh();
  };
  return (
    <div className="ag-page-stack ag-email-notifications">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <h2 className="text-base font-semibold text-zinc-100">
            {text("邮件通知", "Email notifications")}
          </h2>
          <p className="mt-1 text-sm text-zinc-500">
            {text(
              "管理只写收件目标、预览模板并明确发送合成测试。",
              "Manage write-only recipient targets, preview templates, and explicitly send synthetic tests.",
            )}
          </p>
        </div>
        <Button
          icon={<ReloadOutlined aria-hidden="true" />}
          loading={snapshot.loading}
          onClick={() => void refresh()}
        >
          {text("刷新", "Refresh")}
        </Button>
      </div>
      {Boolean(snapshot.error) && (
        <ErrorBanner
          tone="warning"
          title={text("当前显示上次读取的数据", "Showing previously read data")}
          error={safeEmailError(snapshot.error, text, "read")}
          onRetry={() => void refresh()}
        />
      )}
      {notice && (
        <Notice
          tone="success"
          title={
            notice === "queued"
              ? text(
                  "合成测试已入队，请查看交付结果",
                  "Synthetic test queued; check delivery results",
                )
              : text(
                  "邮件目标已保存，没有发送测试",
                  "Email target saved; no test sent",
                )
          }
          onClose={() => setNotice(undefined)}
        />
      )}
      {!capability.enabled && (
        <Notice
          tone="info"
          title={quotaLabel(capability.reason, text)}
          description={text(
            "部署管理员配置邮件通道；此页不接收 SMTP 凭据。仍可预览，保存目标需要可用加密密钥。",
            "A deployment administrator configures the channel; this page accepts no SMTP credentials. Preview remains available; saving targets requires usable encryption.",
          )}
        />
      )}
      {editor ? (
        <EmailTargetEditor
          target={editor.target}
          writable={writable}
          onAuthBlocked={blockAuthorization}
          onCancel={() => setEditor(undefined)}
          onSaved={() => done()}
        />
      ) : confirmation ? (
        <EmailTargetAction
          {...confirmation}
          writable={
            writable && (confirmation.kind !== "test" || capability.enabled)
          }
          onAuthBlocked={blockAuthorization}
          onCancel={() => setConfirmation(undefined)}
          onDone={done}
        />
      ) : (
        <>
          <Card>
            <CardHeader
              title={text("邮件目标", "Email targets")}
              extra={
                <Button
                  type="primary"
                  disabled={!writable}
                  onClick={() => {
                    setNotice(undefined);
                    setEditor({});
                  }}
                >
                  {text("新建邮件目标", "New email target")}
                </Button>
              }
            />
            {targets.length === 0 ? (
              <EmptyState
                title={text("尚无邮件目标", "No email targets")}
                hint={text(
                  "新目标默认停用，保存不会发送邮件。",
                  "New targets are disabled; saving sends no mail.",
                )}
              />
            ) : (
              <ul className="divide-y divide-zinc-800/60">
                {targets.map((target) => (
                  <li key={target.id} className="grid min-w-0 gap-3 px-5 py-4">
                    <div className="flex flex-wrap items-center justify-between gap-3">
                      <h3 className="break-words text-sm font-semibold text-zinc-100">
                        {target.name}
                      </h3>
                      <QuotaBadge>
                        {target.enabled
                          ? text("已启用", "Enabled")
                          : text("已停用", "Disabled")}
                      </QuotaBadge>
                    </div>
                    <p className="text-xs text-zinc-500">
                      {target.locale === "en" ? "English" : "简体中文"} ·{" "}
                      {target.recipientConfigured
                        ? text(
                            "收件人已配置（只写）",
                            "Recipient configured (write-only)",
                          )
                        : text("未配置收件人", "Recipient not configured")}
                    </p>
                    <div className="flex flex-wrap gap-2">
                      <Button
                        size="small"
                        aria-label={text(
                          `编辑 ${target.name}`,
                          `Edit ${target.name}`,
                        )}
                        disabled={!writable}
                        onClick={() => {
                          setNotice(undefined);
                          setEditor({ target });
                        }}
                      >
                        {text("编辑", "Edit")}
                      </Button>
                      <Button
                        size="small"
                        disabled={!writable || !target.recipientConfigured}
                        aria-label={text(
                          `${target.enabled ? "停用" : "启用"} ${target.name}`,
                          `${target.enabled ? "Disable" : "Enable"} ${target.name}`,
                        )}
                        onClick={() => {
                          setNotice(undefined);
                          setConfirmation({
                            target,
                            kind: target.enabled ? "disable" : "enable",
                          });
                        }}
                      >
                        {target.enabled
                          ? text("停用", "Disable")
                          : text("启用", "Enable")}
                      </Button>
                      <Button
                        size="small"
                        disabled={
                          !writable ||
                          !capability.enabled ||
                          !target.enabled ||
                          !target.recipientConfigured
                        }
                        aria-label={text(
                          `测试 ${target.name}`,
                          `Test ${target.name}`,
                        )}
                        onClick={() => {
                          setNotice(undefined);
                          setConfirmation({ target, kind: "test" });
                        }}
                      >
                        {text("发送合成测试…", "Send synthetic test…")}
                      </Button>
                    </div>
                  </li>
                ))}
              </ul>
            )}
          </Card>
          <EmailTemplatePreview
            writable={writable}
            onAuthBlocked={blockAuthorization}
          />
          <EmailDeliveryResults deliveries={deliveries} targets={targets} />
          {snapshot.readAt && (
            <p className="text-xs text-zinc-500">
              {text("读取时间", "Read at")}:{" "}
              {new Date(snapshot.readAt).toLocaleString()} ·{" "}
              {text(
                "可见页面每 30 秒刷新",
                "Refreshes every 30 seconds while visible",
              )}
            </p>
          )}
        </>
      )}
    </div>
  );
}
