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
  const { t } = usePreferences();
  return (
    <Alert
      type={evaluation.allowed ? "success" : "warning"}
      showIcon
      title={
        artifactQuarantined
          ? t("repository.artifactIsQuarantinedAndCannotBePromotedOr")
          : evaluation.allowed
            ? t("repository.securityPolicyAllowsPromotion")
            : t("repository.securityPolicyBlocksPromotion")
      }
      description={
        <div className="space-y-2 text-xs">
          {artifactQuarantined && (
            <p>
              {t("repository.releaseTheArtifactFromItsDetailsThenEvaluate")}
            </p>
          )}
          <div className="flex flex-wrap gap-x-5 gap-y-1 text-fg-tertiary">
            <span>
              {t("repository.policyVersion")}: {evaluation.policyVersion}
            </span>
            <span>
              {t("repository.enforcement")}:{" "}
              {evaluation.enforced
                ? t("repository.enabled2")
                : t("repository.disabled2")}
            </span>
            <span>
              {t("repository.artifactIntelligence")}:{" "}
              {evaluation.intelligencePresent
                ? t("repository.found")
                : t("repository.notFound")}
            </span>
          </div>
          {evaluation.reasons.length > 0 && (
            <div className="flex flex-wrap gap-2">
              {evaluation.reasons.map((reason) => (
                <span
                  className="rounded border border-current/20 px-2 py-1 font-mono"
                  key={reason}
                >
                  {securityReason(reason, t)}
                </span>
              ))}
            </div>
          )}
        </div>
      }
    />
  );
}
