import { useEffect, useId, useRef } from "react";
import { Button, Checkbox, Input, Popconfirm, Upload } from "antd";
import { UploadOutlined } from "@ant-design/icons";
import type { AptRepositorySnapshot, Repository } from "../../client";
import { ErrorBanner, Notice } from "../../components/ui/Feedback";
import { Field } from "../../components/ui/Layout";
import { usePreferences } from "../../lib/preferences";
import { useAptFirstPublication } from "./useAptFirstPublication";

type Props = {
  repo: Repository;
  suite: string;
  disabled: boolean;
  onPublished: (snapshot: AptRepositorySnapshot) => Promise<void>;
};

/** Scope changes discard the old draft before another repository can use it. */
export function APTFirstPublication(props: Props) {
  return (
    <APTFirstPublicationForm
      key={`${props.repo.id}:${props.suite}`}
      {...props}
    />
  );
}

function APTFirstPublicationForm({
  repo,
  suite,
  disabled,
  onPublished,
}: Props) {
  const { t } = usePreferences();
  const headingId = useId();
  const form = useAptFirstPublication({
    repositoryId: repo.id,
    suite,
    disabled,
    onPublished,
  });
  const revision = form.attempt?.revision;
  const revisionId = revision?.id;
  const reviewHeading = useRef<HTMLHeadingElement>(null);
  useEffect(() => {
    if (revisionId) reviewHeading.current?.focus();
  }, [revisionId]);
  const cannotAct = disabled || form.busy !== null || !!form.published;
  const stageLabel = t(
    form.attempt ? "repository.aptFirstRetryStage" : "repository.aptFirstStage",
  );
  const publishLabel = t(
    form.publishAttempted
      ? "repository.aptFirstRetryPublish"
      : "repository.aptFirstPublish",
  );
  return (
    <section
      className="ag-page-stack ag-apt-first-publication"
      aria-labelledby={headingId}
    >
      <div>
        <h4 id={headingId}>{t("repository.aptFirstTitle")}</h4>
        <p className="ag-apt-secondary">{t("repository.aptFirstScope")}</p>
      </div>
      {form.error !== null && (
        <>
          <ErrorBanner error={form.error} />
          {form.attempt && (
            <p className="ag-apt-secondary">
              {t("repository.aptFirstRetryHint")}
            </p>
          )}
        </>
      )}
      <div className="ag-apt-first-inputs">
        <Field label={t("repository.aptFirstFile")} group>
          <div className="ag-apt-file-choice">
            <Upload
              accept=".deb"
              maxCount={1}
              showUploadList={false}
              disabled={form.locked}
              beforeUpload={(file) => {
                void form.selectFile(file);
                return Upload.LIST_IGNORE;
              }}
            >
              <Button
                icon={<UploadOutlined aria-hidden />}
                disabled={form.locked}
              >
                {t("repository.chooseFile")}
              </Button>
            </Upload>
            <span className="ag-apt-wrap">
              {form.file?.name ?? t("repository.noFileSelected")}
            </span>
          </div>
        </Field>
        <Field
          label={t("repository.component")}
          hint={t("repository.aptFirstComponentHint")}
          error={
            form.componentValid
              ? undefined
              : t("repository.aptFirstInvalidComponent")
          }
        >
          <Input
            aria-label={t("repository.component")}
            value={form.component}
            disabled={form.locked}
            onChange={(event) => form.setComponent(event.target.value)}
          />
        </Field>
      </div>
      {form.file && (
        <dl className="ag-apt-evidence">
          <div>
            <dt>{t("repository.size")}</dt>
            <dd>
              {t("repository.aptFirstExactSize", { size: form.file.size })}
            </dd>
          </div>
          <div>
            <dt>SHA-256</dt>
            <dd>
              {form.hashing ? (
                <span role="status">{t("repository.aptFirstHashing")}</span>
              ) : (
                <code>{form.digest ?? "—"}</code>
              )}
            </dd>
          </div>
        </dl>
      )}
      {form.attempt?.session && (
        <p className="ag-apt-secondary ag-apt-wrap">
          {t("repository.aptFirstSession")}{" "}
          <code>{form.attempt.session.id}</code>
        </p>
      )}
      {revision && (
        <div className="ag-page-stack ag-apt-first-review">
          <h4 ref={reviewHeading} tabIndex={-1}>
            {t("repository.aptFirstReviewTitle")}
          </h4>
          <dl className="ag-apt-evidence">
            <div>
              <dt>{t("repository.aptFirstPackage")}</dt>
              <dd>{revision.package}</dd>
            </div>
            <div>
              <dt>{t("repository.version")}</dt>
              <dd>{revision.version}</dd>
            </div>
            <div>
              <dt>{t("repository.aptFirstArchitecture")}</dt>
              <dd>{revision.architecture}</dd>
            </div>
            <div>
              <dt>{t("repository.aptFirstIdentity")}</dt>
              <dd>
                <code>{revision.canonicalIdentity}</code>
              </dd>
            </div>
          </dl>
          <Checkbox
            checked={form.reviewed}
            disabled={cannotAct}
            onChange={(event) => form.setReviewed(event.target.checked)}
          >
            {t("repository.aptFirstReviewed")}
          </Checkbox>
        </div>
      )}
      {form.published && (
        <Notice
          tone="success"
          closable={false}
          title={t("repository.publishedSnapshot", { suite, sequence: 1 })}
          description={t("repository.aptFirstPublishedRefresh")}
        />
      )}
      <div className="ag-apt-first-actions">
        {form.file && !form.published && (
          <Popconfirm
            title={t("repository.aptFirstDiscardTitle")}
            description={t("repository.aptFirstDiscardHint")}
            onConfirm={form.reset}
            disabled={cannotAct}
            okText={t("repository.aptFirstDiscard")}
            cancelText={t("repository.cancel")}
          >
            <Button disabled={cannotAct}>
              {t("repository.aptFirstDiscard")}
            </Button>
          </Popconfirm>
        )}
        {!revision && !form.published && (
          <Button
            type="primary"
            aria-label={stageLabel}
            aria-busy={form.busy === "stage"}
            loading={form.busy === "stage"}
            disabled={cannotAct || !form.digest || !form.componentValid}
            onClick={() => void form.stage()}
          >
            {stageLabel}
          </Button>
        )}
        {revision && !form.published && (
          <Button
            type="primary"
            aria-label={publishLabel}
            aria-busy={form.busy === "publish"}
            loading={form.busy === "publish"}
            disabled={cannotAct || !form.reviewed}
            onClick={() => void form.publish()}
          >
            {publishLabel}
          </Button>
        )}
      </div>
    </section>
  );
}
