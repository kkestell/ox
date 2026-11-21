// Helpers that read Claude's tool calls, for the transcript and approval cards.
import { isRawJSON, stringify } from "@ungap/raw-json";
import { snapshot } from "solid-js";

// toolSubject is what a tool call acts on, such as Bash's command or Edit's
// file: the first string in its input at any depth, such as AskUserQuestion's
// first question. An input with no string is shown as compact JSON.
export function toolSubject(input: Record<string, unknown>): string {
  const plain = snapshot(input);
  return firstString(plain) ?? stringify(plain);
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
