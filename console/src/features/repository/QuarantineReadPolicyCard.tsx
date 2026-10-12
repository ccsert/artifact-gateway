import { useState } from "react";
import { Button, Switch, Tag } from "antd";
import { replaceQuarantineReadPolicy } from "../../client";
import type { QuarantineReadPolicy, Repository } from "../../client";
import { ErrorBanner, Notice } from "../../components/ui/Feedback";
import { usePreferences } from "../../lib/preferences";
import { PolicyCard, ScopeFact } from "./RepositorySecurityPrimitives";

import { quarantineReadScope } from "./securityPolicyParts";
import { useSetQuarantineReadPolicy } from "./repositoryQueries";

/** Whether quarantined artifacts in this repository can still be downloaded. */
export function QuarantineReadPolicyCard({
  repo,
  readPolicy,
  loadError,
  onRetry,
}: {
  repo: Repository;
  readPolicy: QuarantineReadPolicy | null;
  loadError: unknown;
  onRetry: () => void;
}) {
  const { text } = usePreferences();
  const setStoredPolicy = useSetQuarantineReadPolicy(repo.id);
  const [readEnabled, setReadEnabled] = useState(readPolicy?.enabled ?? false);
  // Save failures are local; a failed read comes from the query.
  const [saveError, setReadError] = useState<unknown>(null);
  const readError = saveError ?? loadError;
  const [readSaving, setReadSaving] = useState(false);
  const [readNotice, setReadNotice] = useState("");
  const [draftSource, setDraftSource] = useState({
    repositoryId: repo.id,
    version: readPolicy?.version,
  });
  if (
    draftSource.repositoryId !== repo.id ||
    draftSource.version !== readPolicy?.version
  ) {
    if (draftSource.repositoryId !== repo.id) setReadNotice("");
    setDraftSource({ repositoryId: repo.id, version: readPolicy?.version });
    setReadEnabled(readPolicy?.enabled ?? false);
    setReadError(null);
  }
  const updateReadEnabled = (enabled: boolean) => {
    setReadNotice("");
    setReadError(null);
    setReadEnabled(enabled);
  };

  const saveReadPolicy = async () => {
    if (!readPolicy) return;
    setReadSaving(true);
    setReadError(null);
    setReadNotice("");
    try {
      const { data, error: err } = await replaceQuarantineReadPolicy({
        path: { repositoryId: repo.id },
        headers: { "If-Match": readPolicy.version },
        body: { version: readPolicy.version, enabled: readEnabled },
      });
      if (err) {
        setReadError(err);
        return;
      }
      setReadNotice(text("隔离读取策略已保存", "Quarantine read policy saved"));
      if (data) {
        setStoredPolicy(data);
        setReadEnabled(data.enabled);
      }
    } catch (nextError) {
      setReadError(nextError);
    } finally {
      setReadSaving(false);
    }
  };

  const readDirty = readPolicy ? readPolicy.enabled !== readEnabled : false;
  const readScope = quarantineReadScope(repo.format, text);

  return (
    <PolicyCard
      eyebrow={text("读取防线", "Read guardrail")}
      title={text("隔离制品读取", "Quarantined artifact reads")}
      description={text(
        "控制客户端是否仍能通过仓库协议读取已经隔离的制品；解除隔离后会立即恢复读取。",
        "Controls whether clients may continue reading quarantined artifacts through repository protocols. Reads resume immediately after release.",
      )}
      status={
        <>
          {readPolicy ? (
            <Tag color={readEnabled ? "warning" : "default"}>
              {readEnabled
                ? text("阻断读取", "Reads blocked")
                : text("允许读取", "Reads allowed")}
            </Tag>
          ) : (
            <Tag color="error">{text("状态不可用", "Unavailable")}</Tag>
          )}
          {readDirty && (
            <Tag color="processing">
              {text("有未保存更改", "Unsaved changes")}
            </Tag>
          )}
          {readPolicy && (
            <Switch
              aria-label={text(
                "阻断隔离制品读取",
                "Block quarantined artifact reads",
              )}
              checked={readEnabled}
              onChange={updateReadEnabled}
            />
          )}
        </>
      }
    >
      {readError !== null && (
        <ErrorBanner error={readError} onRetry={onRetry} />
      )}
      {readPolicy && (
        <>
          {readNotice && (
            <Notice
              tone="success"
              title={readNotice}
              onClose={() => setReadNotice("")}
            />
          )}
          <div className="grid gap-px overflow-hidden rounded-lg border border-line bg-[var(--ag-border-subtle)] sm:grid-cols-2">
            <ScopeFact
              label={text("影响请求", "Affected requests")}
              value="GET / HEAD"
            />
            <ScopeFact
              label={text("当前格式的阻断粒度", "Current format boundary")}
              value={readScope.boundary}
            />
            <ScopeFact
              label={text("发现元数据", "Discovery metadata")}
              value={readScope.metadata}
            />
            <ScopeFact
              label={text("作为 Group 成员", "When used by a Group")}
              value={text("不降级回退", "No lower-priority fallback")}
            />
          </div>
          <div className="flex flex-col gap-4 border-t border-line pt-4">
            <p className="max-w-2xl text-xs leading-5 text-fg-tertiary">
              {text(
                "默认允许读取以保持升级兼容。启用阻断前，请先检查当前仓库已有的隔离记录。",
                "Reads remain allowed by default for upgrade compatibility. Review existing quarantine records before enabling enforcement.",
              )}
            </p>
            <div className="flex flex-wrap items-center justify-between gap-3">
              <span className="text-xs text-fg-disabled">
                {text(
                  `读取策略当前版本 ${readPolicy.version}`,
                  `Read policy version ${readPolicy.version}`,
                )}
              </span>
              <Button
                type="primary"
                danger={readEnabled}
                loading={readSaving}
                disabled={!readDirty}
                onClick={saveReadPolicy}
              >
                {text("保存读取策略", "Save read policy")}
              </Button>
            </div>
          </div>
        </>
      )}
    </PolicyCard>
  );
}
