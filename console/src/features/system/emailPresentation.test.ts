import { describe, expect, it } from "vitest";
import { quotaLabel } from "./quotaAlertPresentation";
import {
  emailPreviewDocument,
  safeEmailError,
  validEmailTarget,
} from "./emailPresentation";

describe("email input and isolated preview", () => {
  it.each([
    "a@example.test\r\nBcc:x@example.test",
    "a@example.test\u0000",
    "a@example.test, b@example.test",
    "bad",
    "Display <a@example.test>",
  ])(
    "rejects recipient injection and unsupported addresses: %s",
    (recipient) => {
      expect(validEmailTarget("Synthetic", recipient, true)).toBe(false);
    },
  );
  it("requires a recipient only at creation and bounds the safe name", () => {
    expect(validEmailTarget("Synthetic", "", false)).toBe(true);
    expect(validEmailTarget("Synthetic", "", true)).toBe(false);
    expect(validEmailTarget("Synthetic", "a@example.test", true)).toBe(true);
    expect(validEmailTarget("name\nInjected", "a@example.test", true)).toBe(
      false,
    );
    expect(validEmailTarget("界".repeat(129), "", false)).toBe(false);
  });
  it("keeps mail layout while stripping executable, navigation and external elements", () => {
    const html = emailPreviewDocument(
      '<html><head><base href="https://evil.example"><style>p{color:red}</style></head><body><table><tr><td>合成 &amp; Synthetic</td></tr></table><a href="https://evil.example" onclick="alert(1)">Details</a><img src="https://evil.example/pixel"><script>alert(1)</script><iframe src="https://evil.example"></iframe><form action="https://evil.example"><input></form><svg onload="alert(1)"></svg></body></html>',
    );
    expect(html).toContain("default-src 'none'");
    expect(html).toContain("form-action 'none'");
    expect(html).toContain("合成 &amp; Synthetic");
    expect(html).toContain("<table>");
    expect(html).toContain("p{color:red}");
    expect(html).not.toMatch(
      /evil|onclick|<script|<iframe|<img|<form|<svg|<base/i,
    );
    expect(html).toContain("Details");
  });
});

it.each(["constructor", "__proto__", "toString", "unknown-private-raw"])(
  "uses a fixed fallback for unknown server codes: %s",
  (code) => {
    const text = (_zh: string, en: string) => en;
    expect(
      safeEmailError({ code, message: "private-raw" }, text, "test"),
    ).toContain("request failed");
    expect(quotaLabel(code, text)).toBe("Status unknown");
  },
);

it("asks test conflicts to refresh and reconfirm without claiming an address was cleared", () => {
  for (const language of ["zh", "en"]) {
    const text = (zh: string, en: string) => (language === "zh" ? zh : en);
    const message = safeEmailError({ code: "version_conflict" }, text, "test");
    expect(message).not.toMatch(
      /敏感地址已清除|address was cleared|重新编辑|edit again/,
    );
    expect(
      safeEmailError({ code: "version_conflict" }, text, "save"),
    ).not.toMatch(/敏感地址已清除|address was cleared/);
    expect(message).toMatch(
      language === "zh"
        ? /取消.*刷新.*重新确认/
        : /Cancel.*refresh.*confirm again/,
    );
  }
});

it.each(["read", "save", "preview"] as const)(
  "keeps %s failures free of test-only instructions and raw response text",
  (operation) => {
    for (const code of [
      "constructor",
      "__proto__",
      "unknown",
      "rate_limited",
      "queue_full",
      "invalid_request",
    ]) {
      for (const language of ["zh", "en"]) {
        const text = (zh: string, en: string) => (language === "zh" ? zh : en);
        const message = safeEmailError(
          { code, message: "private-response-marker" },
          text,
          operation,
        );
        expect(message).not.toMatch(
          /private-response-marker|同一标识|same key|Retry the same test/,
        );
        expect(message.length).toBeGreaterThan(10);
        if (language === "zh") expect(message).toMatch(/[\u4e00-\u9fff]/);
      }
    }
  },
);
