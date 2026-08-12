import { useEffect, useMemo, useRef, useState } from "react";
import { Select, Tag } from "antd";
import { searchRepositoryArtifacts } from "../client";
import type { ArtifactSummary, Repository } from "../client";
import { formatBytes, formatDate, shortDigest } from "../lib/format";
import { usePreferences } from "../lib/preferences";

export type RepositoryArtifactIdentity = {
  key: string;
  coordinate: string;
  digest: string;
  size?: number;
  createdAt?: string;
  intelligence?: ArtifactSummary["intelligence"];
};

export type RepositoryArtifactStatus = {
  color?: string;
  label: string;
};

function lifecycleCoordinate(
  format: Repository["format"],
  artifact: ArtifactSummary,
): string {
  if (
    artifact.version &&
    (format === "npm" || format === "pypi" || format === "go")
  ) {
    return `${artifact.coordinate}@${artifact.version}`;
  }
  return artifact.coordinate;
}

function artifactIdentity(
  format: Repository["format"],
  artifact: ArtifactSummary,
): RepositoryArtifactIdentity | null {
  if (!artifact.digest) return null;
  const coordinate = lifecycleCoordinate(format, artifact);
  // The shared Conan browse projection exposes only a reference, while
  // lifecycle operations require a pinned recipe/package revision.
  if (format === "conan" && !coordinate.includes("#")) return null;
  return {
    key: JSON.stringify([coordinate, artifact.digest]),
    coordinate,
    digest: artifact.digest,
    size: artifact.size,
    createdAt: artifact.createdAt,
    intelligence: artifact.intelligence,
  };
}

export function RepositoryArtifactSelect({
  repo,
  value,
  onChange,
  enabled = true,
  disabled = false,
  ariaLabel,
  placeholder,
  statusForOption,
}: {
  repo: Repository;
  value: RepositoryArtifactIdentity | null;
  onChange: (value: RepositoryArtifactIdentity | null) => void;
  enabled?: boolean;
  disabled?: boolean;
  ariaLabel: string;
  placeholder?: string;
  statusForOption?: (
    option: RepositoryArtifactIdentity,
  ) => RepositoryArtifactStatus | undefined;
}) {
  const { text } = usePreferences();
  const [query, setQuery] = useState("");
  const [options, setOptions] = useState<RepositoryArtifactIdentity[]>([]);
  const [searching, setSearching] = useState(false);
  const [searchError, setSearchError] = useState<unknown>(null);
  const requestRef = useRef(0);

  useEffect(() => {
    if (!value) setQuery("");
  }, [value]);

  useEffect(() => {
    const request = ++requestRef.current;
    let active = true;
    if (!enabled) {
      setSearching(false);
      setOptions([]);
      setSearchError(null);
      return;
    }

    const trimmedQuery = query.trim();
    const timer = window.setTimeout(
      () => {
        setSearching(true);
        setSearchError(null);
        void (async () => {
          try {
            const { data, error } = await searchRepositoryArtifacts({
              path: { repositoryId: repo.id },
              query: {
                ...(trimmedQuery ? { q: trimmedQuery } : {}),
                pageSize: 50,
              },
            });
            if (!active || request !== requestRef.current) return;
            setSearching(false);
            if (error) {
              setOptions([]);
              setSearchError(error);
              return;
            }
            const unique = new Map<string, RepositoryArtifactIdentity>();
            for (const artifact of data?.items ?? []) {
              const option = artifactIdentity(repo.format, artifact);
              if (option) unique.set(option.key, option);
            }
            setOptions([...unique.values()]);
          } catch (error) {
            if (!active || request !== requestRef.current) return;
            setSearching(false);
            setOptions([]);
            setSearchError(error);
          }
        })();
      },
      trimmedQuery ? 250 : 0,
    );

    return () => {
      active = false;
      window.clearTimeout(timer);
    };
  }, [enabled, query, repo.format, repo.id]);

  const selectableOptions = useMemo(() => {
    if (value && !options.some((option) => option.key === value.key)) {
      return [value, ...options];
    }
    return options;
  }, [options, value]);

  return (
    <>
      <Select
        aria-label={ariaLabel}
        className="w-full"
        value={value?.key}
        disabled={!enabled || disabled}
        loading={searching}
        placeholder={
          placeholder ??
          text(
            "输入包名、路径或坐标进行搜索",
            "Search by package, path, or coordinate",
          )
        }
        showSearch={{ filterOption: false, onSearch: setQuery }}
        options={selectableOptions.map((option) => ({
          value: option.key,
          label: option.coordinate,
        }))}
        optionRender={(option) => {
          const artifact = selectableOptions.find(
            (candidate) => candidate.key === option.value,
          );
          if (!artifact) return option.label;
          const status = statusForOption?.(artifact);
          return (
            <div className="flex min-w-0 items-center justify-between gap-4 py-1">
              <div className="min-w-0">
                <div className="truncate font-mono text-xs text-zinc-200">
                  {artifact.coordinate}
                </div>
                <div className="mt-0.5 flex flex-wrap gap-x-2 text-[11px] text-zinc-500">
                  <span className="font-mono">
                    {shortDigest(artifact.digest)}
                  </span>
                  {artifact.size !== undefined && (
                    <span>{formatBytes(artifact.size)}</span>
                  )}
                  {artifact.createdAt && (
                    <span>{formatDate(artifact.createdAt)}</span>
                  )}
                </div>
              </div>
              {status && <Tag color={status.color}>{status.label}</Tag>}
            </div>
          );
        }}
        notFoundContent={
          searching
            ? text("正在搜索…", "Searching…")
            : text("没有可直接选择的制品", "No selectable artifacts")
        }
        onChange={(key) => {
          const option = selectableOptions.find(
            (candidate) => candidate.key === key,
          );
          if (option) onChange(option);
        }}
        allowClear
        onClear={() => {
          setQuery("");
          onChange(null);
        }}
        listHeight={320}
      />
      {searchError !== null && (
        <p className="mt-1 text-xs text-rose-400" role="alert">
          {text(
            "制品搜索失败，请修改关键词后重试。",
            "Artifact search failed. Change the query and try again.",
          )}
        </p>
      )}
    </>
  );
}
