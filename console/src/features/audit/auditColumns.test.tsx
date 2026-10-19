import { cleanup, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, expect, it } from "vitest";
import type { AuditRecord } from "../../client";
import { createTranslator } from "../../messages/translate";
import { AuditRecordDetail } from "./auditColumns";

const sessionId = "31b59e86-51c7-4bbc-989c-cf130ea5f9ae";
// The native publication store emits this bounded evidence for a committed
// recipe or package revision (conanNativePublicationAudit).
const publication: AuditRecord = {
  occurredAt: "2026-10-19T00:00:00Z",
  format: "conan",
  operation: "publish",
  outcome: "resolved",
  actor: "service-account:conan-producer",
  evidence: {
    publicationSessionId: sessionId,
    publicationState: "committed",
    digest: `sha256:${"a".repeat(64)}`,
    replay: "false",
  },
};

function detail(record: AuditRecord, locale: "zh-CN" | "en-US" = "zh-CN") {
  render(
    <MemoryRouter>
      <AuditRecordDetail record={record} t={createTranslator(locale)} />
    </MemoryRouter>,
  );
}

afterEach(cleanup);

it.each([
  ["zh-CN", "发布会话", "发布状态", "已提交"],
  ["en-US", "Publication session", "Publication state", "Committed"],
] as const)(
  "links Conan publication evidence in %s",
  (locale, session, state, committed) => {
    detail(publication, locale);
    expect(screen.getByText(session)).toBeVisible();
    expect(screen.getByText(sessionId)).toBeVisible();
    expect(screen.getByText(state)).toBeVisible();
    expect(screen.getByText(committed)).toBeVisible();
    expect(
      screen.queryByText(publication.evidence!.digest),
    ).not.toBeInTheDocument();
    expect(screen.queryByText("false")).not.toBeInTheDocument();
  },
);

it.each([
  { format: "maven" },
  { operation: "get" },
  { format: undefined },
  { operation: undefined },
])("does not read publication evidence outside Conan publish: %j", (scope) => {
  detail({ ...publication, ...scope });
  expect(screen.queryByText(sessionId)).not.toBeInTheDocument();
  expect(screen.queryByText("发布会话")).not.toBeInTheDocument();
  expect(screen.queryByText("已提交")).not.toBeInTheDocument();
});

it.each([
  undefined,
  null,
  '{"publicationState":"committed"}',
  [],
  42,
  { publicationSessionId: 42, publicationState: "committed" },
  { publicationSessionId: [sessionId], publicationState: "committed" },
  { publicationSessionId: "bad<session>", publicationState: "committed" },
  { publicationSessionId: "a".repeat(4096), publicationState: "committed" },
])("keeps malformed publication evidence unavailable: %j", (evidence) => {
  // Network JSON is not guaranteed to match the generated TypeScript shape.
  detail(JSON.parse(JSON.stringify({ ...publication, evidence })));
  const session = screen.getByText("发布会话").parentElement!;
  const state = screen.getByText("发布状态").parentElement!;
  expect(session).toHaveTextContent("—");
  expect(state).toHaveTextContent("—");
  expect(screen.queryByText("已提交")).not.toBeInTheDocument();
  expect(screen.queryByText("bad<session>")).not.toBeInTheDocument();
});

it.each([undefined, null, 42, ["committed"], "open", "unknown", "<script>"])(
  "keeps an unsupported publication state unavailable: %j",
  (publicationState) => {
    detail(
      JSON.parse(
        JSON.stringify({
          ...publication,
          evidence: { publicationSessionId: sessionId, publicationState },
        }),
      ),
    );
    expect(screen.getByText(sessionId)).toBeVisible();
    expect(screen.getByText("发布状态").parentElement).toHaveTextContent("—");
    expect(screen.queryByText("已提交")).not.toBeInTheDocument();
  },
);
