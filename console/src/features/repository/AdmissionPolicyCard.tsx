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
  const { text } = usePreferences();
  const setStoredPolicy = useSetSecurityPolicy(repo.id);
  const [draft, setDraft] = useState<SecurityPolicyDraft>(() =>
    draftFromPolicy(policy),
  );
  const [saveError, setSaveError] = useState<unknown>(null);
  const [saving, setSaving] = useState(false);
  const [notice, setNotice] = useState("");
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
      setNotice(text("仓库安全策略已保存", "Repository security policy saved"));
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
    { value: "none", label: text("不允许漏洞", "No vulnerabilities") },
    { value: "low", label: text("最多低危", "Up to low") },
    { value: "medium", label: text("最多中危", "Up to medium") },
    { value: "high", label: text("最多高危", "Up to high") },
    { value: "critical", label: text("允许严重等级", "Allow critical") },
  ];
  const policyDirty =
    JSON.stringify(draft) !== JSON.stringify(draftFromPolicy(policy));
  const scanUnavailableLabel = formatDescriptor(repo.format)?.scannerSupport
    ? text("未配置可用扫描器", "No scanner configured")
    : text("当前格式不可用", "Unavailable for this format");

  return (
    <PolicyCard
      eyebrow={text("晋升防线", "Promotion guardrail")}
      title={text("晋升准入", "Promotion admission")}
      description={text(
        "在创建晋升任务前评估签名、SBOM、构建来源和漏洞情报；不会改变普通协议读取。",
        "Evaluates signatures, SBOMs, provenance, and vulnerability intelligence before a promotion job is created. Ordinary reads are unchanged.",
      )}
      status={
        <>
          <Tag color={draft.enabled ? "success" : "default"}>
            {draft.enabled
              ? text("正在执行", "Enforced")
              : text("尚未执行", "Not enforced")}
          </Tag>
          {policyDirty && (
            <Tag color="processing">
              {text("有未保存更改", "Unsaved changes")}
            </Tag>
          )}
          <Switch
            aria-label={text("启用准入检查", "Enforce admission checks")}
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
        title={text("安全情报", "Security intelligence")}
        description={text(
          "决定新发布制品是否自动生成后续准入所需的情报。",
          "Controls whether newly published artifacts automatically produce intelligence used by admission.",
        )}
      />
      <SettingRow
        label={text("发布后自动扫描", "Scan after publication")}
        hint={text(
          "仅对保存策略后新发布的制品生效；扫描异步执行，结果可在制品详情中查看。",
          "Applies only to artifacts published after this policy is saved. Scans run asynchronously and results appear in artifact details.",
        )}
        meta={
          !publicationScanning && (
            <Tag color="warning">{scanUnavailableLabel}</Tag>
          )
        }
      >
        <Switch
          aria-label={text("发布后自动扫描", "Scan after publication")}
          checked={draft.autoScanOnPublish}
          disabled={!publicationScanning}
          onChange={(value) => update("autoScanOnPublish", value)}
        />
      </SettingRow>

      <PolicySectionHeader
        title={text("证据要求", "Evidence requirements")}
        description={
          draft.enabled
            ? text(
                "已选择的条件会在每次晋升前执行。",
                "Selected requirements run before every promotion.",
              )
            : text(
                "可提前配置条件；只有启用晋升准入后才会执行。",
                "Requirements may be configured in advance and take effect only after admission is enabled.",
              )
        }
      />
      <div className="grid gap-3 md:grid-cols-2">
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

      <PolicySectionHeader
        title={text("风险门槛", "Risk thresholds")}
        description={text(
          "进一步限制允许通过的漏洞等级与许可证集合。",
          "Further restrict the vulnerability severity and license set allowed through admission.",
        )}
      />
      <div className="grid gap-5 md:grid-cols-2">
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

      <div className="flex flex-wrap items-center justify-end gap-3 border-t border-line pt-4">
        <span className="text-xs text-fg-disabled">
          {text(
            `当前版本 ${policy.version}`,
            `Current version ${policy.version}`,
          )}
        </span>
        <Button
          type="primary"
          onClick={save}
          loading={saving}
          disabled={!policyDirty}
        >
          {text("保存策略", "Save policy")}
        </Button>
      </div>
    </PolicyCard>
  );
}
