import { useState } from "react";
import { ScanOutlined } from "@ant-design/icons";
import { Button, Form, Input, Tag } from "antd";
import { createRepositoryArtifactScan } from "../../client";
import type { LifecycleJob, Repository } from "../../client";
import { ErrorBanner, Notice } from "../../components/ui/Feedback";
import { Card, CardHeader } from "../../components/ui/Layout";
import {
  RepositoryArtifactSelect,
  type RepositoryArtifactIdentity,
} from "./RepositoryArtifactSelect";
import { usePreferences } from "../../lib/preferences";

import {
  type ScanForm,
  scanStatus,
  coordinatePlaceholder,
  manualScanIdempotencyKey,
} from "./scanningModel";

/** Request a scan of one artifact, chosen from the repository or typed. */
export function ScanRequestCard({
  repo,
  artifactScanning,
  canManage,
  jobs,
  onSubmitted,
}: {
  repo: Repository;
  artifactScanning: boolean;
  canManage: boolean;
  /** Known scan jobs, to show each artifact's scan status. */
  jobs: LifecycleJob[] | null;
  /** The queued job, to show at the top of the job list. */
  onSubmitted: (job: LifecycleJob) => void;
}) {
  const { t } = usePreferences();
  const [form] = Form.useForm<ScanForm>();
  const [submitError, setSubmitError] = useState<unknown>(null);
  const [submittedJob, setSubmittedJob] = useState<LifecycleJob | null>(null);
  const [submitting, setSubmitting] = useState(false);
  const [selectedArtifact, setSelectedArtifact] =
    useState<RepositoryArtifactIdentity | null>(null);
  const [manualIdentity, setManualIdentity] = useState(false);

  const submitScan = async (values: ScanForm) => {
    setSubmitting(true);
    setSubmitError(null);
    setSubmittedJob(null);
    try {
      const { data, error } = await createRepositoryArtifactScan({
        path: { repositoryId: repo.id },
        headers: { "Idempotency-Key": manualScanIdempotencyKey(repo.id) },
        body: {
          coordinate: values.coordinate.trim(),
          digest: values.digest.trim(),
        },
      });
      if (error) {
        setSubmitError(error);
        return;
      }
      if (!data) return;
      setSubmittedJob(data);
      onSubmitted(data);
      form.resetFields();
      setSelectedArtifact(null);
      setManualIdentity(false);
    } catch (nextError) {
      setSubmitError(nextError);
    } finally {
      setSubmitting(false);
    }
  };

  const toggleManualIdentity = () => {
    setManualIdentity((current) => {
      const next = !current;
      if (!next) {
        if (selectedArtifact) {
          form.setFieldsValue({
            coordinate: selectedArtifact.coordinate,
            digest: selectedArtifact.digest,
          });
        } else {
          form.resetFields();
        }
      }
      return next;
    });
  };

  return (
    <>
      {submittedJob && (
        <Notice
          tone="success"
          title={t("repository.scanJobQueued")}
          description={
            <span>
              {t("repository.jobID")}
              <code>{submittedJob.id}</code>
            </span>
          }
          onClose={() => setSubmittedJob(null)}
        />
      )}
      {submitError !== null && <ErrorBanner error={submitError} />}

      <Card>
        <CardHeader title={t("repository.selectAndScanAnImmutableArtifact")} />
        <div className="px-5 py-4">
          <Form<ScanForm>
            form={form}
            layout="vertical"
            className="grid gap-4 md:grid-cols-2 md:gap-x-5"
            onFinish={(values) => void submitScan(values)}
            requiredMark={false}
          >
            <Form.Item
              className="md:col-span-2"
              label={t("repository.searchAndSelectAnArtifact")}
              style={{ marginBottom: 0 }}
            >
              <RepositoryArtifactSelect
                repo={repo}
                purpose="scan"
                value={selectedArtifact}
                enabled={artifactScanning && canManage}
                ariaLabel={t("repository.searchAndSelectAnArtifact")}
                statusForOption={(artifact) => scanStatus(artifact, jobs, t)}
                onChange={(artifact) => {
                  setSelectedArtifact(artifact);
                  setManualIdentity(false);
                  if (artifact) {
                    form.setFieldsValue({
                      coordinate: artifact.coordinate,
                      digest: artifact.digest,
                    });
                  } else {
                    form.resetFields();
                  }
                }}
              />
              <div className="mt-1.5 flex flex-wrap items-center justify-between gap-x-4 gap-y-1">
                <span className="text-xs leading-5 text-fg-tertiary">
                  {t(
                    "repository.selectionLocksTheCanonicalCoordinateAndFullDigest",
                  )}
                </span>
                <Button
                  type="link"
                  size="small"
                  className="h-auto px-0 py-0 text-xs"
                  onClick={toggleManualIdentity}
                >
                  {manualIdentity
                    ? t("repository.hideAdvancedManualInput")
                    : t("repository.advancedManualInput")}
                </Button>
              </div>
            </Form.Item>

            {selectedArtifact && !manualIdentity && (
              <div className="flex min-w-0 flex-wrap items-center justify-between gap-3 rounded-md border border-[var(--ag-status-info-border)] bg-[var(--ag-status-info-soft)] px-3 py-2.5 md:col-span-2">
                <div className="min-w-0">
                  <p className="truncate font-mono text-xs text-fg">
                    {selectedArtifact.coordinate}
                  </p>
                  <p className="mt-0.5 break-all font-mono text-xs text-fg-tertiary">
                    {selectedArtifact.digest}
                  </p>
                </div>
                <Tag color={scanStatus(selectedArtifact, jobs, t).color}>
                  {scanStatus(selectedArtifact, jobs, t).label}
                </Tag>
              </div>
            )}

            <Form.Item
              name="coordinate"
              label={t("repository.artifactCoordinate")}
              hidden={!manualIdentity}
              style={{ marginBottom: 0 }}
              rules={[
                {
                  required: true,
                  whitespace: true,
                  message: t("repository.enterAnArtifactCoordinate"),
                },
                {
                  max: 1024,
                  message: t("repository.artifactCoordinateIsTooLong"),
                },
              ]}
            >
              <Input
                disabled={!artifactScanning || !canManage}
                placeholder={coordinatePlaceholder(
                  repo.format,
                  t("repository.canonicalArtifactCoordinate"),
                )}
                autoComplete="off"
              />
            </Form.Item>
            <Form.Item
              name="digest"
              label={t("repository.sHA256Digest")}
              hidden={!manualIdentity}
              style={{ marginBottom: 0 }}
              rules={[
                {
                  required: true,
                  whitespace: true,
                  message: t("repository.enterASHA256Digest"),
                },
                {
                  pattern: /^sha256:[0-9a-f]{64}$/,
                  message: t(
                    "repository.enterSha256FollowedBy64LowercaseHexadecimalCharacters",
                  ),
                },
              ]}
            >
              <Input
                className="font-mono"
                disabled={!artifactScanning || !canManage}
                placeholder="sha256:…"
                autoComplete="off"
                spellCheck={false}
              />
            </Form.Item>
            <div className="grid gap-3 border-t border-line pt-3 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-center md:col-span-2">
              <p className="max-w-2xl text-xs leading-5 text-fg-tertiary">
                {manualIdentity
                  ? t(
                      "repository.advancedModeRequiresAnExactCoordinateAndDigest",
                    )
                  : t("repository.theScanOnlyReadsTheSelectedArtifactAnd")}
              </p>
              <Button
                className="w-full sm:w-auto"
                type="primary"
                htmlType={manualIdentity ? "submit" : "button"}
                aria-label={t("repository.queueScan")}
                icon={<ScanOutlined />}
                loading={submitting}
                disabled={
                  !artifactScanning ||
                  !canManage ||
                  (!manualIdentity && !selectedArtifact)
                }
                onClick={
                  !manualIdentity && selectedArtifact
                    ? () =>
                        void submitScan({
                          coordinate: selectedArtifact.coordinate,
                          digest: selectedArtifact.digest,
                        })
                    : undefined
                }
              >
                {t("repository.queueScan")}
              </Button>
            </div>
          </Form>
        </div>
      </Card>
    </>
  );
}
