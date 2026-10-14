import type { MessageKey } from "../../messages";
import type { Translate } from "../../lib/preferences";
import type { RepositoryQuotaAlertPolicy } from "../../client";
export function isQuotaAuthError(error: unknown) {
  return (
    typeof error === "object" &&
    error !== null &&
    "code" in error &&
    [
      "password_change_required",
      "permission_denied",
      "forbidden",
      "access_denied",
      "unauthenticated",
    ].includes(String(error.code))
  );
}
const labels: Record<string, MessageKey> = {
  normal: "system.notFiring",
  warning: "system.warning",
  critical: "system.critical",
  resolved: "system.recovered",
  available: "system.sampleAvailable",
  unknown: "system.capacityUnknown",
  stale: "system.sampleStale",
  not_configured: "system.repositoryQuotaNotConfigured",
  configuration_changed: "system.configurationChanged",
  disabled: "system.disabled",
  deleted: "system.deletedHistoryRetained",
  repository_deleted: "system.repositoryDeleted",
  repository_inactive: "system.repositoryInactive",
  pending: "system.pendingDelivery",
  delivering: "system.delivering",
  retrying: "system.retryScheduled",
  accepted: "system.acceptedBySMTPServer",
  dead: "system.deliveryStopped",
  queued: "system.queuedForDelivery",
  queue_full: "system.deliveryQueueFull",
  email_disabled: "system.emailChannelDisabled",
  encryption_key_unavailable: "system.encryptionKeyUnavailable",
  target_disabled: "system.emailTargetDisabled",
  target_changed: "system.emailTargetVersionChanged",
  target_unavailable: "system.emailTargetUnavailable",
  rule_changed: "system.automaticDeliveryCancelledAfterRuleChange",
  rule_disabled: "system.automaticDeliveryCancelledAfterDisablingTheRule",
  rule_deleted: "system.automaticDeliveryCancelledAfterDeletingTheRule",
  network_error: "system.networkConnectionFailed",
  tls_error: "system.tLSVerificationFailed",
  smtp_rejected: "system.sMTPServerRejectedTheMessage",
  smtp_temporary: "system.temporarySMTPFailure",
  lease_expired: "system.deliveryLeaseExpired",
  attempts_exhausted: "system.retryAttemptsExhausted",
  invalid_message: "system.messageValidationFailed",
  relay_resolution_failed: "system.emailRelayResolutionFailed",
  relay_address_denied: "system.emailRelayAddressNotApproved",
  relay_connect_failed: "system.emailRelayConnectionFailed",
  tls_configuration_failed: "system.tLSConfigurationUnavailable",
  tls_verification_failed: "system.tLSVerificationFailed",
  tls_required: "system.requiredTLSUnavailable",
  authentication_configuration_failed:
    "system.emailAuthenticationConfigurationUnavailable",
  authentication_failed: "system.emailAuthenticationFailed",
  smtp_permanent_rejection: "system.permanentSMTPRejection",
  smtp_temporary_rejection: "system.temporarySMTPRejection",
  smtp_transport_failed: "system.sMTPTransportFailed",
  outcome_unknown: "system.deliveryOutcomeUnknown",
};
export function quotaLabel(code: string, t: Translate) {
  const label = Object.hasOwn(labels, code) ? labels[code] : undefined;
  return label ? t(label) : t("system.statusUnknown");
}

const errors: Record<string, MessageKey> = {
  version_conflict: "system.anotherAdministratorChangedThisRuleGoBackRefresh",
  password_change_required:
    "system.changeYourPasswordBeforeReturningToThisPage",
  permission_denied:
    "system.permissionDeniedConfirmPlatformAdministratorAccessAndRefresh",
  forbidden:
    "system.permissionDeniedConfirmPlatformAdministratorAccessAndRefresh",
  access_denied: "system.thisSessionCannotPerformThisActionSignIn",
  unauthenticated: "system.theSessionHasExpiredSignInAgain",
  rule_conflict: "system.thisRepositoryAlreadyHasARuleRefreshAnd",
  rule_limit: "system.theRuleLimitHasBeenReachedIncludingRetained",
  rule_deleted: "system.thisRuleWasDeletedGoBackAndRefresh",
  repository_inactive: "system.thisRepositoryIsUnavailableForAlertingCheckIts",
  invalid_request:
    "system.configurationValidationFailedCheckThresholdsHoldDurationsAnd",
  not_found: "system.theResourceOrManagementEndpointIsUnavailableRefresh",
  target_disabled: "system.theEmailTargetWasDisabledRefreshAndChoose",
  email_disabled: "system.theEmailChannelIsDisabledTheRuleCannot",
  encryption_key_unavailable:
    "system.encryptionKeyUnavailableTheRuleCannotBeEnabled",
};
export function safeQuotaError(error: unknown, t: Translate): string {
  const code =
    typeof error === "object" &&
    error !== null &&
    "code" in error &&
    typeof error.code === "string"
      ? error.code
      : "";
  const label = Object.hasOwn(errors, code) ? errors[code] : undefined;
  return label
    ? t(label)
    : t("system.requestFailedCheckTheConnectionAndRefreshBefore");
}

export function percent(value: number) {
  return `${value / 100}%`;
}
export function sampleBytes(value: number | undefined, t: Translate) {
  if (value === undefined || !Number.isFinite(value) || value < 0)
    return t("system.unknown");
  const approximate = !Number.isSafeInteger(value);
  const units = ["B", "KiB", "MiB", "GiB", "TiB", "PiB", "EiB"];
  const unit =
    value === 0 ? 0 : Math.min(6, Math.floor(Math.log(value) / Math.log(1024)));
  return `${approximate ? "≈ " : ""}${(value / 1024 ** unit).toLocaleString(undefined, { maximumFractionDigits: 2 })} ${units[unit]}`;
}

export type PolicyDraft = Record<keyof RepositoryQuotaAlertPolicy, string>;
export function policyDraft(policy?: RepositoryQuotaAlertPolicy): PolicyDraft {
  return {
    warningBasisPoints: policy ? String(policy.warningBasisPoints / 100) : "",
    criticalBasisPoints: policy ? String(policy.criticalBasisPoints / 100) : "",
    recoveryBelowBasisPoints: policy
      ? String(policy.recoveryBelowBasisPoints / 100)
      : "",
    warningForSeconds: policy ? String(policy.warningForSeconds) : "",
    criticalForSeconds: policy ? String(policy.criticalForSeconds) : "",
    recoveryForSeconds: policy ? String(policy.recoveryForSeconds) : "",
    maxSampleAgeSeconds: policy ? String(policy.maxSampleAgeSeconds) : "",
  };
}
type PolicyError =
  "percent" | "seconds" | "sampleAge" | "recoveryOrder" | "warningOrder";
function policyValues(draft: PolicyDraft): RepositoryQuotaAlertPolicy {
  const percent = (value: string) => {
    if (!/^\d{1,3}(?:\.\d{1,2})?$/.test(value.trim())) return NaN;
    const [whole, fraction = ""] = value.trim().split(".");
    return Number(whole) * 100 + Number(fraction.padEnd(2, "0"));
  };
  const seconds = (value: string) =>
    /^\d{1,5}$/.test(value.trim()) ? Number(value.trim()) : NaN;
  return {
    warningBasisPoints: percent(draft.warningBasisPoints),
    criticalBasisPoints: percent(draft.criticalBasisPoints),
    recoveryBelowBasisPoints: percent(draft.recoveryBelowBasisPoints),
    warningForSeconds: seconds(draft.warningForSeconds),
    criticalForSeconds: seconds(draft.criticalForSeconds),
    recoveryForSeconds: seconds(draft.recoveryForSeconds),
    maxSampleAgeSeconds: seconds(draft.maxSampleAgeSeconds),
  };
}
export function policyErrors(draft: PolicyDraft) {
  const values = policyValues(draft);
  const errors: Partial<Record<keyof RepositoryQuotaAlertPolicy, PolicyError>> =
    {};
  for (const key of [
    "warningBasisPoints",
    "criticalBasisPoints",
    "recoveryBelowBasisPoints",
  ] as const) {
    if (!(values[key] > 0 && values[key] <= 10000)) errors[key] = "percent";
  }
  for (const key of [
    "warningForSeconds",
    "criticalForSeconds",
    "recoveryForSeconds",
  ] as const) {
    if (!(values[key] >= 1 && values[key] <= 86400)) errors[key] = "seconds";
  }
  if (!(values.maxSampleAgeSeconds >= 30 && values.maxSampleAgeSeconds <= 3600))
    errors.maxSampleAgeSeconds = "sampleAge";
  if (
    !errors.recoveryBelowBasisPoints &&
    !errors.warningBasisPoints &&
    values.recoveryBelowBasisPoints >= values.warningBasisPoints
  ) {
    errors.recoveryBelowBasisPoints = "recoveryOrder";
    errors.warningBasisPoints = "recoveryOrder";
  }
  if (
    !errors.criticalBasisPoints &&
    Number.isFinite(values.warningBasisPoints) &&
    values.warningBasisPoints >= values.criticalBasisPoints
  ) {
    errors.warningBasisPoints = "warningOrder";
    errors.criticalBasisPoints = "warningOrder";
  }
  return errors;
}
/** The reader-facing explanation of one policy field error. */
export function policyErrorMessage(
  error: PolicyError | undefined,
  t: Translate,
) {
  switch (error) {
    case "percent":
      return t("system.enterAPercentageAbove0AndAtMost");
    case "seconds":
      return t("system.enterIntegerSecondsFrom186400");
    case "sampleAge":
      return t("system.enterIntegerSecondsFrom303600");
    case "recoveryOrder":
      return t("system.recoveryMustBeBelowTheWarningThreshold");
    case "warningOrder":
      return t("system.warningMustBeBelowTheCriticalThreshold");
  }
}

export function parsePolicy(
  draft: PolicyDraft,
): RepositoryQuotaAlertPolicy | undefined {
  return Object.keys(policyErrors(draft)).length
    ? undefined
    : policyValues(draft);
}
