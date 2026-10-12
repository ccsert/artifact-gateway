/** A security-policy refusal reason in the reader's language. */
export function securityReason(
  reason: string,
  text: (zh: string, en: string) => string,
) {
  const labels: Record<string, [string, string]> = {
    artifact_quarantined: [
      "制品已隔离；请先解除隔离",
      "Artifact is quarantined; release it first",
    ],
    policy_disabled: ["策略未启用", "Policy is disabled"],
    signature_required: ["缺少签名", "Signature is required"],
    verified_signature_required: [
      "缺少已验证签名",
      "A verified signature is required",
    ],
    sbom_required: ["缺少 SBOM", "An SBOM is required"],
    provenance_required: ["缺少 provenance", "Provenance is required"],
    vulnerability_scan_required: [
      "缺少漏洞扫描结果",
      "A vulnerability scan is required",
    ],
    license_required: ["缺少许可证信息", "License information is required"],
    license_not_allowed: [
      "许可证不在白名单中",
      "A license is not allow-listed",
    ],
    vulnerability_scan_error: ["漏洞扫描失败", "The vulnerability scan failed"],
    low_vulnerabilities: ["存在低危漏洞", "Low vulnerabilities found"],
    medium_vulnerabilities: ["存在中危漏洞", "Medium vulnerabilities found"],
    high_vulnerabilities: ["存在高危漏洞", "High vulnerabilities found"],
    critical_vulnerabilities: [
      "存在严重漏洞",
      "Critical vulnerabilities found",
    ],
    unknown_vulnerabilities: [
      "存在未知等级漏洞",
      "Unknown-severity vulnerabilities found",
    ],
  };
  const label = labels[reason];
  return label ? text(label[0], label[1]) : reason;
}
