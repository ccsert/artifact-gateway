import { Popconfirm, Switch } from "antd";
import type { AnonymousAccessPolicy } from "../../client";
import { Badge } from "../../components/ui/Badge";
import { ErrorBanner, Notice } from "../../components/ui/Feedback";
import { Card } from "../../components/ui/Layout";
import { usePreferences } from "../../lib/preferences";
import { useReplaceAnonymousPolicy } from "./accessQueries";

/** The global anonymous-read gate and how many repositories are public. */
export function PublicAccessCard({
  anonymousPolicy,
  loadError,
  canManageAnonymousPolicy,
  publicRepositoryCount,
  repositoryCount,
}: {
  anonymousPolicy: AnonymousAccessPolicy | null;
  loadError: unknown;
  canManageAnonymousPolicy: boolean;
  publicRepositoryCount: number;
  repositoryCount: number;
}) {
  const { text } = usePreferences();
  const replacePolicy = useReplaceAnonymousPolicy();
  const savingAnonymousPolicy = replacePolicy.isPending;
  const anonymousPolicyError = replacePolicy.error ?? loadError ?? null;
  const updateAnonymousPolicy = (enabled: boolean) => {
    if (!anonymousPolicy || savingAnonymousPolicy) return;
    replacePolicy.mutate({ ...anonymousPolicy, enabled });
  };

  return (
    <Card className="ag-public-access-card" bodyClassName="p-5">
      <div className="grid gap-4">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1">
            <h2 className="text-sm font-semibold text-fg-strong">
              {text("公开访问边界", "Public access boundary")}
            </h2>
            <span className="text-xs text-fg-tertiary">
              {text(
                `${publicRepositoryCount} / ${repositoryCount} 个仓库公开`,
                `${publicRepositoryCount} / ${repositoryCount} repositories public`,
              )}
            </span>
          </div>
          {anonymousPolicy && canManageAnonymousPolicy ? (
            <Popconfirm
              title={
                anonymousPolicy.enabled
                  ? text(
                      "确认停用全局匿名读取？",
                      "Disable global anonymous reads?",
                    )
                  : text(
                      "确认启用全局匿名读取？",
                      "Enable global anonymous reads?",
                    )
              }
              description={
                anonymousPolicy.enabled
                  ? text(
                      "停用后所有未认证协议读取都会被拒绝。",
                      "All unauthenticated protocol reads will be rejected.",
                    )
                  : text(
                      "启用后满足仓库或组策略的制品可被未认证客户端读取。",
                      "Artifacts allowed by repository or group policy become readable without authentication.",
                    )
              }
              okText={text("继续", "Continue")}
              cancelText={text("取消", "Cancel")}
              okButtonProps={{
                danger: anonymousPolicy.enabled,
                loading: savingAnonymousPolicy,
              }}
              onConfirm={() => updateAnonymousPolicy(!anonymousPolicy.enabled)}
            >
              <Switch
                checked={anonymousPolicy.enabled}
                loading={savingAnonymousPolicy}
                aria-label={text(
                  "切换全局匿名读取",
                  "Toggle global anonymous reads",
                )}
                onChange={() => undefined}
              />
            </Popconfirm>
          ) : canManageAnonymousPolicy ? (
            anonymousPolicyError === null ? (
              <span className="text-xs text-fg-tertiary">
                {text("加载中…", "Loading…")}
              </span>
            ) : null
          ) : (
            <Badge tone="neutral">
              {anonymousPolicy
                ? anonymousPolicy.enabled
                  ? text("总闸已开启 · 只读", "Global gate on · Read-only")
                  : text("总闸已关闭 · 只读", "Global gate off · Read-only")
                : text("只读", "Read-only")}
            </Badge>
          )}
        </div>
        <Notice
          tone="info"
          title={text(
            "匿名读取须全局、仓库及适用的分组同时允许，写入、删除和管理仍需认证。",
            "Anonymous reads require global, repository, and applicable group opt-in; writes, deletes, and management still require authentication.",
          )}
        />
        {anonymousPolicyError !== null && (
          <ErrorBanner error={anonymousPolicyError} />
        )}
      </div>
    </Card>
  );
}
