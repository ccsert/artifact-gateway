import type { MigrationSource, MigrationSourceStats } from "../../client";
import type { MigrationMessageKey } from "../../messages/migration.zh-CN";
import { LocalizedError, type Translator } from "../../messages/translate";
import { formatBytes } from "../../lib/format";

const failures: Record<string, MigrationMessageKey> = {
  invalid_request: "migration.invalidSource",
  invalid_source: "migration.invalidSource",
  credential_replacement_required: "migration.replaceRequired",
  encryption_key_unavailable: "migration.keyUnavailable",
  target_lookup_unavailable: "migration.lookupUnavailable",
  source_egress_denied: "migration.upstreamDenied",
  source_unauthorized: "migration.upstreamDenied",
  source_forbidden: "migration.upstreamDenied",
  source_repository_not_visible: "migration.upstreamDenied",
  source_access_denied: "migration.upstreamDenied",
  source_authentication_failed: "migration.upstreamDenied",
  source_permission_denied: "migration.upstreamDenied",
  source_budget_exceeded: "migration.budget",
  source_limit_exceeded: "migration.budget",
  source_pagination_cycle: "migration.budget",
  source_pagination_invalid: "migration.budget",
  source_timeout: "migration.sourceUnavailable",
  source_tls_failed: "migration.sourceUnavailable",
  source_unreachable: "migration.sourceUnavailable",
  source_invalid_response: "migration.sourceUnavailable",
  source_download_url_denied: "migration.sourceUnavailable",
  source_redirect_denied: "migration.sourceUnavailable",
  source_api_unsupported: "migration.sourceUnavailable",
  source_http_error: "migration.sourceUnavailable",
};

/** Do not render a transport/upstream message that may echo submitted secrets. */
export function migrationError(error: unknown): LocalizedError {
  if (error instanceof LocalizedError) return error;
  const problem = error as { status?: unknown; code?: unknown } | null;
  const status =
    typeof problem?.status === "number" ? problem.status : undefined;
  const key =
    status === 401 || status === 403
      ? "migration.denied"
      : status === 404
        ? "migration.notFound"
        : status === 412
          ? "migration.versionConflict"
          : typeof problem?.code === "string" &&
              Object.hasOwn(failures, problem.code)
            ? failures[problem.code]
            : status === 502 || status === 504
              ? "migration.sourceUnavailable"
              : "migration.failed";
  return new LocalizedError(key, undefined, { status });
}

export function migrationReadDenied(error: unknown) {
  const status = (error as { status?: unknown } | null)?.status;
  return status === 401 || status === 403;
}

export async function migrationRequest<T>(
  request: Promise<{ data?: T; error?: unknown }>,
): Promise<T> {
  try {
    const result = await request;
    if (result.error != null) throw result.error;
    return result.data as T;
  } catch (error) {
    throw migrationError(error);
  }
}

export function assertSourceSnapshot(
  result: { sourceId: string; sourceVersion: string },
  source: MigrationSource,
) {
  if (result.sourceId !== source.id || result.sourceVersion !== source.version)
    throw new LocalizedError("migration.sourceChanged");
}

const reasons: Record<string, MigrationMessageKey> = {
  hosted_read_only_inventory: "migration.reasonHostedInventory",
  maven_release_policy_required: "migration.reasonReleasePolicy",
  proxy_configuration_only: "migration.reasonManual",
  group_configuration_only: "migration.reasonManual",
  maven_snapshot_unsupported: "migration.reasonSnapshot",
  source_snapshot_unsupported: "migration.reasonSnapshot",
  maven_snapshot_path_unsupported: "migration.reasonSnapshot",
  maven_shared_metadata_unsupported: "migration.reasonMetadata",
  maven_unclassified_path_unsupported: "migration.reasonMetadata",
  maven_mixed_metadata_requires_regeneration: "migration.reasonMetadata",
  maven_path_unclassifiable: "migration.reasonMetadata",
  configure_native_policies_and_credentials_explicitly:
    "migration.reasonManual",
  existing_target_conflict: "migration.reasonConflict",
  target_name_invalid_or_duplicate: "migration.reasonName",
  target_format_mismatch: "migration.reasonFormat",
  target_type_mismatch: "migration.reasonType",
  group_member_mapping_missing: "migration.reasonMembers",
  group_member_mapping_invalid: "migration.reasonMembers",
  group_member_order_mismatch: "migration.reasonMembers",
  existing_group_requires_explicit_configuration:
    "migration.reasonExistingGroup",
  members_require_group: "migration.reasonMembersOnly",
};

export function migrationReason(reason: string, t: Translator): string {
  return t(
    Object.hasOwn(reasons, reason)
      ? reasons[reason]
      : "migration.reasonUnsupported",
  );
}

export function observation(stats: MigrationSourceStats, t: Translator) {
  return t("migration.observation", {
    requests: stats.requests,
    pages: stats.pages,
    bytes: formatBytes(stats.responseBytes),
  });
}

export function validSourceUrl(value: string): boolean {
  try {
    const url = new URL(value);
    return (
      url.protocol === "https:" &&
      !url.username &&
      !url.password &&
      !value.includes("?") &&
      !value.includes("#") &&
      !value.includes("%") &&
      !value.includes("\\") &&
      !/[\r\n]/.test(value) &&
      !value.includes("\0")
    );
  } catch {
    return false;
  }
}
