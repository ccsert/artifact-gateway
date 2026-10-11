import {
  MetadataItem,
  UsageSnippetBlock,
} from "../../components/ui/PublicBrowsePrimitives";
import { formatDate } from "../../lib/format";
import { usePreferences } from "../../lib/preferences";
import { artifactCoordinateForDisplay } from "../../lib/rawPath";
import type { UsageSnippet } from "../../lib/usage";
import { APTAssetDetail } from "../artifact-detail/APTAssetDetail";
import { CargoCrateDetail } from "../artifact-detail/CargoCrateDetail";
import { GoModuleDetail } from "../artifact-detail/GoModuleDetail";
import { NpmPackageDetail } from "../artifact-detail/NpmPackageDetail";
import { PyPIProjectDetail } from "../artifact-detail/PyPIProjectDetail";
import { OciVersionPanel } from "./OciVersionPanel";
import type { PublicArtifactTableRow } from "./publicArtifactRows";
import type { PublicRepository } from "./types";

/** What an expanded artifact row shows, per format. */
export function PublicArtifactDetail({
  row,
  repository,
  artifactParam,
  versionParam,
  copiedCoordinate,
  onOpenArtifact,
  onSelectOciVersion,
  onLoadOciTags,
  onLoadOciManifest,
  onCopyPageLink,
  onCopyUsage,
}: {
  row: PublicArtifactTableRow;
  repository: PublicRepository;
  artifactParam: string;
  versionParam: string;
  copiedCoordinate: string | null;
  onOpenArtifact: (
    coordinate: string,
    buildNumber?: number,
    tag?: string,
    revision?: string,
    version?: string,
  ) => void;
  onSelectOciVersion: (coordinate: string, value: string) => void;
  onLoadOciTags: (coordinate: string, after?: string) => Promise<void>;
  onLoadOciManifest: (coordinate: string, tag: string) => Promise<void>;
  onCopyPageLink: (href: string) => unknown;
  onCopyUsage: (snippet: UsageSnippet) => unknown;
}) {
  const { locale, text } = usePreferences();
  const selectedRepository: PublicRepository | undefined = repository;
  const openArtifact = onOpenArtifact;
  const copyUsage = onCopyUsage;
  if (selectedRepository?.format === "npm") {
    return (
      <NpmPackageDetail
        repoName={selectedRepository.name}
        packageName={row.item.coordinate}
        initialVersion={
          artifactParam === row.item.coordinate
            ? versionParam || row.item.version
            : row.item.version
        }
        size={row.item.size}
        publisher={row.item.publisher}
        onVersionChange={(version) =>
          openArtifact(
            row.item.coordinate,
            undefined,
            undefined,
            undefined,
            version,
          )
        }
      />
    );
  }
  if (selectedRepository?.format === "pypi") {
    return (
      <PyPIProjectDetail
        repoName={selectedRepository.name}
        project={row.item.coordinate}
        initialVersion={
          artifactParam === row.item.coordinate
            ? versionParam || row.item.version
            : row.item.version
        }
        size={row.item.size}
        publisher={row.item.publisher}
        onVersionChange={(version) =>
          openArtifact(
            row.item.coordinate,
            undefined,
            undefined,
            undefined,
            version,
          )
        }
      />
    );
  }
  if (selectedRepository?.format === "go") {
    return (
      <GoModuleDetail
        repoName={selectedRepository.name}
        modulePath={row.item.coordinate}
        initialVersion={
          artifactParam === row.item.coordinate
            ? versionParam || row.item.version
            : row.item.version
        }
        size={row.item.size}
        publisher={row.item.publisher}
        onVersionChange={(version) =>
          openArtifact(
            row.item.coordinate,
            undefined,
            undefined,
            undefined,
            version,
          )
        }
      />
    );
  }
  if (selectedRepository?.format === "cargo") {
    return (
      <CargoCrateDetail
        repoName={selectedRepository.name}
        crateName={row.item.coordinate}
        initialVersion={
          artifactParam === row.item.coordinate
            ? versionParam || row.item.version
            : row.item.version
        }
        onVersionChange={(version) =>
          openArtifact(
            row.item.coordinate,
            undefined,
            undefined,
            undefined,
            version,
          )
        }
      />
    );
  }
  if (selectedRepository?.format === "apt") {
    return (
      <APTAssetDetail
        repoName={selectedRepository.name}
        meta={{
          coordinate: row.item.coordinate,
          digest: row.item.digest,
          size: row.item.size,
          contentType: row.item.contentType,
          createdAt: row.item.createdAt,
          cachedAt: row.item.cachedAt,
          sourceUrl: row.item.sourceUrl,
        }}
      />
    );
  }
  if (!row.isOci) {
    return (
      <div className="px-2 py-1">
        <div className="mb-4 grid grid-cols-2 gap-x-4 gap-y-3 border-b border-line pb-4 text-xs sm:grid-cols-4">
          <MetadataItem
            label={text("仓库", "Repository")}
            value={selectedRepository?.name ?? "—"}
            mono
          />
          <MetadataItem
            label={text("制品坐标", "Artifact coordinate")}
            value={artifactCoordinateForDisplay(
              selectedRepository?.format ?? "",
              row.item.coordinate,
            )}
            mono
          />
          <MetadataItem
            label={text("发布时间", "Published")}
            value={formatDate(row.item.createdAt, locale)}
          />
          <MetadataItem
            label={text("发布者", "Publisher")}
            value={row.item.publisher ?? text("未记录", "Not recorded")}
            mono
          />
        </div>
        <div className="grid gap-2 sm:grid-cols-2">
          {row.snippets.map((snippet) => (
            <UsageSnippetBlock
              key={snippet.label}
              snippet={snippet}
              copied={copiedCoordinate === snippet.code}
              onCopy={() => void copyUsage(snippet)}
            />
          ))}
        </div>
      </div>
    );
  }
  return (
    <OciVersionPanel
      row={row}
      copiedCoordinate={copiedCoordinate}
      onSelectVersion={(value) =>
        onSelectOciVersion(row.item.coordinate, value)
      }
      onLoadOciTags={onLoadOciTags}
      onLoadOciManifest={onLoadOciManifest}
      onCopyPageLink={onCopyPageLink}
      onCopyUsage={onCopyUsage}
    />
  );
}
