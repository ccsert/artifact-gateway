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
  const { text } = usePreferences();
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
          title={text("扫描任务已提交", "Scan job queued")}
          description={
            <span>
              {text("任务 ID：", "Job ID: ")}
              <code>{submittedJob.id}</code>
            </span>
          }
          onClose={() => setSubmittedJob(null)}
        />
      )}
      {submitError !== null && <ErrorBanner error={submitError} />}

      <Card>
        <CardHeader
          title={text(
            "选择并扫描不可变制品",
            "Select and scan an immutable artifact",
          )}
        />
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
              label={text("搜索并选择制品", "Search and select an artifact")}
              style={{ marginBottom: 0 }}
            >
              <RepositoryArtifactSelect
                repo={repo}
                purpose="scan"
                value={selectedArtifact}
                enabled={artifactScanning && canManage}
                ariaLabel={text(
                  "搜索并选择制品",
                  "Search and select an artifact",
                )}
                statusForOption={(artifact) => scanStatus(artifact, jobs, text)}
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
                  {text(
                    "选择后会自动锁定规范坐标与完整摘要；最多显示 50 条，可输入关键词检索历史版本和 Conan 修订。仅在无法检索时使用高级手动输入。",
                    "Selection locks the canonical coordinate and full digest. Up to 50 matches are shown; search historical versions and Conan revisions by keyword. Use advanced manual input only as a recovery path.",
                  )}
                </span>
                <Button
                  type="link"
                  size="small"
                  className="h-auto px-0 py-0 text-xs"
                  onClick={toggleManualIdentity}
                >
                  {manualIdentity
                    ? text("收起高级手动输入", "Hide advanced manual input")
                    : text("高级手动输入", "Advanced manual input")}
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
                <Tag color={scanStatus(selectedArtifact, jobs, text).color}>
                  {scanStatus(selectedArtifact, jobs, text).label}
                </Tag>
              </div>
            )}

            <Form.Item
              name="coordinate"
              label={text("制品坐标", "Artifact coordinate")}
              hidden={!manualIdentity}
              style={{ marginBottom: 0 }}
              rules={[
                {
                  required: true,
                  whitespace: true,
                  message: text(
                    "请输入制品坐标",
                    "Enter an artifact coordinate",
                  ),
                },
                {
                  max: 1024,
                  message: text(
                    "制品坐标过长",
                    "Artifact coordinate is too long",
                  ),
                },
              ]}
            >
              <Input
                disabled={!artifactScanning || !canManage}
                placeholder={coordinatePlaceholder(
                  repo.format,
                  text("制品的规范坐标", "Canonical artifact coordinate"),
                )}
                autoComplete="off"
              />
            </Form.Item>
            <Form.Item
              name="digest"
              label={text("SHA-256 摘要", "SHA-256 digest")}
              hidden={!manualIdentity}
              style={{ marginBottom: 0 }}
              rules={[
                {
                  required: true,
                  whitespace: true,
                  message: text(
                    "请输入 SHA-256 摘要",
                    "Enter a SHA-256 digest",
                  ),
                },
                {
                  pattern: /^sha256:[0-9a-f]{64}$/,
                  message: text(
                    "请输入 sha256: 开头的 64 位小写十六进制摘要",
                    "Enter sha256: followed by 64 lowercase hexadecimal characters",
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
                  ? text(
                      "高级模式要求坐标和摘要与仓库中的不可变制品完全一致。",
                      "Advanced mode requires an exact coordinate and digest match.",
                    )
                  : text(
                      "扫描只读取已选制品，不会拉取或修改上游内容。",
                      "The scan only reads the selected artifact and never fetches or modifies upstream content.",
                    )}
              </p>
              <Button
                className="w-full sm:w-auto"
                type="primary"
                htmlType={manualIdentity ? "submit" : "button"}
                aria-label={text("提交扫描", "Queue scan")}
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
                {text("提交扫描", "Queue scan")}
              </Button>
            </div>
          </Form>
        </div>
      </Card>
    </>
  );
}
