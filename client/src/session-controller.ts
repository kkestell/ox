import type * as acp from "@agentclientprotocol/sdk";

import type { SessionTranscript, ToolTranscriptContent, TranscriptContent, TranscriptEntry } from "./protocol.ts";

type MessageKind = "user" | "agent" | "thought";

// This controller is the single host-owned projection for both live updates and
// session/load replay. It deliberately keeps ACP metadata and raw tool values
// at the ACP boundary rather than copying them into browser snapshots.
export class SessionController {
  #configuration: SessionTranscript["configuration"] = [];
  #entries: TranscriptEntry[] = [];
  #indexes = new Map<string, number>();
  #nextLocalID = 1;
  #plan: SessionTranscript["plan"] = [];
  #usage: SessionTranscript["usage"];

  constructor(readonly id: string) {}

  accept(update: acp.SessionUpdate | unknown): void {
    const value = record(update);
    const kind = string(value?.sessionUpdate);
    switch (kind) {
      case "user_message_chunk":
        this.appendMessage("user", value);
        return;
      case "agent_message_chunk":
        this.appendMessage("agent", value);
        return;
      case "agent_thought_chunk":
        this.appendMessage("thought", value);
        return;
      case "tool_call":
      case "tool_call_update":
        this.mergeTool(value);
        return;
      case "plan":
        this.replacePlan(value);
        return;
      case "usage_update":
        this.replaceUsage(value);
        return;
      case "config_option_update":
        this.replaceConfiguration(value?.configOptions);
        return;
      default:
        this.appendUnknown(`Unknown session update${kind ? `: ${kind}` : ""}`);
    }
  }

  // Ox exposes only select options. An option the browser could not render as a
  // faithful select—another type, or a current value missing from its
  // choices—is dropped rather than projected into a misleading control.
  replaceConfiguration(configuration: readonly acp.SessionConfigOption[] | unknown): void {
    if (!Array.isArray(configuration)) return;
    this.#configuration = configuration.flatMap((option) => {
      const value = record(option);
      const id = string(value?.id);
      const name = string(value?.name);
      const currentValue = value?.currentValue;
      if (!id || !name || value?.type !== "select" || typeof currentValue !== "string") return [];
      const options = choices(value.options);
      if (!options.some((choice) => choice.value === currentValue)) return [];
      const description = string(value.description);
      const category = string(value.category);
      return [
        {
          ...(description ? { description } : {}),
          ...(category ? { category } : {}),
          currentValue,
          id,
          name,
          options,
          type: "select" as const,
        },
      ];
    });
  }

  get transcript(): SessionTranscript {
    return {
      configuration: this.#configuration.map((option) => ({ ...option })),
      entries: this.#entries.map(copyEntry),
      plan: this.#plan.map((entry) => ({ ...entry })),
      ...(this.#usage === undefined
        ? {}
        : { usage: { ...this.#usage, ...(this.#usage.cost === undefined ? {} : { cost: { ...this.#usage.cost } }) } }),
    };
  }

  private appendMessage(kind: MessageKind, update: Record<string, unknown> | undefined): void {
    const content = contentBlock(update?.content);
    if (!content) {
      this.appendUnknown(`Unknown ${kind} content`);
      return;
    }
    const messageID = string(update?.messageId);
    const key = messageID ? `message:${kind}:${messageID}` : undefined;
    if (key === undefined) {
      this.append({ content: [content], id: this.localID(kind), kind });
      return;
    }
    const index = this.#indexes.get(key);
    if (index === undefined) {
      this.append({ content: [content], id: key, kind }, key);
      return;
    }
    const current = this.#entries[index];
    if (!current || current.kind !== kind) {
      this.appendUnknown(`Incompatible ${kind} message update`);
      return;
    }
    this.#entries[index] = { ...current, content: [...current.content, content] };
  }

  private mergeTool(update: Record<string, unknown> | undefined): void {
    const toolCallID = string(update?.toolCallId);
    if (!toolCallID) {
      this.appendUnknown("Tool update without a tool-call identity");
      return;
    }
    const key = `tool:${toolCallID}`;
    const index = this.#indexes.get(key);
    const current = index === undefined ? undefined : this.#entries[index];
    const prior = current?.kind === "tool" ? current : emptyTool(key);
    const next: Extract<TranscriptEntry, { kind: "tool" }> = {
      ...prior,
      ...(string(update?.title) ? { title: string(update?.title) } : {}),
      ...(string(update?.name) ? { name: string(update?.name) } : {}),
      ...(string(update?.kind) ? { toolKind: string(update?.kind) } : {}),
      ...(string(update?.status) ? { status: string(update?.status) } : {}),
      ...(Array.isArray(update?.content) ? { content: update.content.map(toolContent) } : {}),
      ...(Array.isArray(update?.locations) ? { locations: update.locations.flatMap(location) } : {}),
    };
    if (index === undefined) {
      this.append(next, key);
      return;
    }
    this.#entries[index] = next;
  }

  private replacePlan(update: Record<string, unknown> | undefined): void {
    if (!Array.isArray(update?.entries)) {
      this.appendUnknown("Unknown plan update");
      return;
    }
    this.#plan = update.entries.flatMap((entry) => {
      const value = record(entry);
      const content = string(value?.content);
      const priority = string(value?.priority);
      const status = string(value?.status);
      return content && priority && status ? [{ content, priority, status }] : [];
    });
  }

  private replaceUsage(update: Record<string, unknown> | undefined): void {
    const used = update?.used;
    const size = update?.size;
    if (!number(used) || !number(size) || size <= 0) {
      this.appendUnknown("Unknown usage update");
      return;
    }
    const cost = record(update?.cost);
    const amount = cost?.amount;
    const currency = string(cost?.currency);
    this.#usage = {
      ...(typeof amount === "number" && Number.isFinite(amount) && currency ? { cost: { amount, currency } } : {}),
      size,
      used,
    };
  }

  private appendUnknown(label: string): void {
    this.append({ id: this.localID("unknown"), kind: "unknown", label });
  }

  private append(entry: TranscriptEntry, key?: string): void {
    this.#entries.push(entry);
    if (key) this.#indexes.set(key, this.#entries.length - 1);
  }

  private localID(kind: string): string {
    return `${kind}:${this.#nextLocalID++}`;
  }
}

function emptyTool(id: string): Extract<TranscriptEntry, { kind: "tool" }> {
  return { content: [], id, kind: "tool", locations: [], title: "Tool" };
}

function contentBlock(value: unknown): TranscriptContent | undefined {
  const content = record(value);
  const type = string(content?.type);
  switch (type) {
    case "text": {
      const text = content?.text;
      return typeof text === "string" ? { text, type } : undefined;
    }
    case "image": {
      const data = content?.data;
      const mimeType = string(content?.mimeType);
      if (typeof data !== "string" || !mimeType) return undefined;
      const uri = string(content?.uri);
      return { data, mimeType, ...(uri ? { uri } : {}), type };
    }
    case "audio": {
      const data = content?.data;
      const mimeType = string(content?.mimeType);
      return typeof data === "string" && mimeType ? { data, mimeType, type } : undefined;
    }
    case "resource_link": {
      const name = string(content?.name);
      const uri = string(content?.uri);
      if (!name || !uri) return undefined;
      const size = content?.size;
      const description = string(content?.description);
      const mimeType = string(content?.mimeType);
      const title = string(content?.title);
      return {
        ...(description ? { description } : {}),
        ...(mimeType ? { mimeType } : {}),
        name,
        ...(typeof size === "number" && Number.isFinite(size) && size >= 0 ? { size } : {}),
        ...(title ? { title } : {}),
        type,
        uri,
      };
    }
    case "resource": {
      const resource = record(content?.resource);
      const uri = string(resource?.uri);
      if (!uri) return undefined;
      const text = resource?.text;
      const blob = resource?.blob;
      if (typeof text !== "string" && typeof blob !== "string") return undefined;
      const mimeType = string(resource?.mimeType);
      return {
        ...(mimeType ? { mimeType } : {}),
        ...(typeof text === "string" ? { text } : { blob: blob as string }),
        type,
        uri,
      };
    }
    default:
      return { label: `Unknown content${type ? `: ${type}` : ""}`, type: "unknown" };
  }
}

function toolContent(value: unknown): ToolTranscriptContent {
  const content = record(value);
  const type = string(content?.type);
  if (type === "content") {
    return { content: contentBlock(content?.content) ?? { label: "Unknown content", type: "unknown" }, type };
  }
  if (type === "diff") {
    const path = string(content?.path);
    const newText = content?.newText;
    if (path && typeof newText === "string") {
      const oldText = content?.oldText;
      return { ...(typeof oldText === "string" ? { oldText } : {}), newText, path, type };
    }
  }
  if (type === "terminal") {
    const terminalId = string(content?.terminalId);
    if (terminalId) return { terminalId, type };
  }
  return { label: `Unknown tool content${type ? `: ${type}` : ""}`, type: "unknown" };
}

function location(value: unknown): Array<{ path: string; line?: number }> {
  const item = record(value);
  const path = string(item?.path);
  const line = item?.line;
  if (!path || (typeof line !== "undefined" && (typeof line !== "number" || !Number.isInteger(line) || line < 0))) return [];
  return [{ ...(typeof line === "number" ? { line } : {}), path }];
}

function copyEntry(entry: TranscriptEntry): TranscriptEntry {
  if (entry.kind === "tool") {
    return {
      ...entry,
      content: entry.content.map((content) => ({ ...content })),
      locations: entry.locations.map((location) => ({ ...location })),
    };
  }
  return entry.kind === "unknown" ? { ...entry } : { ...entry, content: entry.content.map((content) => ({ ...content })) };
}

function choices(value: unknown): SessionTranscript["configuration"][number]["options"] {
  if (!Array.isArray(value)) return [];
  return value.flatMap((candidate) => {
    const choice = record(candidate);
    const name = string(choice?.name);
    const choiceValue = string(choice?.value);
    if (!name || !choiceValue) return [];
    const description = string(choice?.description);
    return [{ ...(description ? { description } : {}), name, value: choiceValue }];
  });
}

function record(value: unknown): Record<string, unknown> | undefined {
  return value !== null && typeof value === "object" && !Array.isArray(value) ? (value as Record<string, unknown>) : undefined;
}

function string(value: unknown): string | undefined {
  return typeof value === "string" && value.length > 0 ? value : undefined;
}

function number(value: unknown): value is number {
  return typeof value === "number" && Number.isFinite(value) && value >= 0;
}
