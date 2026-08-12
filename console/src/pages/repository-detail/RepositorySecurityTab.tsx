import { useCallback, useEffect, useState } from "react";
import { Alert, Button, Select, Space, Switch } from "antd";
import {
  getQuarantineReadPolicy,
  getSecurityPolicy,
  replaceQuarantineReadPolicy,
  replaceSecurityPolicy,
} from "../../client";
import type {
  QuarantineReadPolicy,
  Repository,
  SecurityPolicy,
} from "../../client";
import { ErrorBanner, Loading, isNotFound } from "../../components/Feedback";
import { Field } from "../../components/Layout";
import { usePreferences } from "../../lib/preferences";
import { RepositoryFeatureUnavailable } from "./RepositoryFeatureUnavailable";

type SecurityPolicyDraft = Required<
  Pick<
    SecurityPolicy,
    | "enabled"
    | "autoScanOnPublish"
    | "requireSignature"
    | "requireVerifiedSignature"
    | "requireSbom"
    | "requireProvenance"
    | "requireVulnerabilityScan"
    | "maxAllowedSeverity"
    | "failOnScanError"
    | "allowedLicenses"
  >
>;

const DEFAULT_DRAFT: SecurityPolicyDraft = {
  enabled: false,
  autoScanOnPublish: false,
  requireSignature: false,
  requireVerifiedSignature: false,
  requireSbom: false,
  requireProvenance: false,
  requireVulnerabilityScan: false,
  maxAllowedSeverity: "critical",
  failOnScanError: true,
  allowedLicenses: [],
};

function draftFromPolicy(policy: SecurityPolicy): SecurityPolicyDraft {
  return {
    enabled: policy.enabled ?? false,
    autoScanOnPublish: policy.autoScanOnPublish ?? false,
    requireSignature: policy.requireSignature ?? false,
    requireVerifiedSignature: policy.requireVerifiedSignature ?? false,
    requireSbom: policy.requireSbom ?? false,
    requireProvenance: policy.requireProvenance ?? false,
    requireVulnerabilityScan: policy.requireVulnerabilityScan ?? false,
    maxAllowedSeverity: policy.maxAllowedSeverity ?? "critical",
    failOnScanError: policy.failOnScanError ?? true,
    allowedLicenses: policy.allowedLicenses ?? [],
  };
}

export function RepositorySecurityTab({
  repo,
  publicationScanning,
}: {
  repo: Repository;
  publicationScanning: boolean;
}) {
  const { text } = usePreferences();
  const [policy, setPolicy] = useState<SecurityPolicy | null>(null);
  const [readPolicy, setReadPolicy] = useState<QuarantineReadPolicy | null>(
    null,
  );
  const [readEnabled, setReadEnabled] = useState(false);
  const [draft, setDraft] = useState<SecurityPolicyDraft>(DEFAULT_DRAFT);
  const [error, setError] = useState<unknown>(null);
  const [saveError, setSaveError] = useState<unknown>(null);
  const [saving, setSaving] = useState(false);
  const [notice, setNotice] = useState("");
  const [readError, setReadError] = useState<unknown>(null);
  const [readSaving, setReadSaving] = useState(false);
  const [readNotice, setReadNotice] = useState("");

  const load = useCallback(async () => {
    setError(null);
    const { data, error: err } = await getSecurityPolicy({
      path: { repositoryId: repo.id },
    });
    if (err) {
      setError(err);
      return;
    }
    if (data) {
      setPolicy(data);
      setDraft(draftFromPolicy(data));
    }
  }, [repo.id]);

  const loadReadPolicy = useCallback(async () => {
    setReadError(null);
    const { data, error: err } = await getQuarantineReadPolicy({
      path: { repositoryId: repo.id },
    });
    if (err) {
      setReadError(err);
      return;
    }
    if (data) {
      setReadPolicy(data);
      setReadEnabled(data.enabled);
    }
  }, [repo.id]);

  useEffect(() => {
    void load();
    void loadReadPolicy();
  }, [load, loadReadPolicy]);

  const update = <K extends keyof SecurityPolicyDraft>(
    key: K,
    value: SecurityPolicyDraft[K],
  ) => {
    setDraft((current) => ({ ...current, [key]: value }));
  };

  const save = async () => {
    if (!policy) return;
    setSaving(true);
    setSaveError(null);
    setNotice("");
    const { data, error: err } = await replaceSecurityPolicy({
      path: { repositoryId: repo.id },
      headers: { "If-Match": policy.version },
      body: { version: policy.version, ...draft },
    });
    setSaving(false);
    if (err) {
      setSaveError(err);
      return;
    }
    setNotice(text("仓库安全策略已保存", "Repository security policy saved"));
    if (data) {
      setPolicy(data);
      setDraft(draftFromPolicy(data));
    } else {
      void load();
    }
  };

  const saveReadPolicy = async () => {
    if (!readPolicy) return;
    setReadSaving(true);
    setReadError(null);
    setReadNotice("");
    const { data, error: err } = await replaceQuarantineReadPolicy({
      path: { repositoryId: repo.id },
      headers: { "If-Match": readPolicy.version },
      body: { version: readPolicy.version, enabled: readEnabled },
    });
    setReadSaving(false);
    if (err) {
      setReadError(err);
      return;
    }
    setReadNotice(text("隔离读取策略已保存", "Quarantine read policy saved"));
    if (data) {
      setReadPolicy(data);
      setReadEnabled(data.enabled);
    } else {
      void loadReadPolicy();
    }
  };

  if (error !== null) {
    return isNotFound(error) ? (
      <RepositoryFeatureUnavailable
        feature={text("安全准入策略", "Security admission policy")}
      />
    ) : (
      <ErrorBanner error={error} onRetry={load} />
    );
  }
  if (!policy || (!readPolicy && readError === null)) return <Loading />;

  const severityOptions = [
    { value: "none", label: text("不允许漏洞", "No vulnerabilities") },
    { value: "low", label: text("最多低危", "Up to low") },
    { value: "medium", label: text("最多中危", "Up to medium") },
    { value: "high", label: text("最多高危", "Up to high") },
    { value: "critical", label: text("允许严重等级", "Allow critical") },
  ];

  return (
    <div className="space-y-5">
      <Alert
        type="info"
        showIcon
        title={text("制品安全策略", "Artifact security policy")}
        description={text(
          "自动扫描在新制品发布后生成安全情报；准入规则在制品晋升到当前仓库前使用这些情报。两者都不影响普通读取。",
          "Automatic scans produce security intelligence after new publications. Admission rules consume that intelligence before promotion into this repository. Neither affects ordinary reads.",
        )}
      />
      {saveError !== null && <ErrorBanner error={saveError} />}
      {notice && <Alert type="success" showIcon title={notice} />}

      <div className="max-w-5xl space-y-4 rounded-lg border border-amber-900/60 bg-amber-950/20 p-5">
        <Alert
          type={readEnabled ? "warning" : "info"}
          showIcon
          title={text("隔离制品读取策略", "Quarantined artifact reads")}
          description={text(
            "这是独立于晋升准入的强制策略。启用后，协议 GET/HEAD 会拒绝隔离制品；npm 与 PyPI 按整版本阻断，Conan 按 recipe revision 闭包阻断，Group 不会回退到低优先级成员。解除隔离后读取立即恢复。",
            "This policy is independent from promotion admission. When enabled, protocol GET/HEAD requests reject quarantined artifacts. npm and PyPI block whole versions, Conan blocks the recipe revision closure, and Groups do not fall through to lower-priority members. Reads resume immediately after release.",
          )}
        />
        {readError !== null && (
          <ErrorBanner error={readError} onRetry={loadReadPolicy} />
        )}
        {readPolicy && (
          <>
            {readNotice && <Alert type="success" showIcon title={readNotice} />}
            <div className="flex items-center justify-between gap-8">
              <div>
                <div className="text-sm font-medium text-zinc-200">
                  {text("阻断隔离制品读取", "Block quarantined artifact reads")}
                </div>
                <div className="mt-1 text-xs leading-5 text-zinc-500">
                  {text(
                    "默认关闭以保持升级兼容；建议在确认现有隔离记录后为受保护仓库开启。",
                    "Disabled by default for upgrade compatibility. Enable it for protected repositories after reviewing existing quarantine records.",
                  )}
                </div>
              </div>
              <Switch
                aria-label={text(
                  "阻断隔离制品读取",
                  "Block quarantined artifact reads",
                )}
                checked={readEnabled}
                checkedChildren={text("已启用", "On")}
                unCheckedChildren={text("已停用", "Off")}
                onChange={setReadEnabled}
              />
            </div>
            <Space>
              <Button
                type="primary"
                danger={readEnabled}
                loading={readSaving}
                onClick={saveReadPolicy}
              >
                {text("保存读取策略", "Save read policy")}
              </Button>
              <span className="text-xs text-zinc-600">
                {text(
                  `读取策略当前版本 ${readPolicy.version}`,
                  `Read policy version ${readPolicy.version}`,
                )}
              </span>
            </Space>
          </>
        )}
      </div>

      <div className="flex max-w-5xl items-center justify-between gap-8 border-b border-zinc-800 pb-5">
        <div>
          <div className="text-sm font-medium text-zinc-200">
            {text("发布后自动扫描", "Scan after publication")}
          </div>
          <div className="mt-1 text-xs leading-5 text-zinc-500">
            {text(
              "仅对保存策略后新发布的制品生效。扫描异步执行，完成后的安全情报可在制品详情中查看。",
              "Applies only to artifacts published after this policy is saved. Scans run asynchronously; completed security intelligence appears in artifact details.",
            )}
          </div>
        </div>
        <Switch
          aria-label={text("发布后自动扫描", "Scan after publication")}
          checked={draft.autoScanOnPublish}
          disabled={!publicationScanning}
          onChange={(value) => update("autoScanOnPublish", value)}
        />
      </div>

      {!publicationScanning && (
        <Alert
          type="warning"
          showIcon
          title={text(
            "当前仓库类型或格式未启用发布后自动扫描。",
            "Scan after publication is not available for this repository type or format.",
          )}
        />
      )}

      <div className="flex max-w-5xl items-center justify-between gap-8 border-b border-zinc-800 pb-5">
        <div>
          <div className="text-sm font-medium text-zinc-200">
            {text("启用准入检查", "Enforce admission checks")}
          </div>
          <div className="mt-1 text-xs leading-5 text-zinc-500">
            {text(
              "启用后，晋升接口会在创建任务前检查制品情报。",
              "When enabled, promotion requests are checked before a job is created.",
            )}
          </div>
        </div>
        <Switch
          aria-label={text("启用准入检查", "Enforce admission checks")}
          checked={draft.enabled}
          checkedChildren={text("已启用", "On")}
          unCheckedChildren={text("已停用", "Off")}
          onChange={(value) => update("enabled", value)}
        />
      </div>

      <div className="grid max-w-5xl grid-cols-2 gap-x-8">
        <PolicySwitch
          label={text("要求签名", "Require a signature")}
          hint={text(
            "没有任何签名证据的制品不能晋升。",
            "Reject artifacts without signature evidence.",
          )}
          checked={draft.requireSignature}
          onChange={(value) => update("requireSignature", value)}
        />
        <PolicySwitch
          label={text("要求已验证签名", "Require a verified signature")}
          hint={text(
            "至少一个签名必须通过验证；适合受保护的发布仓库。",
            "At least one signature must be verified; useful for protected release repositories.",
          )}
          checked={draft.requireVerifiedSignature}
          onChange={(value) => update("requireVerifiedSignature", value)}
        />
        <PolicySwitch
          label={text("要求 SBOM", "Require an SBOM")}
          hint={text(
            "没有软件物料清单的制品不能晋升。",
            "Reject artifacts without a software bill of materials.",
          )}
          checked={draft.requireSbom}
          onChange={(value) => update("requireSbom", value)}
        />
        <PolicySwitch
          label={text("要求 provenance", "Require provenance")}
          hint={text(
            "要求构建来源、构建器和提交信息完整。",
            "Require build source, builder, and commit provenance.",
          )}
          checked={draft.requireProvenance}
          onChange={(value) => update("requireProvenance", value)}
        />
        <PolicySwitch
          label={text("要求漏洞扫描", "Require vulnerability scanning")}
          hint={text(
            "未扫描或扫描状态异常的制品不能绕过检查。",
            "Do not admit artifacts that have not been scanned.",
          )}
          checked={draft.requireVulnerabilityScan}
          onChange={(value) => update("requireVulnerabilityScan", value)}
        />
        <PolicySwitch
          label={text("扫描错误时阻断", "Block on scan errors")}
          hint={text(
            "扫描器返回 error 时阻断晋升；关闭可允许故障降级。",
            "Block when a scanner reports an error; disable for a fail-open fallback.",
          )}
          checked={draft.failOnScanError}
          onChange={(value) => update("failOnScanError", value)}
        />
      </div>

      <div className="grid max-w-5xl grid-cols-2 gap-5 border-t border-zinc-800 pt-5">
        <Field
          label={text(
            "最大允许漏洞等级",
            "Maximum allowed vulnerability severity",
          )}
          hint={text(
            "超过此等级的 affected 结果会拒绝晋升；unknown 始终视为最高风险。",
            "Affected results above this level are rejected. Unknown findings are always treated as highest risk.",
          )}
        >
          <Select
            className="w-full"
            value={draft.maxAllowedSeverity}
            options={severityOptions}
            onChange={(value) => update("maxAllowedSeverity", value)}
          />
        </Field>
        <Field
          label={text("允许的 SPDX 许可证", "Allowed SPDX licenses")}
          hint={text(
            "留空表示不限制；填写后制品必须只包含列表中的 SPDX ID。",
            "Leave empty for no restriction. When set, every license must be in this SPDX ID list.",
          )}
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
            placeholder={text("如 Apache-2.0、MIT", "e.g. Apache-2.0, MIT")}
            onChange={(value) => update("allowedLicenses", value)}
          />
        </Field>
      </div>

      <Space>
        <Button type="primary" onClick={save} loading={saving}>
          {text("保存策略", "Save policy")}
        </Button>
        <span className="text-xs text-zinc-600">
          {text(
            `当前版本 ${policy.version}`,
            `Current version ${policy.version}`,
          )}
        </span>
      </Space>
    </div>
  );
}

function PolicySwitch({
  label,
  hint,
  checked,
  onChange,
}: {
  label: string;
  hint: string;
  checked: boolean;
  onChange: (checked: boolean) => void;
}) {
  return (
    <div className="flex min-h-24 items-start justify-between gap-5 border-b border-zinc-800/80 py-4">
      <div className="min-w-0">
        <div className="text-sm font-medium text-zinc-200">{label}</div>
        <div className="mt-1 text-xs leading-5 text-zinc-500">{hint}</div>
      </div>
      <Switch aria-label={label} checked={checked} onChange={onChange} />
    </div>
  );
}
