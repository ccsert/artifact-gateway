import { describe, expect, it } from "vitest";
import {
  auditOutcomeIsDenied,
  auditOutcomeLabel,
  auditOutcomeTone,
} from "./auditOutcomes";

const zh = (chinese: string) => chinese;
const en = (_chinese: string, english: string) => english;

describe("auditOutcomeLabel", () => {
  it("labels every outcome the gateway records, in both languages", () => {
    const samples: [string, string, string][] = [
      ["resolved", "已放行", "Resolved"],
      ["access_denied", "访问被拒", "Access denied"],
      ["internal_preferred", "命中内部副本", "Internal copy preferred"],
      ["not_found", "未找到", "Not found"],
      ["group_disabled", "分组已停用", "Group disabled"],
      ["proxy_denied", "代理拒绝", "Proxy denied"],
      ["upstream_error", "上游错误", "Upstream error"],
      ["storage_error", "存储错误", "Storage error"],
    ];
    for (const [code, chinese, english] of samples) {
      expect(auditOutcomeLabel(code, zh)).toBe(chinese);
      expect(auditOutcomeLabel(code, en)).toBe(english);
    }
  });

  it("keeps an unknown outcome readable by showing the code itself", () => {
    expect(auditOutcomeLabel("future_outcome", zh)).toBe("future_outcome");
  });
});

describe("auditOutcomeTone", () => {
  it("separates refusals from the rest", () => {
    expect(auditOutcomeTone("resolved")).toBe("success");
    expect(auditOutcomeTone("access_denied")).toBe("danger");
    expect(auditOutcomeTone("storage_error")).toBe("danger");
    expect(auditOutcomeTone("not_found")).toBe("warning");
    expect(auditOutcomeTone(undefined)).toBe("neutral");
  });
});

describe("auditOutcomeIsDenied", () => {
  it("counts every refusal as denied and nothing else", () => {
    expect(auditOutcomeIsDenied("denied")).toBe(true);
    expect(auditOutcomeIsDenied("access_denied")).toBe(true);
    expect(auditOutcomeIsDenied("proxy_denied")).toBe(true);
    expect(auditOutcomeIsDenied("resolved")).toBe(false);
    expect(auditOutcomeIsDenied("storage_error")).toBe(false);
    expect(auditOutcomeIsDenied(undefined)).toBe(false);
  });
});
