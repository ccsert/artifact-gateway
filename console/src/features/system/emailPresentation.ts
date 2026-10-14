import type { MessageKey } from "../../messages";
import type { Translate } from "../../lib/preferences";

export function emailTargetErrors(
  name: string,
  recipient: string,
  create: boolean,
) {
  const errors: Partial<Record<"name" | "recipient", "required" | "invalid">> =
    {};
  if (!name.trim()) errors.name = "required";
  if (!name.trim() || [...name.trim()].length > 128 || /[\r\n\0]/.test(name))
    errors.name ??= "invalid";
  if (!recipient) {
    if (create) errors.recipient = "required";
    return errors;
  }
  // Deliberately a single bare mailbox; the server remains authoritative.
  if (!(
    recipient.length <= 254 &&
    /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(recipient) &&
    ![...recipient].some(
      (c) =>
        c.codePointAt(0)! < 33 ||
        c.codePointAt(0) === 127 ||
        '<>(),;:\\"[]'.includes(c),
    )
  ))
    errors.recipient = "invalid";
  return errors;
}

export function validEmailTarget(
  name: string,
  recipient: string,
  create: boolean,
) {
  return Object.keys(emailTargetErrors(name, recipient, create)).length === 0;
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
export type EmailOperation = "read" | "save" | "test" | "preview";
export function safeEmailError(
  error: unknown,
  t: Translate,
  operation: EmailOperation,
) {
  const labels: Record<string, MessageKey> = {
    version_conflict: "system.targetSettingsChangedRefreshTheLatestTargetAnd",
    idempotency_conflict: "system.thisTestKeyBelongsToADifferentRequest",
    target_disabled: "system.theTargetIsDisabledCancelAndRefreshNo",
    target_changed: "system.theTargetChangedCancelRefreshAndConfirmAgain",
    rate_limited: "system.theTestRateLimitWasReachedRetryThe",
    queue_full: "system.theDeliveryQueueIsFullRetryTheSame",
    encryption_key_unavailable:
      "system.encryptionIsUnavailableContactTheDeploymentAdministrator",
    email_disabled: "system.theEmailChannelIsDisabledContactTheDeployment",
    invalid_request:
      "system.validationFailedCheckTheNameSingleRecipientAddress",
    not_found: "system.theTargetOrEndpointIsUnavailableCancelAnd",
    password_change_required:
      "system.changeYourPasswordBeforeReturningToThisPage",
    permission_denied:
      "system.emailAccessDeniedConfirmPlatformAdministratorAccessAnd",
    forbidden: "system.emailAccessDeniedConfirmPlatformAdministratorAccessAnd",
    access_denied: "system.thisSessionCannotAccessEmailSettingsSignIn",
    unauthenticated: "system.theSessionExpiredSignInAgain",
  };
  const code = emailCode(error);
  const fallback: Record<EmailOperation, MessageKey> = {
    read: "system.couldNotReadEmailNotificationDataRetryThe",
    save: "system.couldNotConfirmTheSaveRefreshAndCheck",
    preview: "system.couldNotGenerateTheTemplatePreviewRetryThe",
    test: "system.theRequestFailedAndItsResultMayBe",
  };
  const invalid: Record<EmailOperation, MessageKey> = {
    read: "system.theReadRequestWasInvalidRefreshAndCheck",
    save: "system.targetValidationFailedCheckTheNameSingleRecipient",
    preview: "system.previewValidationFailedCheckTheScenarioAndLanguage",
    test: "system.testValidationFailedCheckTheTargetScenarioAnd",
  };
  const testOnly = [
    "idempotency_conflict",
    "target_disabled",
    "target_changed",
    "rate_limited",
    "queue_full",
  ];
  const scoped: Partial<Record<EmailOperation, Record<string, MessageKey>>> = {
    test: {
      version_conflict: "system.targetSettingsChangedCancelThisTestRefreshThe",
    },
    read: {
      rate_limited: "system.readsAreRateLimitedRetryTheReadLater",
      version_conflict: "system.emailSettingsChangedRetryTheRead",
      not_found: "system.emailResourcesOrEndpointsAreUnavailableRetryThe",
    },
    save: {
      rate_limited: "system.saveRequestsAreRateLimitedRetryLater",
      target_disabled: "system.theEmailTargetWasDisabledRefreshAndCheck",
      target_changed: "system.theEmailTargetChangedRefreshAndConfirmIts",
    },
    preview: {
      rate_limited: "system.previewRequestsAreRateLimitedRetryThePreview",
      not_found: "system.theTemplatePreviewEndpointIsUnavailableRefreshAnd",
    },
  };
  const contextLabels = scoped[operation];
  const label =
    code === "invalid_request"
      ? invalid[operation]
      : contextLabels && Object.hasOwn(contextLabels, code)
        ? contextLabels[code]
        : operation !== "test" && testOnly.includes(code)
          ? undefined
          : Object.hasOwn(labels, code)
            ? labels[code]
            : undefined;
  return label ? t(label) : t(fallback[operation]);
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
