import { useRef, useState } from "react";
import {
  createRepositoryPromotion,
  createRepositoryReplication,
  evaluateSecurityPolicy,
} from "../../client";
import type { Repository, SecurityPolicyEvaluation } from "../../client";
import { type RepositoryArtifactIdentity } from "./RepositoryArtifactSelect";
import { usePreferences } from "../../lib/preferences";
import { formatDescriptor } from "../../formats";

/**
 * The distribution request's state: target, artifact identity, the target's
 * security-policy answer, and submission with a stable idempotency key per
 * identical request.
 */
export function useDistributionRequest({
  repo,
  repos,
  disabled,
  setActionError,
  setNotice,
  onSubmitted,
}: {
  repo: Repository;
  repos: Repository[];
  disabled: boolean;
  setActionError: (error: unknown) => void;
  setNotice: (notice: string) => void;
  onSubmitted: () => Promise<unknown>;
}) {
  const { t } = usePreferences();
  // APT promotes and replicates into a named distribution suite.
  const suiteTarget = Boolean(formatDescriptor(repo.format)?.distributionSuite);
  const [targetId, setTargetId] = useState("");
  const [aptTargetSuite, setAptTargetSuite] = useState("");
  const evaluationVersion = useRef(0);
  const submission = useRef<{ identity: string; key: string } | null>(null);
  const submitting = useRef(false);
  const [coordinate, setCoordinate] = useState("");
  const [digest, setDigest] = useState("");
  const [selectedArtifact, setSelectedArtifact] =
    useState<RepositoryArtifactIdentity | null>(null);
  const [manualIdentity, setManualIdentity] = useState(false);
  const [busy, setBusy] = useState<"promote" | "replicate" | null>(null);
  const [evaluation, setEvaluation] = useState<SecurityPolicyEvaluation | null>(
    null,
  );
  const [evaluating, setEvaluating] = useState(false);

  const targets = repos.filter(
    (r) =>
      r.id !== repo.id &&
      r.type === "hosted" &&
      r.format === repo.format &&
      r.state === "active",
  );
  const coordinateExample = formatDescriptor(repo.format)?.coordinateExample;

  const invalidateEvaluation = () => {
    evaluationVersion.current++;
    setEvaluation(null);
    setEvaluating(false);
  };

  const submit = async (kind: "promote" | "replicate") => {
    if (
      submitting.current ||
      disabled ||
      (suiteTarget && !aptTargetSuite.trim())
    )
      return;
    submitting.current = true;
    setBusy(kind);
    setActionError(null);
    setNotice("");
    const body = {
      targetRepositoryId: targetId,
      coordinate: coordinate.trim(),
      digest: digest.trim(),
      ...(suiteTarget ? { aptTargetSuite: aptTargetSuite.trim() } : {}),
    };
    const identity = JSON.stringify([kind, body]);
    try {
      if (submission.current?.identity !== identity)
        submission.current = { identity, key: crypto.randomUUID() };
      const headers = { "Idempotency-Key": submission.current.key };
      const response =
        kind === "promote"
          ? await createRepositoryPromotion({
              path: { repositoryId: repo.id },
              body,
              headers,
            })
          : await createRepositoryReplication({
              path: { repositoryId: repo.id },
              body,
              headers,
            });
      if (response.error) throw response.error;
      submission.current = null;
      setNotice(
        kind === "promote"
          ? t("repository.promotionSubmittedTrackItOnTheTargetRepository")
          : t("repository.replicationPlanCreatedTrackItsProgressBelow"),
      );
      setCoordinate("");
      setDigest("");
      setSelectedArtifact(null);
      setManualIdentity(false);
      invalidateEvaluation();
      await onSubmitted();
    } catch (err) {
      setActionError(err);
    } finally {
      submitting.current = false;
      setBusy(null);
    }
  };

  const evaluate = async () => {
    if (!targetId || !coordinate.trim() || !digest.trim()) return;
    const version = ++evaluationVersion.current;
    setEvaluating(true);
    setActionError(null);
    try {
      const response = await evaluateSecurityPolicy({
        path: { repositoryId: targetId },
        body: {
          sourceRepositoryId: repo.id,
          coordinate: coordinate.trim(),
          digest: digest.trim(),
        },
      });
      if (version !== evaluationVersion.current) return;
      if (response.error) throw response.error;
      setEvaluation(response.data ?? null);
    } catch (err) {
      if (version === evaluationVersion.current) {
        setActionError(err);
        setEvaluation(null);
      }
    } finally {
      if (version === evaluationVersion.current) setEvaluating(false);
    }
  };

  const promotionBlocked = evaluation?.allowed === false;
  const artifactQuarantined =
    evaluation?.reasons.includes("artifact_quarantined") ?? false;

  const chooseArtifact = (artifact: RepositoryArtifactIdentity | null) => {
    setSelectedArtifact(artifact);
    setManualIdentity(false);
    invalidateEvaluation();
    setCoordinate(artifact?.coordinate ?? "");
    setDigest(artifact?.digest ?? "");
  };

  const toggleManualIdentity = () => {
    setManualIdentity((current) => {
      const next = !current;
      if (!next && !selectedArtifact) {
        setCoordinate("");
        setDigest("");
      }
      if (!next && selectedArtifact) {
        setCoordinate(selectedArtifact.coordinate);
        setDigest(selectedArtifact.digest);
      }
      invalidateEvaluation();
      return next;
    });
  };

  return {
    suiteTarget,
    targetId,
    setTargetId,
    aptTargetSuite,
    setAptTargetSuite,
    coordinate,
    setCoordinate,
    digest,
    setDigest,
    selectedArtifact,
    setSelectedArtifact,
    manualIdentity,
    busy,
    evaluation,
    evaluating,
    targets,
    coordinateExample,
    invalidateEvaluation,
    submit,
    evaluate,
    promotionBlocked,
    artifactQuarantined,
    chooseArtifact,
    toggleManualIdentity,
  };
}
