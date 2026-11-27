// Helpers that read Claude's tool calls, for the transcript and approval cards.
import { isRawJSON, stringify } from "@ungap/raw-json";
import { snapshot } from "solid-js";

// Ox's own MCP tools, defined in internal/server/mcp.go. Claude Code names
// them for the server internal/claude/claude.go registers, such as
// mcp__ox__send_message, and Ur names them as Ox does, such as send_message.
const OX_PREFIX = "mcp__ox__";
const OX_TOOLS = new Set([
  "list_models",
  "list_sessions",
  "send_message",
  "read_session",
  "create_session",
  "create_top_level_session",
]);

// oxTool is Ox's own name for a call to one of its tools, or undefined for
// any other tool.
function oxTool(tool: string): string | undefined {
  const name = tool.startsWith(OX_PREFIX) ? tool.slice(OX_PREFIX.length) : tool;
  return OX_TOOLS.has(name) ? name : undefined;
}

// toolLabel is the name a tool call shows. Ox's tools show their own name in
// PascalCase, as built-in tools do; every other name shows as it arrives.
export function toolLabel(tool: string): string {
  const name = oxTool(tool);
  if (name === undefined) return tool;
  return name
    .split("_")
    .map((word) => word.charAt(0).toUpperCase() + word.slice(1))
    .join("");
}

// The input field that summarizes an Ox tool call, where it is not the first
// string.
const OX_SUBJECT_FIELD: Record<string, string> = {
  send_message: "text",
  create_session: "title",
  create_top_level_session: "title",
};

// toolSubject is what a tool call acts on, such as Bash's command or Edit's
// file: the first string in its input at any depth, such as AskUserQuestion's
// first question. An input with no keys has no subject, and an input with no
// string is shown as compact JSON.
export function toolSubject(tool: string, input: Record<string, unknown>): string {
  const plain = snapshot(input);
  if (Object.keys(plain).length === 0) return "";
  const name = oxTool(tool);
  const field = name === undefined ? undefined : OX_SUBJECT_FIELD[name];
  if (field !== undefined && typeof plain[field] === "string") return plain[field];
  return firstString(plain) ?? stringify(plain);
}

// toolSummary is the first nonblank line of a tool call's subject, cut to 120
// characters.
export function toolSummary(tool: string, input: Record<string, unknown>): string {
  const line = toolSubject(tool, input)
    .split("\n")
    .find((l) => l.trim() !== "");
  return (line ?? "").slice(0, 120);
}

function firstString(value: unknown): string | undefined {
  if (typeof value === "string") return value;
  if (!value || typeof value !== "object" || isRawJSON(value)) return undefined;
  for (const v of Object.values(value)) {
    const found = firstString(v);
    if (found !== undefined) return found;
  }
  return undefined;
}

// planOf is the plan of an ExitPlanMode call, which is Markdown.
export const planOf = (tool: string, input: Record<string, unknown>) =>
  tool === "ExitPlanMode" && typeof input.plan === "string" ? input.plan : undefined;

// Question is one question of an AskUserQuestion call, Claude's payload.
export type Question = {
  question: string;
  header?: string;
  options: { label: string; description?: string }[];
  multiSelect: boolean;
};

// questionsOf is the questions of an AskUserQuestion call, or undefined when
// the input does not have the recorded shape.
export function questionsOf(tool: string, input: Record<string, unknown>): Question[] | undefined {
  if (tool !== "AskUserQuestion" || !Array.isArray(input.questions) || input.questions.length === 0) return undefined;
  const str = (v: unknown) => (typeof v === "string" ? v : undefined);
  const questions: Question[] = [];
  for (const q of input.questions) {
    if (!q || typeof q.question !== "string" || !Array.isArray(q.options)) return undefined;
    const options: Question["options"] = [];
    for (const o of q.options) {
      if (!o || typeof o.label !== "string") return undefined;
      options.push({ label: o.label, description: str(o.description) });
    }
    questions.push({ question: q.question, header: str(q.header), options, multiSelect: q.multiSelect === true });
  }
  return questions;
}

// Raw JSON numbers must reach the serializer outside the store's proxy.
export const toolInputJSON = (input: Record<string, unknown>) => stringify(snapshot(input), null, 2);
