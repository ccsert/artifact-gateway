import type {
  AptLifecyclePackage,
  AptLifecyclePlan,
  AptLifecycleRequest,
  AptSnapshotHistory,
} from "../../client";

export type Review = {
  request: AptLifecycleRequest;
  plan: AptLifecyclePlan;
  key: string;
};

export function packageName(item: AptLifecyclePackage) {
  return `${item.revision.package} ${item.revision.version} · ${item.revision.architecture}`;
}

// The Gateway restores an archive only against the exact `sha256:<hex>` receipt
// the operator saved at backup time, so the form validates the shape up front
// instead of letting the server reject an obviously malformed value.
export const APT_ARCHIVE_RECEIPT = /^sha256:[0-9a-f]{64}$/;

// The exporter accepts a visible or retired snapshot only; a pruned snapshot
// has lost its index references and the Gateway rejects it with 409.
export function archiveExportable(item: AptSnapshotHistory) {
  return item.snapshot.state === "visible" || item.snapshot.state === "retired";
}
