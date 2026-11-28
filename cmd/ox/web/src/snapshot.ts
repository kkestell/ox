import type { ApprovalRequest, Methods, Prompt, Settings, ToolResult, Update, UsageWindow } from "./protocol";

// A session's entries are its conversation, in order. A message is a prompt
// from another session. A reply is Claude's
// text up to its next tool call or the end of its turn; a result entry ends a
// turn. A tool is stopped when its turn ended or its process exited before its result.
export type Entry =
  | { kind: "prompt"; input: Prompt }
  | { kind: "reply"; text: string }
  | { kind: "reasoning"; text: string }
  | { kind: "tool"; id: string; name: string; input: Record<string, unknown>; result?: ToolResult; stopped?: boolean }
  | { kind: "result"; result: ToolResult }
  | { kind: "exited"; error: string };

export type Approval = { id: string; request: ApprovalRequest };

export type SessionSnapshot = {
  settings: Settings;
  entries: Entry[];
  approvals: Approval[];
  queued: Prompt[];
  // usage is the windows of the session's latest usage update, shortest first.
  usage?: UsageWindow[];
};

export const settingsOf = (p: Settings): Settings => ({
  model: p.model,
  effort: p.effort,
  permission_mode: p.permission_mode,
});

function stopTools(session: SessionSnapshot) {
  for (const e of session.entries) if (e.kind === "tool" && !e.result) e.stopped = true;
}

export function apply(session: SessionSnapshot, u: Update) {
  switch (u.type) {
    case "prompt":
      session.entries.push({ kind: "prompt", input: u });
      session.queued = session.queued.filter((p) => p.message_id !== u.message_id);
      break;
    case "prompt_queued":
      session.queued.push(u);
      break;
    case "prompt_cancelled":
      session.queued = session.queued.filter((p) => p.message_id !== u.message_id);
      break;
    case "text_delta": {
      const last = session.entries.at(-1);
      if (last?.kind === "reply") last.text += u.text;
      else session.entries.push({ kind: "reply", text: u.text });
      break;
    }
    case "reasoning_delta": {
      const last = session.entries.at(-1);
      if (last?.kind === "reasoning") last.text += u.text;
      else session.entries.push({ kind: "reasoning", text: u.text });
      break;
    }
    case "tool_use":
      session.entries.push({ kind: "tool", id: u.tool_use_id, name: u.tool_name, input: u.input });
      break;
    case "tool_result": {
      const tool = session.entries.findLast((e) => e.kind === "tool" && e.id === u.tool_use_id);
      if (tool?.kind === "tool") tool.result = u.result;
      break;
    }
    case "result":
      session.entries.push({ kind: "result", result: u.result });
      stopTools(session);
      break;
    case "parked":
      stopTools(session);
      break;
    case "approval":
      session.approvals.push({ id: u.request_id, request: u.request });
      break;
    case "approval_answered":
    case "approval_cancelled": {
      const i = session.approvals.findIndex((a) => a.id === u.request_id);
      if (i >= 0) session.approvals.splice(i, 1);
      break;
    }
    case "exited":
      session.entries.push({ kind: "exited", error: u.error });
      stopTools(session);
      break;
    case "usage":
      session.usage = u.usage;
      break;
  }
}

// snapshotOf builds a session's snapshot from its session/get result.
export function snapshotOf(result: Methods["session/get"][1]): SessionSnapshot {
  const snapshot: SessionSnapshot = {
    settings: settingsOf(result),
    entries: [],
    approvals: [],
    queued: [],
  };
  for (const u of result.history) apply(snapshot, u);
  snapshot.approvals = result.approvals.map((a) => ({ id: a.request_id, request: a.request }));
  snapshot.queued = result.queued;
  return snapshot;
}
