import type { ArtifactSummary } from "../../client";
import type { ConanArtifactGroup } from "../../lib/publicBrowseModel";
import type { UsageSnippet } from "../../lib/usage";
import type { PublicRepository } from "./types";

export interface ConanRevision {
  revision: string;
  digest?: string;
  createdAt?: string;
}

export interface ConanRevisionPage {
  items: ConanRevision[];
  nextPageToken?: string;
  query: string;
  loaded: boolean;
  loading: boolean;
  error?: string;
}

export interface ConanGroupTableProps {
  groups: ConanArtifactGroup[];
  repository: PublicRepository;
  artifactParam: string;
  revisionParam: string;
  expandedGroup: string | null;
  selectedReferences: Record<string, string>;
  selectedRevisions: Record<string, string>;
  revisionPages: Record<string, ConanRevisionPage>;
  versionFilter: string;
  copiedCoordinate: string | null;
  onExpand: (key: string, reference: string) => void;
  onCollapse: () => void;
  onSelectReference: (key: string, reference: string) => void;
  onSelectRevision: (reference: string, revision: string) => void;
  onFilterChange: (value: string) => void;
  onLoadRevisions: (
    reference: string,
    query?: string,
    pageToken?: string,
  ) => void;
  onOpenArtifact: (reference: string, revision?: string) => void;
  onClearArtifactParams: () => void;
  artifactHref: (reference: string, revision?: string) => string;
  onCopyCoordinate: (value: string) => void;
  onCopyPageLink: (value: string) => void;
  onCopyUsage: (snippet: UsageSnippet) => void;
}

export interface ConanTableRow {
  key: string;
  group: ConanArtifactGroup;
  latest: ArtifactSummary;
  selectedReference: string;
  expanded: boolean;
  page?: ConanRevisionPage;
  revisions: ConanRevision[];
  visibleRevisions: ConanRevision[];
  selectedRevisionValue: string;
  selectedRevisionItem?: ConanRevision;
  referenceVersion: string;
  versionHref: string;
  snippets: UsageSnippet[];
}
