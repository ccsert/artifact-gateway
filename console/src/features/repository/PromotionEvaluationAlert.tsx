import { Alert } from "antd";
import type { SecurityPolicyEvaluation } from "../../client";
import { usePreferences } from "../../lib/preferences";

import { securityReason } from "./distributionModel";

/** The target's security-policy answer for the chosen artifact. */
export function PromotionEvaluationAlert({
  evaluation,
  artifactQuarantined,
}: {
  evaluation: SecurityPolicyEvaluation;
  artifactQuarantined: boolean;
}) {
  const { text } = usePreferences();
  return (
    <Alert
      type={evaluation.allowed ? "success" : "warning"}
      showIcon
      title={text(
        artifactQuarantined
          ? "制品已隔离，无法晋升或复制"
          : evaluation.allowed
            ? "安全策略允许晋升"
            : "安全策略阻止晋升",
        artifactQuarantined
          ? "Artifact is quarantined and cannot be promoted or replicated"
          : evaluation.allowed
            ? "Security policy allows promotion"
            : "Security policy blocks promotion",
      )}
      description={
        <div className="space-y-2 text-xs">
          {artifactQuarantined && (
            <p>
              {text(
                "请先在制品详情中解除隔离，然后重新评估准入。",
                "Release the artifact from its details, then evaluate admission again.",
              )}
            </p>
          )}
          <div className="flex flex-wrap gap-x-5 gap-y-1 text-fg-tertiary">
            <span>
              {text("策略版本", "Policy version")}: {evaluation.policyVersion}
            </span>
            <span>
              {text("策略状态", "Enforcement")}:{" "}
              {evaluation.enforced
                ? text("已启用", "enabled")
                : text("未启用", "disabled")}
            </span>
            <span>
              {text("制品情报", "Artifact intelligence")}:{" "}
              {evaluation.intelligencePresent
                ? text("已找到", "found")
                : text("未找到", "not found")}
            </span>
          </div>
          {evaluation.reasons.length > 0 && (
            <div className="flex flex-wrap gap-2">
              {evaluation.reasons.map((reason) => (
                <span
                  className="rounded border border-current/20 px-2 py-1 font-mono"
                  key={reason}
                >
                  {securityReason(reason, text)}
                </span>
              ))}
            </div>
          )}
        </div>
      }
    />
  );
}
