import { useEffect, useRef, useState } from "react";
import {
  createAptPublicationSession,
  publishAptRepositorySnapshot,
  uploadAptPublicationPackage,
} from "../../client";
import type {
  AptPackageRevision,
  AptPublicationSession,
  AptRepositorySnapshot,
  CreateAptPublicationSession,
} from "../../client";
import { sha256Receipt } from "../../lib/download";
import { usePreferences } from "../../lib/preferences";

type Attempt = {
  file: File;
  body: CreateAptPublicationSession;
  createKey: string;
  publishKey: string;
  session?: AptPublicationSession;
  revision?: AptPackageRevision;
};

/** One in-memory draft; retries keep both request bodies and keys unchanged. */
export function useAptFirstPublication({
  repositoryId,
  suite,
  disabled,
  onPublished,
}: {
  repositoryId: string;
  suite: string;
  disabled: boolean;
  onPublished: (snapshot: AptRepositorySnapshot) => Promise<void>;
}) {
  const { t } = usePreferences();
  const [file, setFile] = useState<File | null>(null);
  const [digest, setDigest] = useState<string | null>(null);
  const [hashing, setHashing] = useState(false);
  const [component, setComponent] = useState("main");
  const [attempt, setAttempt] = useState<Attempt | null>(null);
  const attemptRef = useRef<Attempt | null>(null);
  const [reviewed, setReviewed] = useState(false);
  const [busy, setBusy] = useState<"stage" | "publish" | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [publishAttempted, setPublishAttempted] = useState(false);
  const [published, setPublished] = useState<AptRepositorySnapshot | null>(
    null,
  );
  const active = useRef(false);
  const generation = useRef(0);
  const inFlight = useRef(false);
  const controller = useRef<AbortController | null>(null);
  useEffect(() => {
    generation.current++;
    active.current = true;
    return () => {
      active.current = false;
      controller.current?.abort();
    };
  }, []);

  const current = (version: number) =>
    active.current && generation.current === version;
  const save = (next: Attempt) => {
    attemptRef.current = next;
    setAttempt(next);
  };
  const componentValid = /^[a-z0-9][a-z0-9+.-]{0,127}$/.test(component.trim());
  const locked = disabled || busy !== null || attempt !== null || !!published;

  const selectFile = async (selected: File) => {
    if (disabled || inFlight.current || attemptRef.current || published) return;
    const version = ++generation.current;
    setFile(selected);
    setDigest(null);
    setError(null);
    setHashing(false);
    if (
      !/^[^/\\]+\.deb$/.test(selected.name) ||
      selected.name.length < 5 ||
      selected.name.length > 255 ||
      selected.size < 1 ||
      selected.size > 1073741824
    ) {
      setError(new Error(t("repository.aptFirstInvalidFile")));
      return;
    }
    setHashing(true);
    const receipt = await sha256Receipt(selected);
    if (!current(version)) return;
    setHashing(false);
    if (!receipt) {
      setError(new Error(t("repository.aptFirstHashUnavailable")));
      return;
    }
    setDigest(receipt);
  };

  const run = async (
    action: "stage" | "publish",
    operation: (version: number, signal: AbortSignal) => Promise<void>,
  ) => {
    if (disabled || inFlight.current || published) return;
    inFlight.current = true;
    const version = generation.current;
    const request = new AbortController();
    controller.current = request;
    setBusy(action);
    setError(null);
    try {
      await operation(version, request.signal);
    } catch (err) {
      if (current(version)) setError(err);
    } finally {
      inFlight.current = false;
      if (current(version)) setBusy(null);
    }
  };

  const stage = () =>
    run("stage", async (version, signal) => {
      if (!file || !digest || !componentValid) return;
      let draft = attemptRef.current;
      if (!draft) {
        draft = {
          file,
          body: {
            suite,
            component: component.trim(),
            objectName: file.name,
            declaredDigest: digest,
            declaredSize: file.size,
          },
          createKey: crypto.randomUUID(),
          publishKey: crypto.randomUUID(),
        };
        save(draft);
      }
      if (!draft.session) {
        const response = await createAptPublicationSession({
          path: { repositoryId },
          body: draft.body,
          headers: { "Idempotency-Key": draft.createKey },
          signal,
        });
        if (!current(version)) return;
        if (response.error) throw response.error;
        const session = response.data;
        if (
          !session?.id ||
          session.repositoryId !== repositoryId ||
          session.suite !== suite ||
          session.component !== draft.body.component ||
          session.objectName !== draft.body.objectName ||
          session.declaredDigest !== draft.body.declaredDigest ||
          session.declaredSize !== draft.body.declaredSize ||
          session.state === "aborted"
        )
          throw new Error(t("repository.aptFirstSessionMismatch"));
        draft = { ...draft, session };
        save(draft);
      }
      const session = draft.session;
      if (!session) throw new Error(t("repository.aptFirstSessionMismatch"));
      const response = await uploadAptPublicationPackage({
        path: { repositoryId, sessionId: session.id },
        body: draft.file,
        signal,
      });
      if (!current(version)) return;
      if (response.error) throw response.error;
      const revision = response.data;
      if (
        !revision?.id ||
        revision.repositoryId !== repositoryId ||
        revision.digest !== draft.body.declaredDigest ||
        revision.size !== draft.body.declaredSize ||
        !revision.package ||
        !revision.version ||
        !revision.architecture ||
        !revision.canonicalIdentity ||
        (draft.session?.packageRevisionId &&
          draft.session.packageRevisionId !== revision.id)
      )
        throw new Error(t("repository.aptFirstRevisionMismatch"));
      save({ ...draft, revision });
      setReviewed(false);
    });

  const publish = () =>
    run("publish", async (version, signal) => {
      const draft = attemptRef.current;
      if (!reviewed || !draft?.revision || !draft.session) return;
      setPublishAttempted(true);
      const response = await publishAptRepositorySnapshot({
        path: { repositoryId },
        body: {
          suite,
          sequence: 1,
          publicationSessionIds: [draft.session.id],
        },
        headers: { "Idempotency-Key": draft.publishKey },
        signal,
      });
      if (!current(version)) return;
      if (response.error) throw response.error;
      const snapshot = response.data;
      if (
        !snapshot?.id ||
        snapshot.repositoryId !== repositoryId ||
        snapshot.suite !== suite ||
        snapshot.sequence !== 1 ||
        snapshot.state !== "visible"
      )
        throw new Error(t("repository.aptFirstSnapshotMismatch"));
      setPublished(snapshot);
      await onPublished(snapshot);
    });

  const reset = () => {
    if (disabled || inFlight.current || published) return;
    generation.current++;
    attemptRef.current = null;
    setAttempt(null);
    setFile(null);
    setDigest(null);
    setHashing(false);
    setReviewed(false);
    setPublishAttempted(false);
    setError(null);
  };

  return {
    file,
    digest,
    hashing,
    component,
    setComponent,
    componentValid,
    attempt,
    reviewed,
    setReviewed,
    busy,
    error,
    locked,
    published,
    publishAttempted,
    selectFile,
    stage,
    publish,
    reset,
  };
}
