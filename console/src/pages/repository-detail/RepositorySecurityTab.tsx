import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { Alert, Button, Select, Switch, Tag } from "antd";
import {
  getAptRepositorySigningState,
  getQuarantineReadPolicy,
  getSecurityPolicy,
  replaceQuarantineReadPolicy,
  replaceSecurityPolicy,
} from "../../client";
import type {
  AptRepositorySigningState,
  QuarantineReadPolicy,
  Repository,
  SecurityPolicy,
} from "../../client";
import { ErrorBanner, Loading, isNotFound } from "../../components/Feedback";
import { Field } from "../../components/Layout";
import { usePreferences } from "../../lib/preferences";
import { APTSigningStatePanel } from "./APTSigningStatePanel";
import { RepositoryFeatureUnavailable } from "./RepositoryFeatureUnavailable";
import {
  PolicyCard,
  PolicySectionHeader,
  ScopeFact,
} from "./RepositorySecurityPrimitives";

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
  const [aptSigningState, setAptSigningState] =
    useState<AptRepositorySigningState | null>(null);
  const [aptSigningError, setAptSigningError] = useState<unknown>(null);
  const aptSigningRequest = useRef(0);
  const aptSigningApplicable = repo.format === "apt" && repo.type === "hosted";

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

  const loadAptSigningState = useCallback(async () => {
    const requestId = ++aptSigningRequest.current;
    setAptSigningState(null);
    setAptSigningError(null);
    const { data, error: err } = await getAptRepositorySigningState({
      path: { repositoryId: repo.id },
    });
    if (requestId !== aptSigningRequest.current) return;
    if (err) {
      setAptSigningError(err);
      return;
    }
    if (data) setAptSigningState(data);
  }, [repo.id]);

  useEffect(() => {
    if (aptSigningApplicable) {
      void loadAptSigningState();
      return;
    }
    aptSigningRequest.current += 1;
    setAptSigningState(null);
    setAptSigningError(null);
    void load();
    void loadReadPolicy();
  }, [aptSigningApplicable, load, loadAptSigningState, loadReadPolicy]);

  const update = <K extends keyof SecurityPolicyDraft>(
    key: K,
    value: SecurityPolicyDraft[K],
  ) => {
    setNotice("");
    setSaveError(null);
    setDraft((current) => ({ ...current, [key]: value }));
  };

  const updateReadEnabled = (enabled: boolean) => {
    setReadNotice("");
    setReadError(null);
    setReadEnabled(enabled);
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

  if (aptSigningApplicable) {
    if (aptSigningError !== null) {
      return isNotFound(aptSigningError) ? (
        <RepositoryFeatureUnavailable
          feature={text("APT 签名状态", "APT signing state")}
        />
      ) : (
        <ErrorBanner error={aptSigningError} onRetry={loadAptSigningState} />
      );
    }
    if (!aptSigningState) return <Loading />;
    return <APTSigningStatePanel state={aptSigningState} />;
  }

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
  const readDirty = readPolicy ? readPolicy.enabled !== readEnabled : false;
  const policyDirty =
    JSON.stringify(draft) !== JSON.stringify(draftFromPolicy(policy));
  const readScope = quarantineReadScope(repo.format, text);
  const scanUnavailableLabel = [
    "maven",
    "oci",
    "raw",
    "conan",
    "npm",
    "pypi",
  ].includes(repo.format)
    ? text("未配置可用扫描器", "No scanner configured")
    : text("当前格式不可用", "Unavailable for this format");

  return (
    <div className="grid items-start gap-4 xl:grid-cols-[minmax(320px,0.78fr)_minmax(0,1.55fr)]">
      <div className="border-b border-zinc-800/80 pb-4 xl:col-span-2">
        <div className="text-[11px] font-semibold uppercase tracking-[0.16em] text-cyan-500">
          {text("仓库安全防线", "Repository guardrails")}
        </div>
        <h2 className="mt-1.5 text-lg font-semibold tracking-tight text-zinc-100">
          {text("安全准入与隔离读取", "Admission and quarantined reads")}
        </h2>
        <p className="mt-1.5 max-w-3xl text-sm leading-6 text-zinc-500">
          {text(
            "这里有两条彼此独立的防线：读取策略决定已隔离制品能否被下载；晋升准入决定制品能否进入当前仓库。",
            "Two independent guardrails live here: the read policy decides whether quarantined artifacts can be downloaded, while admission decides whether artifacts may enter this repository.",
          )}
        </p>
      </div>

      <PolicyCard
        eyebrow={text("读取防线", "Read guardrail")}
        title={text("隔离制品读取", "Quarantined artifact reads")}
        description={text(
          "控制客户端是否仍能通过仓库协议读取已经隔离的制品；解除隔离后会立即恢复读取。",
          "Controls whether clients may continue reading quarantined artifacts through repository protocols. Reads resume immediately after release.",
        )}
        status={
          <>
            {readPolicy ? (
              <Tag color={readEnabled ? "warning" : "default"}>
                {readEnabled
                  ? text("阻断读取", "Reads blocked")
                  : text("允许读取", "Reads allowed")}
              </Tag>
            ) : (
              <Tag color="error">{text("状态不可用", "Unavailable")}</Tag>
            )}
            {readDirty && (
              <Tag color="processing">
                {text("有未保存更改", "Unsaved changes")}
              </Tag>
            )}
            {readPolicy && (
              <Switch
                aria-label={text(
                  "阻断隔离制品读取",
                  "Block quarantined artifact reads",
                )}
                checked={readEnabled}
                onChange={updateReadEnabled}
              />
            )}
          </>
        }
      >
        {readError !== null && (
          <ErrorBanner error={readError} onRetry={loadReadPolicy} />
        )}
        {readPolicy && (
          <>
            {readNotice && <Alert type="success" showIcon title={readNotice} />}
            <div className="grid gap-px overflow-hidden rounded-lg border border-zinc-800/80 bg-[var(--ag-border-subtle)] sm:grid-cols-2">
              <ScopeFact
                label={text("影响请求", "Affected requests")}
                value="GET / HEAD"
              />
              <ScopeFact
                label={text("当前格式的阻断粒度", "Current format boundary")}
                value={readScope.boundary}
              />
              <ScopeFact
                label={text("发现元数据", "Discovery metadata")}
                value={readScope.metadata}
              />
              <ScopeFact
                label={text("作为 Group 成员", "When used by a Group")}
                value={text("不降级回退", "No lower-priority fallback")}
              />
            </div>
            <div className="flex flex-col gap-4 border-t border-zinc-800/70 pt-4">
              <p className="max-w-2xl text-xs leading-5 text-zinc-500">
                {text(
                  "默认允许读取以保持升级兼容。启用阻断前，请先检查当前仓库已有的隔离记录。",
                  "Reads remain allowed by default for upgrade compatibility. Review existing quarantine records before enabling enforcement.",
                )}
              </p>
              <div className="flex flex-wrap items-center justify-between gap-3">
                <span className="text-xs text-zinc-600">
                  {text(
                    `读取策略当前版本 ${readPolicy.version}`,
                    `Read policy version ${readPolicy.version}`,
                  )}
                </span>
                <Button
                  type="primary"
                  danger={readEnabled}
                  loading={readSaving}
                  disabled={!readDirty}
                  onClick={saveReadPolicy}
                >
                  {text("保存读取策略", "Save read policy")}
                </Button>
              </div>
            </div>
          </>
        )}
      </PolicyCard>

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
        {notice && <Alert type="success" showIcon title={notice} />}

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

        <div className="flex flex-wrap items-center justify-end gap-3 border-t border-zinc-800/70 pt-4">
          <span className="text-xs text-zinc-600">
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
    </div>
  );
}

function quarantineReadScope(
  format: Repository["format"],
  text: (zh: string, en: string) => string,
): { boundary: string; metadata: string } {
  switch (format) {
    case "raw":
      return {
        boundary: text("路径及校验 sidecar", "Path and checksum sidecars"),
        metadata: text("从目录列表隐藏", "Hidden from directory listings"),
      };
    case "maven":
      return {
        boundary: text("坐标内的制品资产", "Assets within the coordinate"),
        metadata: text("从生成元数据隐藏", "Hidden from generated metadata"),
      };
    case "oci":
      return {
        boundary: text(
          "manifest 与描述符闭包",
          "Manifest and descriptor closure",
        ),
        metadata: text(
          "从 tag、catalog、referrer 隐藏",
          "Hidden from tags, catalog, and referrers",
        ),
      };
    case "npm":
      return {
        boundary: "package@version",
        metadata: text(
          "从 packument 与 dist-tag 隐藏",
          "Hidden from packuments and dist-tags",
        ),
      };
    case "pypi":
      return {
        boundary: "project@version",
        metadata: text("从 Simple 元数据隐藏", "Hidden from Simple metadata"),
      };
    case "conan":
      return {
        boundary: text("recipe revision 闭包", "Recipe revision closure"),
        metadata: text(
          "从 revision 元数据隐藏",
          "Hidden from revision metadata",
        ),
      };
    default:
      return {
        boundary: text("制品身份", "Artifact identity"),
        metadata: text("从发现元数据隐藏", "Hidden from discovery metadata"),
      };
  }
}

function SettingRow({
  label,
  hint,
  meta,
  children,
}: {
  label: string;
  hint: string;
  meta?: ReactNode;
  children: ReactNode;
}) {
  return (
    <div className="flex items-center justify-between gap-6 rounded-lg border border-zinc-800/80 bg-[var(--ag-table-header)] px-4 py-3.5">
      <div className="min-w-0">
        <div className="flex flex-wrap items-center gap-2">
          <span className="text-sm font-medium text-zinc-200">{label}</span>
          {meta}
        </div>
        <p className="mt-1 text-xs leading-5 text-zinc-500">{hint}</p>
      </div>
      <div className="shrink-0">{children}</div>
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
    <div
      className={`flex min-h-24 items-start justify-between gap-5 rounded-lg border px-4 py-3.5 transition-colors ${
        checked
          ? "border-cyan-700/50 bg-[var(--ag-brand-soft)]"
          : "border-zinc-800/80 bg-[var(--ag-table-header)]"
      }`}
    >
      <div className="min-w-0">
        <div className="text-sm font-medium text-zinc-200">{label}</div>
        <div className="mt-1 text-xs leading-5 text-zinc-500">{hint}</div>
      </div>
      <Switch aria-label={label} checked={checked} onChange={onChange} />
    </div>
  );
}
