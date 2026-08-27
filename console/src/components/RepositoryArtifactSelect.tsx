import { useEffect, useMemo, useRef, useState } from "react";
import { Select, Tag } from "antd";
import { listRepositoryArtifactIdentities } from "../client";
import type {
  ArtifactIdentity,
  ArtifactIdentityPurpose,
  Repository,
} from "../client";
import { formatBytes, formatDate, shortDigest } from "../lib/format";
import { usePreferences } from "../lib/preferences";
import {
  artifactCoordinateForDisplay,
  canonicalRawSearchPrefix,
} from "../lib/rawPath";

export type RepositoryArtifactIdentity = {
  key: string;
  coordinate: string;
  digest: string;
  size?: number;
  createdAt?: string;
  intelligence?: ArtifactIdentity["intelligence"];
};

export type RepositoryArtifactStatus = {
  color?: string;
  label: string;
};

function artifactIdentity(
  artifact: ArtifactIdentity,
): RepositoryArtifactIdentity {
  return {
    key: JSON.stringify([artifact.coordinate, artifact.digest]),
    coordinate: artifact.coordinate,
    digest: artifact.digest,
    size: artifact.size,
    createdAt: artifact.publishedAt,
    intelligence: artifact.intelligence,
  };
}

export function RepositoryArtifactSelect({
  repo,
  purpose,
  value,
  onChange,
  enabled = true,
  disabled = false,
  ariaLabel,
  placeholder,
  statusForOption,
}: {
  repo: Repository;
  purpose: ArtifactIdentityPurpose;
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
            const { data, error } = await listRepositoryArtifactIdentities({
              path: { repositoryId: repo.id },
              query: {
                purpose,
                ...(trimmedQuery
                  ? {
                      q:
                        repo.format === "raw"
                          ? canonicalRawSearchPrefix(trimmedQuery)
                          : trimmedQuery,
                    }
                  : {}),
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
              const option = artifactIdentity(artifact);
              unique.set(option.key, option);
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
  }, [enabled, purpose, query, repo.format, repo.id]);

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
          label: artifactCoordinateForDisplay(repo.format, option.coordinate),
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
                  {artifactCoordinateForDisplay(
                    repo.format,
                    artifact.coordinate,
                  )}
                </div>
                <div className="mt-0.5 flex flex-wrap gap-x-2 text-xs text-zinc-500">
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
