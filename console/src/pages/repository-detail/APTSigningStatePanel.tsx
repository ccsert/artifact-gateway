import { Alert, Tag } from "antd";
import type { AptRepositorySigningState } from "../../client";
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
  const { locale, text } = usePreferences();
  const presentation = aptSigningPresentation(state.readiness, text);
  const current = state.currentSnapshot;
  return (
    <div className="space-y-4">
      <div className="border-b border-zinc-800/80 pb-4">
        <div className="text-xs font-semibold uppercase tracking-[0.16em] text-cyan-500">
          {text("APT 发布信任", "APT publication trust")}
        </div>
        <h2 className="mt-1.5 text-lg font-semibold tracking-tight text-zinc-100">
          {text("签名信任与当前快照", "Signing trust and current snapshot")}
        </h2>
        <p className="mt-1.5 max-w-3xl text-sm leading-6 text-zinc-500">
          {text(
            "核对 Gateway 当前允许的签名密钥、轮换窗口，以及最近一次原子发布实际使用的不可变签名证据。这里不会展示 signer 地址、令牌或私钥信息。",
            "Verify the signing keys currently trusted by Gateway, the rotation window, and immutable evidence from the latest atomic publication. Signer endpoints, tokens, and private-key material are never shown here.",
          )}
        </p>
      </div>
      <PolicyCard
        eyebrow={text("发布根信任", "Publication root of trust")}
        title={text("APT Release 签名", "APT Release signing")}
        description={text(
          "远程模式必须使用固定公钥指纹；双指纹表示受控的旧/新密钥重叠窗口。",
          "Remote mode requires pinned public-key fingerprints. Two fingerprints represent a controlled old/new overlap window.",
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
        <div className="grid gap-px overflow-hidden rounded-lg border border-zinc-800/80 bg-[var(--ag-border-subtle)] sm:grid-cols-2 lg:grid-cols-4">
          <ScopeFact
            label={text("签名器模式", "Signer mode")}
            value={aptSignerModeLabel(state.signerMode, text)}
          />
          <ScopeFact
            label={text("可信密钥数量", "Trusted key count")}
            value={String(state.trustedFingerprints.length)}
          />
          <ScopeFact
            label={text("当前密钥角色", "Current key role")}
            value={aptKeyRoleLabel(state.currentKeyRole, text)}
          />
          <ScopeFact
            label={text("最近发布", "Latest publication")}
            value={current ? `${current.suite} · #${current.sequence}` : "—"}
          />
        </div>

        <PolicySectionHeader
          title={text("可信指纹窗口", "Trusted fingerprint window")}
          description={text(
            "第一枚是主密钥，第二枚是轮换期间允许的新密钥；客户端公钥分发仍由运维独立完成。",
            "The first fingerprint is primary and the optional second is the next key accepted during rotation. Client key distribution remains an explicit operator action.",
          )}
        />
        {state.trustedFingerprints.length === 0 ? (
          <div className="rounded-lg border border-dashed border-zinc-800 px-4 py-4 text-sm text-zinc-500">
            {text(
              "未配置远程可信指纹。",
              "No remote trusted fingerprints configured.",
            )}
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
                      ? "border-cyan-700/50 bg-[var(--ag-brand-soft)]"
                      : "border-zinc-800/80 bg-[var(--ag-table-header)]"
                  }`}
                >
                  <div className="flex items-center gap-2">
                    <Tag color={index === 0 ? "blue" : "purple"}>
                      {index === 0
                        ? text("主密钥", "Primary")
                        : text("轮换密钥", "Next")}
                    </Tag>
                    {isCurrent && (
                      <Tag color="success">{text("当前使用", "In use")}</Tag>
                    )}
                  </div>
                  <code className="break-all font-mono text-xs text-zinc-300">
                    {fingerprint}
                  </code>
                </div>
              );
            })}
          </div>
        )}

        <PolicySectionHeader
          title={text("最近一次不可变证据", "Latest immutable evidence")}
          description={text(
            "这些字段来自已提交的可见快照，而不是 signer 当前自报状态。",
            "These values come from the committed visible snapshot, not the signer's current self-reported state.",
          )}
        />
        {current ? (
          <div className="grid gap-3 md:grid-cols-2">
            <SigningEvidence
              label={text("Suite / 序列", "Suite / sequence")}
              value={`${current.suite} / ${current.sequence}`}
            />
            <SigningEvidence
              label={text("发布时间", "Published at")}
              value={formatDate(current.publishedAt, locale)}
            />
            <SigningEvidence
              label={text("签名身份", "Signer identity")}
              value={current.signerIdentity}
              mono
            />
            <SigningEvidence
              label={text("签名算法", "Signature algorithm")}
              value={current.signatureAlgorithm}
              mono
            />
            <SigningEvidence
              label={text("密钥指纹", "Key fingerprint")}
              value={current.keyFingerprint}
              mono
            />
            <SigningEvidence
              label={text("Release 摘要", "Release digest")}
              value={current.releaseDigest}
              mono
            />
          </div>
        ) : (
          <div className="rounded-lg border border-dashed border-zinc-800 px-4 py-4 text-sm text-zinc-500">
            {text(
              "该仓库尚未发布可见快照。",
              "This repository has no visible snapshot yet.",
            )}
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
    <div className="min-w-0 rounded-lg border border-zinc-800/80 bg-[var(--ag-table-header)] px-4 py-3">
      <div className="text-xs font-medium text-zinc-600">{label}</div>
      <div
        className={`mt-1 break-all text-xs text-zinc-300 ${mono ? "font-mono" : "font-medium"}`}
      >
        {value}
      </div>
    </div>
  );
}

function aptSigningPresentation(
  readiness: AptRepositorySigningState["readiness"],
  text: (zh: string, en: string) => string,
): {
  color: string;
  label: string;
  warning: { type: "info" | "warning" | "error"; title: string } | null;
} {
  switch (readiness) {
    case "ready":
      return {
        color: "success",
        label: text("生产信任已固定", "Production trust pinned"),
        warning: null,
      };
    case "rotation_overlap":
      return {
        color: "processing",
        label: text("密钥轮换重叠", "Key rotation overlap"),
        warning: {
          type: "info",
          title: text(
            "当前允许旧/新两枚密钥。确认新快照已使用轮换密钥，并等待客户端信任窗口结束后再移除旧密钥。",
            "Both old and new keys are accepted. Confirm a new snapshot uses the next key, then remove the old key only after the client trust window closes.",
          ),
        },
      };
    case "fixture":
      return {
        color: "warning",
        label: text("仅参考签名器", "Reference signer only"),
        warning: {
          type: "warning",
          title: text(
            "当前使用本地参考签名器，只适合验收 H2 流程，不应作为生产根信任。",
            "The local reference signer is suitable only for H2 validation and must not become a production root of trust.",
          ),
        },
      };
    case "policy_mismatch":
      return {
        color: "error",
        label: text("信任策略不匹配", "Trust policy mismatch"),
        warning: {
          type: "error",
          title: text(
            "最近可见快照使用的密钥不在当前信任策略中，或远程信任配置不完整。发布前请先修复配置。",
            "The latest visible snapshot uses a key outside the active trust policy, or remote trust configuration is incomplete. Repair the policy before publishing.",
          ),
        },
      };
    case "unconfigured":
      return {
        color: "default",
        label: text("尚未配置", "Not configured"),
        warning: {
          type: "warning",
          title: text(
            "未配置可用签名器；当前无法发布新的 APT 快照。",
            "No usable signer is configured, so new APT snapshots cannot be published.",
          ),
        },
      };
    default:
      return {
        color: "default",
        label: text("未知状态", "Unknown state"),
        warning: null,
      };
  }
}

function aptSignerModeLabel(
  mode: AptRepositorySigningState["signerMode"],
  text: (zh: string, en: string) => string,
) {
  if (mode === "remote") return text("远程固定信任", "Remote pinned trust");
  if (mode === "reference")
    return text("本地参考签名器", "Local reference signer");
  return text("已禁用", "Disabled");
}

function aptKeyRoleLabel(
  role: AptRepositorySigningState["currentKeyRole"],
  text: (zh: string, en: string) => string,
) {
  if (role === "active") return text("当前信任密钥", "Active trust key");
  if (role === "next") return text("轮换密钥", "Next");
  if (role === "fixture") return text("参考密钥", "Fixture");
  if (role === "outside_policy") return text("策略外密钥", "Outside policy");
  return "—";
}
