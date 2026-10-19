import { useRef, useState } from "react";
import { Button, Input, Select, Space } from "antd";
import {
  createMigrationSource,
  updateMigrationSource,
  type MigrationSource,
  type MigrationSourceInputWritable,
} from "../../client";
import { ErrorBanner } from "../../components/ui/Feedback";
import { Field } from "../../components/ui/Layout";
import { Modal } from "../../components/ui/Modal";
import { usePreferences } from "../../lib/preferences";
import {
  migrationError,
  migrationRequest,
  validSourceUrl,
} from "./migrationModel";

type CredentialAction = "keep" | "replace" | "clear";

/** Local form only: secrets never enter a query key, cache or mutation record. */
export function MigrationSourceEditor({
  source,
  onClose,
  onSaved,
  onReload,
}: {
  source: MigrationSource | null;
  onClose: () => void;
  onSaved: (source: MigrationSource) => void;
  onReload: () => Promise<void>;
}) {
  const { t } = usePreferences();
  const [name, setName] = useState(source?.name ?? "");
  const [baseUrl, setBaseUrl] = useState(source?.baseUrl ?? "");
  const [credentialAction, setCredentialAction] = useState<CredentialAction>(
    source ? "keep" : "clear",
  );
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const submitting = useRef(false);
  const originChanged = Boolean(
    source?.credentialsConfigured &&
    source.baseUrl !== baseUrl.trim() &&
    credentialAction === "keep",
  );
  const valid =
    name.trim().length > 0 &&
    name.trim().length <= 128 &&
    validSourceUrl(baseUrl.trim()) &&
    !originChanged &&
    (credentialAction !== "replace" ||
      (username.length > 0 && password.length > 0));
  const clearInputs = () => {
    setUsername("");
    setPassword("");
  };
  const save = async () => {
    if (submitting.current || !valid) return;
    submitting.current = true;
    setBusy(true);
    setError(null);
    const body: MigrationSourceInputWritable = {
      name: name.trim(),
      baseUrl: baseUrl.trim(),
      ...(credentialAction === "replace"
        ? { credentials: { username, password } }
        : credentialAction === "clear" && source
          ? { clearCredentials: true }
          : {}),
    };
    clearInputs();
    try {
      const saved = await migrationRequest(
        source
          ? updateMigrationSource({
              path: { sourceId: source.id },
              headers: { "If-Match": source.version },
              body,
            })
          : createMigrationSource({ body }),
      );
      onSaved(saved);
    } catch (failure) {
      setError(migrationError(failure));
    } finally {
      submitting.current = false;
      setBusy(false);
    }
  };
  const reload = async () => {
    if (submitting.current) return;
    submitting.current = true;
    setBusy(true);
    clearInputs();
    try {
      await onReload();
    } catch (failure) {
      setError(migrationError(failure));
    } finally {
      submitting.current = false;
      setBusy(false);
    }
  };
  const close = () => {
    clearInputs();
    onClose();
  };
  return (
    <Modal
      open
      title={t(source ? "migration.editSource" : "migration.newSource")}
      onClose={close}
      busy={busy}
      footer={
        <Space wrap>
          <Button onClick={close} disabled={busy}>
            {t("migration.cancel")}
          </Button>
          <Button
            type="primary"
            htmlType="submit"
            form="migration-source-editor"
            loading={busy}
            disabled={!valid}
          >
            {t(source ? "migration.save" : "migration.create")}
          </Button>
        </Space>
      }
    >
      <form
        id="migration-source-editor"
        className="ag-migration-form"
        autoComplete="off"
        onSubmit={(event) => {
          event.preventDefault();
          void save();
        }}
      >
        {error !== null && <ErrorBanner error={error} />}
        {source &&
          error instanceof Error &&
          "status" in error &&
          error.status === 412 && (
            <Button onClick={() => void reload()} disabled={busy}>
              {t("migration.reloadLatest")}
            </Button>
          )}
        <Field label={t("migration.sourceName")}>
          <Input
            value={name}
            maxLength={128}
            disabled={busy}
            onChange={(event) => setName(event.target.value)}
          />
        </Field>
        <Field label={t("migration.baseUrl")} hint={t("migration.baseUrlHint")}>
          <Input
            value={baseUrl}
            maxLength={2048}
            disabled={busy}
            onChange={(event) => setBaseUrl(event.target.value)}
          />
        </Field>
        <Field
          label={t("migration.credentials")}
          hint={t("migration.credentialHint")}
        >
          <Select
            virtual={false}
            aria-label={t("migration.credentials")}
            value={credentialAction}
            disabled={busy}
            className="w-full"
            onChange={(value: CredentialAction) => {
              setCredentialAction(value);
              clearInputs();
            }}
            options={[
              ...(source
                ? [{ value: "keep", label: t("migration.keepCredentials") }]
                : []),
              { value: "replace", label: t("migration.replaceCredentials") },
              {
                value: "clear",
                label: t(
                  source
                    ? "migration.clearCredentials"
                    : "migration.noCredentials",
                ),
              },
            ]}
          />
        </Field>
        {originChanged && (
          <p role="status" className="text-sm text-[var(--ag-status-warning)]">
            {t("migration.replaceRequired")}
          </p>
        )}
        {credentialAction === "replace" && (
          <>
            <Field label={t("migration.username")}>
              <Input
                autoComplete="off"
                value={username}
                maxLength={256}
                disabled={busy}
                onChange={(event) => setUsername(event.target.value)}
              />
            </Field>
            <Field label={t("migration.password")}>
              <Input.Password
                autoComplete="new-password"
                visibilityToggle={false}
                value={password}
                maxLength={4096}
                disabled={busy}
                onChange={(event) => setPassword(event.target.value)}
              />
            </Field>
          </>
        )}
      </form>
    </Modal>
  );
}
