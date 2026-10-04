import { type Translate } from "./quotaAlertPresentation";

export function validEmailTarget(
  name: string,
  recipient: string,
  create: boolean,
) {
  if (!name.trim() || [...name.trim()].length > 128 || /[\r\n\0]/.test(name))
    return false;
  if (!recipient) return !create;
  // Deliberately a single bare mailbox; the server remains authoritative.
  return (
    recipient.length <= 254 &&
    /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(recipient) &&
    ![...recipient].some(
      (c) =>
        c.codePointAt(0)! < 33 ||
        c.codePointAt(0) === 127 ||
        '<>(),;:\\"[]'.includes(c),
    )
  );
}

export function emailCode(error: unknown) {
  return typeof error === "object" &&
    error !== null &&
    "code" in error &&
    typeof error.code === "string"
    ? error.code
    : "";
}
export function emailConflict(error: unknown) {
  return [
    "version_conflict",
    "idempotency_conflict",
    "target_disabled",
    "target_changed",
  ].includes(emailCode(error));
}
export function safeEmailError(error: unknown, text: Translate) {
  const labels: Record<string, [string, string]> = {
    version_conflict: [
      "配置已变更。敏感地址已清除，请刷新最新目标并重新编辑。",
      "Configuration changed. The sensitive address was cleared; refresh the latest target and edit again.",
    ],
    idempotency_conflict: [
      "此测试标识已有不同请求。取消后刷新目标并重新确认。",
      "This test key belongs to a different request. Cancel, refresh the target, and confirm again.",
    ],
    target_disabled: [
      "目标已停用。取消后刷新目标；没有自动重试发送。",
      "The target is disabled. Cancel and refresh; no send was automatically retried.",
    ],
    target_changed: [
      "目标已变更。取消后刷新并重新确认。",
      "The target changed. Cancel, refresh, and confirm again.",
    ],
    rate_limited: [
      "测试频率已达上限，请稍后重试同一测试。",
      "The test rate limit was reached. Retry the same test later.",
    ],
    queue_full: [
      "交付队列已满，请稍后重试同一测试。",
      "The delivery queue is full. Retry the same test later.",
    ],
    encryption_key_unavailable: [
      "加密配置不可用，请联系部署管理员。",
      "Encryption is unavailable. Contact the deployment administrator.",
    ],
    email_disabled: [
      "邮件通道未开启，请联系部署管理员。",
      "The email channel is disabled. Contact the deployment administrator.",
    ],
    invalid_request: [
      "目标或测试未通过校验，请检查名称、单个收件人地址和语言。",
      "Validation failed. Check the name, single recipient address, and language.",
    ],
    not_found: [
      "目标或接口不可用，请取消并刷新。",
      "The target or endpoint is unavailable. Cancel and refresh.",
    ],
    password_change_required: [
      "必须先修改密码，再返回此页。",
      "Change your password before returning to this page.",
    ],
    permission_denied: [
      "无权访问邮件配置，请确认平台管理员权限后刷新。",
      "Email access denied. Confirm platform administrator access and refresh.",
    ],
    forbidden: [
      "无权访问邮件配置，请确认平台管理员权限后刷新。",
      "Email access denied. Confirm platform administrator access and refresh.",
    ],
    access_denied: [
      "会话无权访问邮件配置，请重新登录。",
      "This session cannot access email settings. Sign in again.",
    ],
    unauthenticated: [
      "会话已失效，请重新登录。",
      "The session expired. Sign in again.",
    ],
  };
  const code = emailCode(error);
  const label = Object.hasOwn(labels, code) ? labels[code] : undefined;
  return label
    ? text(...label)
    : text(
        "请求失败，结果可能尚未返回。测试重试会使用同一标识；请刷新核对交付结果。",
        "The request failed and its result may be unknown. Test retries use the same key; refresh to check delivery results.",
      );
}

/** Defense in depth for API HTML: rebuild a finite inert mail layout before sandboxing. */
export function emailPreviewDocument(
  html: string,
  locale: "en" | "zh-CN" = "en",
) {
  // Template contents are inert fragments, including their resource elements.
  // DOMParser documents may fetch images before a later filtering pass.
  const source = document.createElement("template");
  source.innerHTML = html.slice(0, 200000);
  const output = document.implementation.createHTMLDocument("");
  const tags = new Set([
    "DIV",
    "P",
    "SPAN",
    "H1",
    "H2",
    "H3",
    "TABLE",
    "TBODY",
    "THEAD",
    "TFOOT",
    "TR",
    "TD",
    "TH",
    "BR",
    "HR",
    "STRONG",
    "B",
    "EM",
    "I",
    "UL",
    "OL",
    "LI",
    "STYLE",
  ]);
  const attributes = new Set([
    "style",
    "class",
    "width",
    "height",
    "cellpadding",
    "cellspacing",
    "border",
    "align",
    "valign",
    "colspan",
    "rowspan",
    "role",
    "lang",
    "dir",
    "aria-label",
    "bgcolor",
  ]);
  const copy = (node: Node, parent: Node) => {
    if (node.nodeType === Node.TEXT_NODE) {
      parent.appendChild(output.createTextNode(node.textContent ?? ""));
      return;
    }
    if (
      !(node instanceof Element) ||
      node.namespaceURI !== "http://www.w3.org/1999/xhtml"
    )
      return;
    if (node.tagName === "A") {
      const span = output.createElement("span");
      parent.appendChild(span);
      if (node.hasAttribute("style"))
        span.setAttribute("style", node.getAttribute("style")!);
      node.childNodes.forEach((child) => copy(child, span));
      return;
    }
    if (!tags.has(node.tagName)) return;
    const next = output.createElement(node.tagName.toLowerCase());
    for (const attr of node.attributes)
      if (attributes.has(attr.name)) next.setAttribute(attr.name, attr.value);
    parent.appendChild(next);
    node.childNodes.forEach((child) => copy(child, next));
  };
  source.content.childNodes.forEach((node) => copy(node, output.body));
  // This policy is first, before all supplied styles. No scripts, URLs, forms or nested frames.
  return `<!doctype html><html lang="${locale === "zh-CN" ? "zh-CN" : "en"}"><head><meta http-equiv="Content-Security-Policy" content="default-src 'none'; script-src 'none'; style-src 'unsafe-inline'; img-src 'none'; font-src 'none'; connect-src 'none'; frame-src 'none'; object-src 'none'; base-uri 'none'; form-action 'none'"><meta name="viewport" content="width=device-width, initial-scale=1"></head><body style="margin:0;padding:0;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI','PingFang SC','Microsoft YaHei',Arial,sans-serif">${output.body.innerHTML}</body></html>`;
}
