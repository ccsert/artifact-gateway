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
  const { t } = usePreferences();
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
      setInvalidateError(t("repository.enterAnInvalidationTarget"));
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
        throw new Error(t("repository.failedToInvalidateCache"));
      setInvalidateResult(result.invalidated);
      onWarmed();
    } catch (error) {
      setInvalidateError(
        error instanceof Error
          ? error.message
          : t("repository.failedToInvalidateCache"),
      );
    } finally {
      setInvalidating(false);
    }
  };

  const clearNegative = async () => {
    const path = invalidateInput.trim() ? mavenWarmPath(invalidateInput) : null;
    if (invalidateInput.trim() && !path) {
      setNegativeError(t("repository.enterAMavenGAVOrCachePath"));
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
        throw new Error(t("repository.failedToClearNegativeCache"));
      setNegativeResult(result.cleared);
      onWarmed();
    } catch (error) {
      setNegativeError(
        error instanceof Error
          ? error.message
          : t("repository.failedToClearNegativeCache"),
      );
    } finally {
      setClearingNegative(false);
    }
  };

  return (
    <div className="rounded-lg border border-line bg-surface-translucent px-3 py-3">
      <div className="mb-2 text-sm font-medium text-fg">
        {t("repository.invalidateCache")}
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <Select
          className="w-28"
          value={invalidateScope}
          options={[
            { value: "path", label: t("repository.path") },
            { value: "version", label: t("repository.version") },
            { value: "component", label: t("repository.component") },
            { value: "repository", label: t("repository.repository") },
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
            {t("repository.byPrefix")}
          </Checkbox>
        )}
        <Button
          onClick={invalidate}
          loading={invalidating}
          disabled={invalidateScope !== "repository" && !invalidateInput.trim()}
        >
          {t("repository.invalidate")}
        </Button>
        <Button onClick={clearNegative} loading={clearingNegative}>
          {t("repository.clearNegativeCache")}
        </Button>
      </div>
      <div className="mt-1 text-xs text-fg-disabled">
        {t(
          "repository.versionComponentAndRepositoryInvalidationsUseTheirMaven",
        )}
      </div>
      {invalidateError && (
        <div className="mt-2 text-xs text-[var(--ag-status-danger)]">
          {invalidateError}
        </div>
      )}
      {invalidateResult !== null && (
        <div className="mt-2 text-xs text-[var(--ag-status-success)]">
          {t("repository.cacheEntriesInvalidated", { invalidateResult })}
        </div>
      )}
      {negativeError && (
        <div className="mt-2 text-xs text-[var(--ag-status-danger)]">
          {negativeError}
        </div>
      )}
      {negativeResult !== null && (
        <div className="mt-2 text-xs text-[var(--ag-status-success)]">
          {t("repository.negativeCacheEntriesCleared", { negativeResult })}
        </div>
      )}
    </div>
  );
}
