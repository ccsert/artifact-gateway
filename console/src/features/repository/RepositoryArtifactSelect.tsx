import { useMemo, useRef, useState } from "react";
import { Select, Tag } from "antd";
import { useQuery } from "@tanstack/react-query";
import { listRepositoryArtifactIdentities } from "../../client";
import { unwrap } from "../../lib/query/client";
import { repositoryKeys } from "../../lib/query/keys";
import type {
  ArtifactIdentity,
  ArtifactIdentityPurpose,
  Repository,
} from "../../client";
import { formatBytes, formatDate, shortDigest } from "../../lib/format";
import { usePreferences } from "../../lib/preferences";
import {
  artifactCoordinateForDisplay,
  artifactSearchPrefix,
} from "../../formats";

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
  const { t } = usePreferences();
  const [query, setQuery] = useState("");
  const searchTimer = useRef<number | undefined>(undefined);
  // Typing settles for a moment before it searches; clearing searches at once.
  const search = (next: string) => {
    window.clearTimeout(searchTimer.current);
    searchTimer.current = window.setTimeout(
      () => setQuery(next.trim()),
      next.trim() ? 250 : 0,
    );
  };
  // A selection cleared by the parent also clears the search.
  const [seenValue, setSeenValue] = useState(value);
  if (seenValue !== value) {
    setSeenValue(value);
    if (!value) setQuery("");
  }
  const identities = useQuery({
    queryKey: [
      ...repositoryKeys.detail(repo.id),
      "artifact-identities",
      purpose,
      query,
    ] as const,
    queryFn: async ({ signal }) => {
      const page = await unwrap(
        listRepositoryArtifactIdentities({
          path: { repositoryId: repo.id },
          query: {
            purpose,
            ...(query ? { q: artifactSearchPrefix(repo.format, query) } : {}),
            pageSize: 50,
          },
          signal,
        }),
      );
      const unique = new Map<string, RepositoryArtifactIdentity>();
      for (const artifact of page?.items ?? []) {
        const option = artifactIdentity(artifact);
        unique.set(option.key, option);
      }
      return [...unique.values()];
    },
    enabled,
    staleTime: 0,
  });
  const options = useMemo(
    () => (enabled ? (identities.data ?? []) : []),
    [enabled, identities.data],
  );
  const searching = enabled && identities.isFetching;
  const searchError = enabled ? identities.error : null;

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
          placeholder ?? t("repository.searchByPackagePathOrCoordinate")
        }
        showSearch={{ filterOption: false, onSearch: search }}
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
                <div className="truncate font-mono text-xs text-fg">
                  {artifactCoordinateForDisplay(
                    repo.format,
                    artifact.coordinate,
                  )}
                </div>
                <div className="mt-0.5 flex flex-wrap gap-x-2 text-xs text-fg-tertiary">
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
            ? t("repository.searching")
            : t("repository.noSelectableArtifacts")
        }
        onChange={(key) => {
          const option = selectableOptions.find(
            (candidate) => candidate.key === key,
          );
          if (option) onChange(option);
        }}
        allowClear
        onClear={() => {
          window.clearTimeout(searchTimer.current);
          setQuery("");
          onChange(null);
        }}
        listHeight={320}
      />
      {searchError !== null && (
        <p className="mt-1 text-xs text-[var(--ag-status-danger)]" role="alert">
          {t("repository.artifactSearchFailedChangeTheQueryAndTry")}
        </p>
      )}
    </>
  );
}
