package repository

import "strings"

const (
	SecuritySeverityNone     = "none"
	SecuritySeverityLow      = "low"
	SecuritySeverityMedium   = "medium"
	SecuritySeverityHigh     = "high"
	SecuritySeverityCritical = "critical"
)

func DefaultRepositorySecurityPolicy() RepositorySecurityPolicy {
	return RepositorySecurityPolicy{
		Version:            "1",
		MaxAllowedSeverity: SecuritySeverityCritical,
		FailOnScanError:    true,
		AllowedLicenses:    []string{},
	}
}

func CloneRepositorySecurityPolicy(policy RepositorySecurityPolicy) RepositorySecurityPolicy {
	policy.AllowedLicenses = append([]string{}, policy.AllowedLicenses...)
	return policy
}

func ValidSecuritySeverity(value string) bool {
	switch value {
	case SecuritySeverityNone, SecuritySeverityLow, SecuritySeverityMedium, SecuritySeverityHigh, SecuritySeverityCritical:
		return true
	default:
		return false
	}
}

// EvaluateRepositorySecurityPolicy returns stable reason codes so clients can
// render localized explanations without parsing prose.
func EvaluateRepositorySecurityPolicy(policy RepositorySecurityPolicy, intelligence *ArtifactIntelligence) SecurityPolicyEvaluation {
	policy = CloneRepositorySecurityPolicy(policy)
	result := SecurityPolicyEvaluation{Allowed: true, Enforced: policy.Enabled, PolicyVersion: policy.Version, Reasons: []string{}}
	if !policy.Enabled {
		result.Reasons = []string{"policy_disabled"}
		return result
	}
	if intelligence == nil {
		result.IntelligencePresent = false
		if policy.RequireSignature {
			result.Reasons = append(result.Reasons, "signature_required")
		}
		if policy.RequireVerifiedSignature {
			result.Reasons = append(result.Reasons, "verified_signature_required")
		}
		if policy.RequireSBOM {
			result.Reasons = append(result.Reasons, "sbom_required")
		}
		if policy.RequireProvenance {
			result.Reasons = append(result.Reasons, "provenance_required")
		}
		if policy.RequireVulnerabilityScan {
			result.Reasons = append(result.Reasons, "vulnerability_scan_required")
		}
		if len(policy.AllowedLicenses) > 0 {
			result.Reasons = append(result.Reasons, "license_required")
		}
		result.Allowed = len(result.Reasons) == 0
		return result
	}
	result.IntelligencePresent = true
	if policy.RequireSignature && len(intelligence.Signatures) == 0 {
		result.Reasons = append(result.Reasons, "signature_required")
	}
	if policy.RequireVerifiedSignature {
		verified := false
		for _, signature := range intelligence.Signatures {
			if signature.Verified {
				verified = true
				break
			}
		}
		if !verified {
			result.Reasons = append(result.Reasons, "verified_signature_required")
		}
	}
	if policy.RequireSBOM && len(intelligence.SBOMs) == 0 {
		result.Reasons = append(result.Reasons, "sbom_required")
	}
	if policy.RequireProvenance && intelligence.Provenance == nil {
		result.Reasons = append(result.Reasons, "provenance_required")
	}
	if len(policy.AllowedLicenses) > 0 {
		allowed := make(map[string]struct{}, len(policy.AllowedLicenses))
		for _, license := range policy.AllowedLicenses {
			allowed[strings.ToLower(license)] = struct{}{}
		}
		if len(intelligence.Licenses) == 0 {
			result.Reasons = append(result.Reasons, "license_required")
		} else {
			for _, license := range intelligence.Licenses {
				if _, ok := allowed[strings.ToLower(license.SPDXID)]; !ok {
					result.Reasons = append(result.Reasons, "license_not_allowed")
					break
				}
			}
		}
	}
	vulnerability := intelligence.Vulnerability
	if policy.RequireVulnerabilityScan && (vulnerability == nil || vulnerability.Status == "not_scanned") {
		result.Reasons = append(result.Reasons, "vulnerability_scan_required")
	}
	if vulnerability != nil {
		if vulnerability.Status == "error" && policy.FailOnScanError {
			result.Reasons = append(result.Reasons, "vulnerability_scan_error")
		}
		if vulnerability.Status == "affected" {
			max := securitySeverityRank(policy.MaxAllowedSeverity)
			for _, finding := range []struct {
				severity string
				count    int
			}{
				{severity: "low", count: vulnerability.Low},
				{severity: "medium", count: vulnerability.Medium},
				{severity: "high", count: vulnerability.High},
				{severity: "critical", count: vulnerability.Critical},
				{severity: "unknown", count: vulnerability.Unknown},
			} {
				severity, count := finding.severity, finding.count
				if count > 0 && securitySeverityRank(severity) > max {
					result.Reasons = append(result.Reasons, severity+"_vulnerabilities")
				}
			}
		}
	}
	result.Allowed = len(result.Reasons) == 0
	return result
}

func securitySeverityRank(value string) int {
	switch value {
	case SecuritySeverityLow:
		return 1
	case SecuritySeverityMedium:
		return 2
	case SecuritySeverityHigh:
		return 3
	case SecuritySeverityCritical:
		return 4
	case "unknown":
		return 5
	default:
		return 0
	}
}
