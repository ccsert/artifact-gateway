import { useState } from "react";
import { Button, Checkbox, Input, Select } from "antd";
import { clearProxyNegativeCache, invalidateProxyCache } from "../../client";
import { usePreferences } from "../../lib/preferences";
import { mavenWarmPath } from "./proxyMavenPaths";

/** Invalidate cached entries, or clear remembered upstream misses. */
export function ProxyMavenInvalidateCard({
  repoId,
  onWarmed,
}: {
  repoId: string;
  onWarmed: () => void;
}) {
  const { text } = usePreferences();
  const [invalidateInput, setInvalidateInput] = useState("");
  const [invalidateScope, setInvalidateScope] = useState<
    "path" | "version" | "component" | "repository"
  >("path");
  const [invalidatePrefix, setInvalidatePrefix] = useState(false);
  const [invalidating, setInvalidating] = useState(false);
  const [invalidateResult, setInvalidateResult] = useState<number | null>(null);
  const [invalidateError, setInvalidateError] = useState("");
  const [clearingNegative, setClearingNegative] = useState(false);
  const [negativeResult, setNegativeResult] = useState<number | null>(null);
  const [negativeError, setNegativeError] = useState("");

  const invalidate = async () => {
    const value = invalidateInput.trim();
    if (invalidateScope !== "repository" && !value) {
      setInvalidateError(
        text("请输入失效目标。", "Enter an invalidation target."),
      );
      return;
    }
    setInvalidating(true);
    setInvalidateError("");
    setInvalidateResult(null);
    try {
      const body = {
        path: value,
        scope: invalidateScope,
        prefix: invalidateScope === "path" && invalidatePrefix,
      };
      const { data: result, error } = await invalidateProxyCache({
        path: { repositoryId: repoId },
        body,
      });
      if (error || !result)
        throw new Error(text("失效缓存失败", "Failed to invalidate cache"));
      setInvalidateResult(result.invalidated);
      onWarmed();
    } catch (error) {
      setInvalidateError(
        error instanceof Error
          ? error.message
          : text("失效缓存失败", "Failed to invalidate cache"),
      );
    } finally {
      setInvalidating(false);
    }
  };

  const clearNegative = async () => {
    const path = invalidateInput.trim() ? mavenWarmPath(invalidateInput) : null;
    if (invalidateInput.trim() && !path) {
      setNegativeError(
        text(
          "请输入 Maven GAV 或缓存路径。",
          "Enter a Maven GAV or cache path.",
        ),
      );
      return;
    }
    setClearingNegative(true);
    setNegativeError("");
    setNegativeResult(null);
    try {
      const { data: result, error } = await clearProxyNegativeCache({
        path: { repositoryId: repoId },
        body: { ...(path ? { path } : {}), prefix: invalidatePrefix },
      });
      if (error || !result)
        throw new Error(
          text("清理负缓存失败", "Failed to clear negative cache"),
        );
      setNegativeResult(result.cleared);
      onWarmed();
    } catch (error) {
      setNegativeError(
        error instanceof Error
          ? error.message
          : text("清理负缓存失败", "Failed to clear negative cache"),
      );
    } finally {
      setClearingNegative(false);
    }
  };

  return (
    <div className="rounded-lg border border-line bg-surface-translucent px-3 py-3">
      <div className="mb-2 text-sm font-medium text-fg">
        {text("失效缓存", "Invalidate cache")}
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <Select
          className="w-28"
          value={invalidateScope}
          options={[
            { value: "path", label: text("路径", "Path") },
            { value: "version", label: text("版本", "Version") },
            { value: "component", label: text("组件", "Component") },
            { value: "repository", label: text("全部", "Repository") },
          ]}
          onChange={(value: typeof invalidateScope) =>
            setInvalidateScope(value)
          }
        />
        <Input
          className="min-w-80 flex-1 font-mono"
          placeholder={
            invalidateScope === "component"
              ? "org.springframework.boot:spring-boot"
              : invalidateScope === "version"
                ? "org.springframework.boot:spring-boot:3.4.4"
                : "org/springframework/boot/spring-boot/3.4.4"
          }
          value={invalidateInput}
          onChange={(e) => setInvalidateInput(e.target.value)}
          disabled={invalidateScope === "repository"}
        />
        {invalidateScope === "path" && (
          <Checkbox
            checked={invalidatePrefix}
            onChange={(e) => setInvalidatePrefix(e.target.checked)}
          >
            {text("按前缀", "By prefix")}
          </Checkbox>
        )}
        <Button
          onClick={invalidate}
          loading={invalidating}
          disabled={invalidateScope !== "repository" && !invalidateInput.trim()}
        >
          {text("失效", "Invalidate")}
        </Button>
        <Button onClick={clearNegative} loading={clearingNegative}>
          {text("清理负缓存", "Clear negative cache")}
        </Button>
      </div>
      <div className="mt-1 text-xs text-fg-disabled">
        {text(
          "版本、组件和全部会按对应 Maven 缓存前缀失效；只删除缓存索引，字节对象由 Orphan Collector 延迟回收。",
          "Version, component, and repository invalidations use their Maven cache prefixes. Only cache indexes are removed; byte objects are reclaimed later by the orphan collector.",
        )}
      </div>
      {invalidateError && (
        <div className="mt-2 text-xs text-[var(--ag-status-danger)]">
          {invalidateError}
        </div>
      )}
      {invalidateResult !== null && (
        <div className="mt-2 text-xs text-[var(--ag-status-success)]">
          {text(
            `已失效 ${invalidateResult} 个缓存条目。`,
            `${invalidateResult} cache entries invalidated.`,
          )}
        </div>
      )}
      {negativeError && (
        <div className="mt-2 text-xs text-[var(--ag-status-danger)]">
          {negativeError}
        </div>
      )}
      {negativeResult !== null && (
        <div className="mt-2 text-xs text-[var(--ag-status-success)]">
          {text(
            `已清理 ${negativeResult} 个负-cache 条目。`,
            `${negativeResult} negative-cache entries cleared.`,
          )}
        </div>
      )}
    </div>
  );
}
