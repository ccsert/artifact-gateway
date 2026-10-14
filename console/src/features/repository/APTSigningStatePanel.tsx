import type { Translate } from "../../lib/preferences";
import { Alert, Tag } from "antd";
import type { AptRepositorySigningState } from "../../client";
import { Badge } from "../../components/ui/Badge";
import { formatDate } from "../../lib/format";
import { usePreferences } from "../../lib/preferences";
import {
  PolicyCard,
  PolicySectionHeader,
  ScopeFact,
} from "./RepositorySecurityPrimitives";

export function APTSigningStatePanel({
  state,
}: {
  state: AptRepositorySigningState;
}) {
  const { locale, t } = usePreferences();
  const presentation = aptSigningPresentation(state.readiness, t);
  const current = state.currentSnapshot;
  return (
    <div className="space-y-4">
      <div className="border-b border-line pb-4">
        <div className="text-xs font-semibold text-[var(--ag-content-tertiary)]">
          {t("repository.aPTPublicationTrust")}
        </div>
        <h2 className="mt-1.5 text-lg font-semibold tracking-tight text-fg-strong">
          {t("repository.signingTrustAndCurrentSnapshot")}
        </h2>
        <p className="mt-1.5 max-w-3xl text-sm leading-6 text-fg-tertiary">
          {t("repository.verifyTheSigningKeysCurrentlyTrustedByGateway")}
        </p>
      </div>
      <PolicyCard
        eyebrow={t("repository.publicationRootOfTrust")}
        title={t("repository.aPTReleaseSigning")}
        description={t(
          "repository.remoteModeRequiresPinnedPublicKeyFingerprintsTwo",
        )}
        status={<Tag color={presentation.color}>{presentation.label}</Tag>}
      >
        {presentation.warning && (
          <Alert
            type={presentation.warning.type}
            showIcon
            title={presentation.warning.title}
          />
        )}
        <div className="grid gap-px overflow-hidden rounded-lg border border-line bg-[var(--ag-border-subtle)] sm:grid-cols-2 lg:grid-cols-4">
          <ScopeFact
            label={t("repository.signerMode")}
            value={aptSignerModeLabel(state.signerMode, t)}
          />
          <ScopeFact
            label={t("repository.trustedKeyCount")}
            value={String(state.trustedFingerprints.length)}
          />
          <ScopeFact
            label={t("repository.currentKeyRole")}
            value={aptKeyRoleLabel(state.currentKeyRole, t)}
          />
          <ScopeFact
            label={t("repository.latestPublication")}
            value={current ? `${current.suite} · #${current.sequence}` : "—"}
          />
        </div>

        <PolicySectionHeader
          title={t("repository.trustedFingerprintWindow")}
          description={t(
            "repository.theFirstFingerprintIsPrimaryAndTheOptional",
          )}
        />
        {state.trustedFingerprints.length === 0 ? (
          <div className="rounded-lg border border-dashed border-line px-4 py-4 text-sm text-fg-tertiary">
            {t("repository.noRemoteTrustedFingerprintsConfigured")}
          </div>
        ) : (
          <div className="space-y-2">
            {state.trustedFingerprints.map((fingerprint, index) => {
              const isCurrent = current?.keyFingerprint === fingerprint;
              return (
                <div
                  key={fingerprint}
                  className={`flex flex-col gap-2 rounded-lg border px-4 py-3 sm:flex-row sm:items-center sm:justify-between ${
                    isCurrent
                      ? "border-[var(--ag-border-default)] bg-[var(--ag-navigation-selected-start)]"
                      : "border-line bg-[var(--ag-surface-table-header)]"
                  }`}
                >
                  <div className="flex items-center gap-2">
                    <Badge
                      tone={index === 0 ? "visualization-5" : "visualization-6"}
                    >
                      {index === 0
                        ? t("repository.primary")
                        : t("repository.next")}
                    </Badge>
                    {isCurrent && (
                      <Tag color="success">{t("repository.inUse")}</Tag>
                    )}
                  </div>
                  <code className="break-all font-mono text-xs text-fg-secondary">
                    {fingerprint}
                  </code>
                </div>
              );
            })}
          </div>
        )}

        <PolicySectionHeader
          title={t("repository.latestImmutableEvidence")}
          description={t(
            "repository.theseValuesComeFromTheCommittedVisibleSnapshot",
          )}
        />
        {current ? (
          <div className="grid gap-3 md:grid-cols-2">
            <SigningEvidence
              label={t("repository.suiteSequence")}
              value={`${current.suite} / ${current.sequence}`}
            />
            <SigningEvidence
              label={t("repository.publishedAt")}
              value={formatDate(current.publishedAt, locale)}
            />
            <SigningEvidence
              label={t("repository.signerIdentity")}
              value={current.signerIdentity}
              mono
            />
            <SigningEvidence
              label={t("repository.signatureAlgorithm")}
              value={current.signatureAlgorithm}
              mono
            />
            <SigningEvidence
              label={t("repository.keyFingerprint")}
              value={current.keyFingerprint}
              mono
            />
            <SigningEvidence
              label={t("repository.releaseDigest")}
              value={current.releaseDigest}
              mono
            />
          </div>
        ) : (
          <div className="rounded-lg border border-dashed border-line px-4 py-4 text-sm text-fg-tertiary">
            {t("repository.thisRepositoryHasNoVisibleSnapshotYet")}
          </div>
        )}
      </PolicyCard>
    </div>
  );
}

function SigningEvidence({
  label,
  value,
  mono = false,
}: {
  label: string;
  value: string;
  mono?: boolean;
}) {
  return (
    <div className="min-w-0 rounded-lg border border-line bg-[var(--ag-surface-table-header)] px-4 py-3">
      <div className="text-xs font-medium text-fg-disabled">{label}</div>
      <div
        className={`mt-1 break-all text-xs text-fg-secondary ${mono ? "font-mono" : "font-medium"}`}
      >
        {value}
      </div>
    </div>
  );
}

function aptSigningPresentation(
  readiness: AptRepositorySigningState["readiness"],
  t: Translate,
): {
  color: string;
  label: string;
  warning: { type: "info" | "warning" | "error"; title: string } | null;
} {
  switch (readiness) {
    case "ready":
      return {
        color: "success",
        label: t("repository.productionTrustPinned"),
        warning: null,
      };
    case "rotation_overlap":
      return {
        color: "processing",
        label: t("repository.keyRotationOverlap"),
        warning: {
          type: "info",
          title: t("repository.bothOldAndNewKeysAreAcceptedConfirm"),
        },
      };
    case "fixture":
      return {
        color: "warning",
        label: t("repository.referenceSignerOnly"),
        warning: {
          type: "warning",
          title: t("repository.theLocalReferenceSignerIsSuitableOnlyFor"),
        },
      };
    case "policy_mismatch":
      return {
        color: "error",
        label: t("repository.trustPolicyMismatch"),
        warning: {
          type: "error",
          title: t("repository.theLatestVisibleSnapshotUsesAKeyOutside"),
        },
      };
    case "unconfigured":
      return {
        color: "default",
        label: t("repository.notConfigured2"),
        warning: {
          type: "warning",
          title: t("repository.noUsableSignerIsConfiguredSoNewAPT"),
        },
      };
    default:
      return {
        color: "default",
        label: t("repository.unknownState"),
        warning: null,
      };
  }
}

function aptSignerModeLabel(
  mode: AptRepositorySigningState["signerMode"],
  t: Translate,
) {
  if (mode === "remote") return t("repository.remotePinnedTrust");
  if (mode === "reference") return t("repository.localReferenceSigner");
  return t("repository.disabled3");
}

function aptKeyRoleLabel(
  role: AptRepositorySigningState["currentKeyRole"],
  t: Translate,
) {
  if (role === "active") return t("repository.activeTrustKey");
  if (role === "next") return t("repository.next");
  if (role === "fixture") return t("repository.fixture");
  if (role === "outside_policy") return t("repository.outsidePolicy");
  return "—";
}
