import { enUS, zhCN, type MessageKey } from ".";

export type MessageLocale = "zh-CN" | "en-US";

export type MessageVariables = Record<string, string | number>;

/**
 * A translator bound to one locale. `{name}` placeholders take the matching
 * variable; a placeholder without one stays literal, and inserted values are
 * never matched again.
 */
export function createTranslator(locale: MessageLocale) {
  const messages: Record<MessageKey, string> = locale === "zh-CN" ? zhCN : enUS;
  return (key: MessageKey, variables?: MessageVariables): string => {
    const message = messages[key];
    if (!variables) return message;
    return message.replace(/\{([^{}]+)\}/g, (placeholder, name: string) =>
      Object.hasOwn(variables, name) ? String(variables[name]) : placeholder,
    );
  };
}

export type Translator = ReturnType<typeof createTranslator>;

/**
 * A failure whose text is a message key, so code without a translator (query
 * functions, readers) can still fail in the reader's language. Whatever shows
 * the error translates it with `errorText`; `message` holds the Chinese text.
 */
export class LocalizedError extends Error {
  readonly key: MessageKey;
  readonly variables?: MessageVariables;
  readonly status?: number;

  constructor(
    key: MessageKey,
    variables?: MessageVariables,
    options?: { status?: number },
  ) {
    // The message is readable where nothing translates it (logs, tests).
    super(createTranslator("zh-CN")(key, variables));
    this.name = "LocalizedError";
    this.key = key;
    this.variables = variables;
    this.status = options?.status;
  }
}

/** An error's text in the reader's language, else the fallback message. */
export function errorText(
  error: unknown,
  t: Translator,
  fallback: MessageKey,
): string {
  if (error instanceof LocalizedError) return t(error.key, error.variables);
  if (error instanceof Error && error.message) return error.message;
  return t(fallback);
}
