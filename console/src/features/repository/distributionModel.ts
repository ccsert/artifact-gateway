import type { MessageKey } from "../../messages";
import type { Translate } from "../../lib/preferences";
/** A security-policy refusal reason in the reader's language. */
export function securityReason(reason: string, t: Translate) {
  const labels: Record<string, MessageKey> = {
    artifact_quarantined: "repository.artifactIsQuarantinedReleaseItFirst",
    policy_disabled: "repository.policyIsDisabled",
    signature_required: "repository.signatureIsRequired",
    verified_signature_required: "repository.aVerifiedSignatureIsRequired",
    sbom_required: "repository.anSBOMIsRequired",
    provenance_required: "repository.provenanceIsRequired",
    vulnerability_scan_required: "repository.aVulnerabilityScanIsRequired",
    license_required: "repository.licenseInformationIsRequired",
    license_not_allowed: "repository.aLicenseIsNotAllowListed",
    vulnerability_scan_error: "repository.theVulnerabilityScanFailed",
    low_vulnerabilities: "repository.lowVulnerabilitiesFound",
    medium_vulnerabilities: "repository.mediumVulnerabilitiesFound",
    high_vulnerabilities: "repository.highVulnerabilitiesFound",
    critical_vulnerabilities: "repository.criticalVulnerabilitiesFound",
    unknown_vulnerabilities: "repository.unknownSeverityVulnerabilitiesFound",
  };
  const label = labels[reason];
  return label ? t(label) : reason;
}
