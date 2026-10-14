import { useState } from "react";
import { Button, Select, Switch, Tag } from "antd";
import { replaceSecurityPolicy } from "../../client";
import type { Repository, SecurityPolicy } from "../../client";
import { ErrorBanner, Notice } from "../../components/ui/Feedback";
import { Field } from "../../components/ui/Layout";
import { usePreferences } from "../../lib/preferences";
import {
  PolicyCard,
  PolicySectionHeader,
} from "./RepositorySecurityPrimitives";
import { formatDescriptor } from "../../formats";

import {
  draftFromPolicy,
  PolicySwitch,
  SettingRow,
  type SecurityPolicyDraft,
} from "./securityPolicyParts";
import { useSetSecurityPolicy } from "./repositoryQueries";

/** Which artifacts may enter this repository by promotion. */
export function AdmissionPolicyCard({
  repo,
  policy,
  publicationScanning,
}: {
  repo: Repository;
  policy: SecurityPolicy;
  /** New publications can enqueue scans automatically. */
  publicationScanning: boolean;
}) {
  const { t } = usePreferences();
  const setStoredPolicy = useSetSecurityPolicy(repo.id);
  const [draft, setDraft] = useState<SecurityPolicyDraft>(() =>
    draftFromPolicy(policy),
  );
  const [saveError, setSaveError] = useState<unknown>(null);
  const [saving, setSaving] = useState(false);
  const [notice, setNotice] = useState("");
  const [draftSource, setDraftSource] = useState({
    repositoryId: repo.id,
    version: policy.version,
  });
  // A cached policy can render before its background read finishes. Keep the
  // editable values and the version used by If-Match from the same answer.
  if (
    draftSource.repositoryId !== repo.id ||
    draftSource.version !== policy.version
  ) {
    if (draftSource.repositoryId !== repo.id) setNotice("");
    setDraftSource({ repositoryId: repo.id, version: policy.version });
    setDraft(draftFromPolicy(policy));
    setSaveError(null);
  }
  const update = <K extends keyof SecurityPolicyDraft>(
    key: K,
    value: SecurityPolicyDraft[K],
  ) => {
    setNotice("");
    setSaveError(null);
    setDraft((current) => ({ ...current, [key]: value }));
  };

  const save = async () => {
    if (!policy) return;
    setSaving(true);
    setSaveError(null);
    setNotice("");
    try {
      const { data, error: err } = await replaceSecurityPolicy({
        path: { repositoryId: repo.id },
        headers: { "If-Match": policy.version },
        body: { version: policy.version, ...draft },
      });
      if (err) {
        setSaveError(err);
        return;
      }
      setNotice(t("repository.repositorySecurityPolicySaved"));
      if (data) {
        setStoredPolicy(data);
        setDraft(draftFromPolicy(data));
      }
    } catch (nextError) {
      setSaveError(nextError);
    } finally {
      setSaving(false);
    }
  };

  const severityOptions = [
    { value: "none", label: t("repository.noVulnerabilities") },
    { value: "low", label: t("repository.upToLow") },
    { value: "medium", label: t("repository.upToMedium") },
    { value: "high", label: t("repository.upToHigh") },
    { value: "critical", label: t("repository.allowCritical") },
  ];
  const policyDirty =
    JSON.stringify(draft) !== JSON.stringify(draftFromPolicy(policy));
  const scanUnavailableLabel = formatDescriptor(repo.format)?.scannerSupport
    ? t("repository.noScannerConfigured")
    : t("repository.unavailableForThisFormat");

  return (
    <PolicyCard
      eyebrow={t("repository.promotionGuardrail")}
      title={t("repository.promotionAdmission")}
      description={t(
        "repository.evaluatesSignaturesSBOMsProvenanceAndVulnerabilityIntelligenceBefore",
      )}
      status={
        <>
          <Tag color={draft.enabled ? "success" : "default"}>
            {draft.enabled
              ? t("repository.enforced")
              : t("repository.notEnforced")}
          </Tag>
          {policyDirty && (
            <Tag color="processing">{t("repository.unsavedChanges")}</Tag>
          )}
          <Switch
            aria-label={t("repository.enforceAdmissionChecks")}
            checked={draft.enabled}
            onChange={(value) => update("enabled", value)}
          />
        </>
      }
    >
      {saveError !== null && <ErrorBanner error={saveError} />}
      {notice && (
        <Notice tone="success" title={notice} onClose={() => setNotice("")} />
      )}

      <PolicySectionHeader
        title={t("repository.securityIntelligence")}
        description={t(
          "repository.controlsWhetherNewlyPublishedArtifactsAutomaticallyProduceIntelligence",
        )}
      />
      <SettingRow
        label={t("repository.scanAfterPublication")}
        hint={t("repository.appliesOnlyToArtifactsPublishedAfterThisPolicy")}
        meta={
          !publicationScanning && (
            <Tag color="warning">{scanUnavailableLabel}</Tag>
          )
        }
      >
        <Switch
          aria-label={t("repository.scanAfterPublication")}
          checked={draft.autoScanOnPublish}
          disabled={!publicationScanning}
          onChange={(value) => update("autoScanOnPublish", value)}
        />
      </SettingRow>

      <PolicySectionHeader
        title={t("repository.evidenceRequirements")}
        description={
          draft.enabled
            ? t("repository.selectedRequirementsRunBeforeEveryPromotion")
            : t("repository.requirementsMayBeConfiguredInAdvanceAndTake")
        }
      />
      <div className="grid gap-3 md:grid-cols-2">
        <PolicySwitch
          label={t("repository.requireASignature")}
          hint={t("repository.rejectArtifactsWithoutSignatureEvidence")}
          checked={draft.requireSignature}
          onChange={(value) => update("requireSignature", value)}
        />
        <PolicySwitch
          label={t("repository.requireAVerifiedSignature")}
          hint={t("repository.atLeastOneSignatureMustBeVerifiedUseful")}
          checked={draft.requireVerifiedSignature}
          onChange={(value) => update("requireVerifiedSignature", value)}
        />
        <PolicySwitch
          label={t("repository.requireAnSBOM")}
          hint={t("repository.rejectArtifactsWithoutASoftwareBillOfMaterials")}
          checked={draft.requireSbom}
          onChange={(value) => update("requireSbom", value)}
        />
        <PolicySwitch
          label={t("repository.requireProvenance")}
          hint={t("repository.requireBuildSourceBuilderAndCommitProvenance")}
          checked={draft.requireProvenance}
          onChange={(value) => update("requireProvenance", value)}
        />
        <PolicySwitch
          label={t("repository.requireVulnerabilityScanning")}
          hint={t("repository.doNotAdmitArtifactsThatHaveNotBeen")}
          checked={draft.requireVulnerabilityScan}
          onChange={(value) => update("requireVulnerabilityScan", value)}
        />
        <PolicySwitch
          label={t("repository.blockOnScanErrors")}
          hint={t("repository.blockWhenAScannerReportsAnErrorDisable")}
          checked={draft.failOnScanError}
          onChange={(value) => update("failOnScanError", value)}
        />
      </div>

      <PolicySectionHeader
        title={t("repository.riskThresholds")}
        description={t(
          "repository.furtherRestrictTheVulnerabilitySeverityAndLicenseSet",
        )}
      />
      <div className="grid gap-5 md:grid-cols-2">
        <Field
          label={t("repository.maximumAllowedVulnerabilitySeverity")}
          hint={t("repository.affectedResultsAboveThisLevelAreRejectedUnknown")}
        >
          <Select
            className="w-full"
            value={draft.maxAllowedSeverity}
            options={severityOptions}
            onChange={(value) => update("maxAllowedSeverity", value)}
          />
        </Field>
        <Field
          label={t("repository.allowedSPDXLicenses")}
          hint={t("repository.leaveEmptyForNoRestrictionWhenSetEvery")}
        >
          <Select
            mode="tags"
            showSearch
            className="w-full font-mono text-xs"
            value={draft.allowedLicenses}
            options={draft.allowedLicenses.map((license) => ({
              value: license,
              label: license,
            }))}
            tokenSeparators={[",", " "]}
            maxTagCount="responsive"
            placeholder={t("repository.eGApache20MIT")}
            onChange={(value) => update("allowedLicenses", value)}
          />
        </Field>
      </div>

      <div className="flex flex-wrap items-center justify-end gap-3 border-t border-line pt-4">
        <span className="text-xs text-fg-disabled">
          {t("repository.currentVersion", { version: policy.version })}
        </span>
        <Button
          type="primary"
          onClick={save}
          loading={saving}
          disabled={!policyDirty}
        >
          {t("repository.savePolicy")}
        </Button>
      </div>
    </PolicyCard>
  );
}
