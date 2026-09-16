import type * as acp from "@agentclientprotocol/sdk";

import { parsePendingInteraction, type FormField, type FormValue, type PendingInteraction, type PromptContentBlock, type SessionTranscript, type ToolTranscriptContent, type TranscriptContent, type TranscriptEntry } from "./protocol.ts";

type MessageKind = "user" | "agent" | "thought";

const cacheHitRateMetadataKey = "kkestell.ox/cacheHitRate";
const toolDisplayArgumentsMetadataKey = "kkestell.ox/toolDisplayArguments";
const toolDisplayNameMetadataKey = "kkestell.ox/toolDisplayName";

type PendingResolver = {
  changed: () => void;
  detach: () => void;
  resolve: (result: acp.RequestPermissionResponse | acp.CreateElicitationResponse) => void;
};

// This controller is the single host-owned projection for both live updates and
// session/load replay. It deliberately keeps ACP metadata and raw tool values
// at the ACP boundary rather than copying them into browser snapshots.
export class SessionController {
  #configuration: SessionTranscript["configuration"] = [];
  #entries: TranscriptEntry[] = [];
  #indexes = new Map<string, number>();
  #nextLocalID = 1;
  #nextInteractionID = 1;
  #plan: SessionTranscript["plan"] = [];
  #pending = new Map<string, PendingInteraction & PendingResolver>();
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

  get awaiting(): boolean {
    return this.#pending.size > 0;
  }

  appendPrompt(prompt: PromptContentBlock[]): void {
    this.append({
      content: prompt.map(promptTranscriptContent),
      id: this.localID("user"),
      kind: "user",
    });
  }

  get interactions(): PendingInteraction[] {
    return [...this.#pending.values()].map(copyInteraction);
  }

  requestPermission(request: acp.RequestPermissionRequest, signal: AbortSignal, changed: () => void): Promise<acp.RequestPermissionResponse> {
    const tool = request.toolCall;
    const presentation = toolPresentation(tool);
    const options = request.options.flatMap((option) => {
      if (!option.optionId || !option.name || !isPermissionKind(option.kind)) return [];
      return [{ id: option.optionId, kind: option.kind, name: option.name }];
    });
    if (!tool.toolCallId || options.length !== request.options.length || options.length === 0) {
      return Promise.resolve({ outcome: { outcome: "cancelled" } });
    }
    const interaction = parsePendingInteraction({
      id: this.interactionID(),
      kind: "permission",
      options,
      tool: {
        id: tool.toolCallId,
        title: presentation.name ?? tool.title,
        ...(presentation.arguments ? { arguments: presentation.arguments } : {}),
        ...(tool.name ? { name: tool.name } : {}),
        ...(tool.kind ? { toolKind: tool.kind } : {}),
      },
    });
    if (!interaction || interaction.kind !== "permission") {
      return Promise.resolve({ outcome: { outcome: "cancelled" } });
    }
    return this.waitFor(interaction, signal, changed) as Promise<acp.RequestPermissionResponse>;
  }

  requestElicitation(request: acp.CreateElicitationRequest, signal: AbortSignal, changed: () => void): Promise<acp.CreateElicitationResponse> {
    const interaction = formInteraction(this.interactionID(), request);
    if (!interaction) return Promise.resolve({ action: "cancel" });
    return this.waitFor(interaction, signal, changed) as Promise<acp.CreateElicitationResponse>;
  }

  resolvePermission(interactionID: string, optionID: string): void {
    const pending = this.#pending.get(interactionID);
    if (!pending) throw new Error("interaction is no longer pending");
    if (pending.kind !== "permission") throw new Error("interaction is not a permission request");
    if (!pending.options.some((option) => option.id === optionID)) throw new Error("permission option is not available");
    this.settle(interactionID, { outcome: { optionId: optionID, outcome: "selected" } });
  }

  resolveElicitation(interactionID: string, action: "accept" | "decline" | "cancel", content?: Record<string, FormValue>): void {
    const pending = this.#pending.get(interactionID);
    if (!pending) throw new Error("interaction is no longer pending");
    if (pending.kind !== "form") throw new Error("interaction is not a form elicitation");
    if (action !== "accept") {
      this.settle(interactionID, { action });
      return;
    }
    const checked = validateForm(pending.fields, content);
    this.settle(interactionID, { action, content: checked });
  }

  cancelInteractions(): void {
    for (const [id, interaction] of this.#pending) {
      this.settle(id, interaction.kind === "permission" ? { outcome: { outcome: "cancelled" } } : { action: "cancel" });
    }
  }

  private waitFor(
    interaction: PendingInteraction,
    signal: AbortSignal,
    changed: () => void,
  ): Promise<acp.RequestPermissionResponse | acp.CreateElicitationResponse> {
    if (this.#pending.size >= 64) {
      return Promise.resolve(interaction.kind === "permission" ? { outcome: { outcome: "cancelled" } } : { action: "cancel" });
    }
    return new Promise((resolve) => {
      const abort = () => this.settle(interaction.id, interaction.kind === "permission" ? { outcome: { outcome: "cancelled" } } : { action: "cancel" });
      const detach = () => signal.removeEventListener("abort", abort);
      this.#pending.set(interaction.id, { ...interaction, detach, resolve, changed });
      signal.addEventListener("abort", abort, { once: true });
      if (signal.aborted) abort();
      else changed();
    });
  }

  private settle(interactionID: string, result: acp.RequestPermissionResponse | acp.CreateElicitationResponse): void {
    const interaction = this.#pending.get(interactionID);
    if (!interaction) return;
    this.#pending.delete(interactionID);
    interaction.detach();
    interaction.changed();
    interaction.resolve(result);
  }

  private interactionID(): string {
    return `interaction-${this.#nextInteractionID++}`;
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
    this.#entries[index] = { ...current, content: appendMessageContent(current.content, content) };
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
    const presentation = toolPresentation(update);
    const next: Extract<TranscriptEntry, { kind: "tool" }> = {
      ...prior,
      ...(presentation.name ? { title: presentation.name } : string(update?.title) ? { title: string(update?.title) } : {}),
      ...(presentation.arguments ? { arguments: presentation.arguments } : {}),
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
    const cacheHitRate = record(update?._meta)?.[cacheHitRateMetadataKey];
    this.#usage = {
      ...(number(cacheHitRate) && cacheHitRate <= 1 ? { cacheHitRate } : {}),
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

function appendMessageContent(current: TranscriptContent[], next: TranscriptContent): TranscriptContent[] {
  const previous = current.at(-1);
  if (previous?.type !== "text" || next.type !== "text") {
    return [...current, next];
  }
  return [...current.slice(0, -1), { text: previous.text + next.text, type: "text" }];
}

function emptyTool(id: string): Extract<TranscriptEntry, { kind: "tool" }> {
  return { content: [], id, kind: "tool", locations: [], title: "Tool" };
}

function toolPresentation(value: unknown): { name?: string; arguments?: string } {
  const update = record(value);
  const meta = record(update?._meta);
  const name = string(meta?.[toolDisplayNameMetadataKey]);
  if (!name) return {};
  const displayArguments = string(meta?.[toolDisplayArgumentsMetadataKey]);
  return { ...(displayArguments ? { arguments: displayArguments } : {}), name };
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

function promptTranscriptContent(content: PromptContentBlock): TranscriptContent {
  if (content.type !== "resource") return { ...content };
  return { ...content.resource, type: "resource" };
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

function isPermissionKind(value: unknown): value is Extract<PendingInteraction, { kind: "permission" }> ["options"][number]["kind"] {
  return value === "allow_once" || value === "allow_always" || value === "reject_once" || value === "reject_always";
}

function copyInteraction(interaction: PendingInteraction): PendingInteraction {
  if (interaction.kind === "permission") {
    return { id: interaction.id, kind: "permission", options: interaction.options.map((option) => ({ ...option })), tool: { ...interaction.tool } };
  }
  return { id: interaction.id, kind: "form", message: interaction.message, ...(interaction.title === undefined ? {} : { title: interaction.title }), ...(interaction.description === undefined ? {} : { description: interaction.description }), fields: interaction.fields.map((field) => ({ ...field, ...(field.type === "multi-select" || field.type === "string" ? { choices: field.choices?.map((choice) => ({ ...choice })) } : {}) })) } as PendingInteraction;
}

function formInteraction(id: string, request: acp.CreateElicitationRequest): Extract<PendingInteraction, { kind: "form" }> | undefined {
  if (request.mode !== "form" || !("sessionId" in request) || !request.sessionId || !request.message) return undefined;
  const schema = record((request as { requestedSchema?: unknown }).requestedSchema);
  const properties = record(schema?.properties);
  if (!schema || schema.type !== undefined && schema.type !== "object" || !properties) return undefined;
  const entries = Object.entries(properties);
  if (entries.length === 0 || entries.length > 64 || Array.isArray(schema.required) && schema.required.length > entries.length) return undefined;
  const required = new Set(Array.isArray(schema.required) ? schema.required.filter((name): name is string => typeof name === "string") : []);
  const fields = entries.flatMap(([name, property]) => formField(name, property, required.has(name)));
  if (fields.length !== entries.length) return undefined;
  const title = string(schema.title);
  const description = string(schema.description);
  const interaction = parsePendingInteraction({ fields, id, kind: "form", message: request.message, ...(title ? { title } : {}), ...(description ? { description } : {}) });
  return interaction?.kind === "form" ? interaction : undefined;
}

function formField(name: string, raw: unknown, required: boolean): FormField[] {
  const property = record(raw);
  if (!property || !name || name.length > 256) return [];
  const title = string(property.title) ?? name;
  const description = string(property.description);
  const common = { name, label: title, ...(description ? { description } : {}), required };
  const minimum = finite(property.minimum);
  const maximum = finite(property.maximum);
  if (minimum !== undefined && maximum !== undefined && minimum > maximum) return [];
  if (property.type === "string") {
    if (property.oneOf !== undefined && property.enum !== undefined) return [];
    const choices = choicesFor(property.oneOf, property.enum);
    const minLength = integer(property.minLength);
    const maxLength = integer(property.maxLength);
    const defaultValue = typeof property.default === "string" ? property.default : undefined;
    const format = property.format === "email" || property.format === "uri" || property.format === "date" || property.format === "date-time" ? property.format : undefined;
    if (typeof property.pattern === "string") {
      try {
        new RegExp(property.pattern);
      } catch {
        return [];
      }
    }
    if (choices === undefined && (property.oneOf !== undefined || property.enum !== undefined) || minLength !== undefined && maxLength !== undefined && minLength > maxLength || defaultValue !== undefined && (minLength !== undefined && defaultValue.length < minLength || maxLength !== undefined && defaultValue.length > maxLength || typeof property.pattern === "string" && !new RegExp(property.pattern).test(defaultValue) || !validFormat(defaultValue, format) || choices !== undefined && !choices.some((choice) => choice.value === defaultValue))) return [];
    return [{ ...common, type: "string", ...(minLength === undefined ? {} : { minLength }), ...(maxLength === undefined ? {} : { maxLength }), ...(typeof property.pattern === "string" ? { pattern: property.pattern } : {}), ...(format === undefined ? {} : { format }), ...(defaultValue === undefined ? {} : { default: defaultValue }), ...(choices === undefined ? {} : { choices }) }];
  }
  if (property.type === "number" || property.type === "integer") {
    const defaultValue = finite(property.default);
    if (defaultValue !== undefined && (property.type === "integer" && !Number.isInteger(defaultValue) || minimum !== undefined && defaultValue < minimum || maximum !== undefined && defaultValue > maximum)) return [];
    return [{ ...common, type: property.type, ...(minimum === undefined ? {} : { minimum }), ...(maximum === undefined ? {} : { maximum }), ...(defaultValue === undefined ? {} : { default: defaultValue }) }];
  }
  if (property.type === "boolean") return [{ ...common, type: "boolean", ...(typeof property.default === "boolean" ? { default: property.default } : {}) }];
  if (property.type !== "array") return [];
  const items = record(property.items);
  if (items?.anyOf !== undefined && items.enum !== undefined) return [];
  const choices = items ? choicesFor(items.anyOf, items.enum) : undefined;
  const minItems = integer(property.minItems);
  const maxItems = integer(property.maxItems);
  const defaultValue = Array.isArray(property.default) && property.default.every((value) => typeof value === "string") ? property.default : undefined;
  if (!choices?.length || minItems !== undefined && maxItems !== undefined && minItems > maxItems || defaultValue !== undefined && (new Set(defaultValue).size !== defaultValue.length || defaultValue.some((value) => !choices.some((choice) => choice.value === value)) || minItems !== undefined && defaultValue.length < minItems || maxItems !== undefined && defaultValue.length > maxItems)) return [];
  return [{ ...common, type: "multi-select", choices, ...(minItems === undefined ? {} : { minItems }), ...(maxItems === undefined ? {} : { maxItems }), ...(defaultValue === undefined ? {} : { default: defaultValue }) }];
}

function choicesFor(titled: unknown, plain: unknown): Array<{ description?: string; label: string; value: string }> | undefined {
  if (Array.isArray(titled)) {
    if (titled.length === 0 || titled.length > 256) return undefined;
    const choices = titled.flatMap((item) => {
      const value = record(item);
      const label = string(value?.title);
      const id = string(value?.const);
      const description = string(value?.description);
      return label && id ? [{ ...(description ? { description } : {}), label, value: id }] : [];
    });
    return choices.length === titled.length && new Set(choices.map((choice) => choice.value)).size === choices.length ? choices : undefined;
  }
  if (!Array.isArray(plain) || plain.length === 0 || plain.length > 256 || !plain.every((value) => typeof value === "string" && value.length > 0)) return undefined;
  return new Set(plain).size === plain.length ? plain.map((value) => ({ label: value, value })) : undefined;
}

function validateForm(fields: readonly FormField[], supplied: Record<string, FormValue> | undefined): Record<string, FormValue> {
  const content = supplied ?? {};
  const expected = new Set(fields.map((field) => field.name));
  for (const name of Object.keys(content)) if (!expected.has(name)) throw new Error("form contains an unknown field");
  const result = Object.create(null) as Record<string, FormValue>;
  for (const field of fields) {
    const value = content[field.name];
    if (value === undefined) {
      if (field.required) throw new Error(`form field ${field.label} is required`);
      continue;
    }
    switch (field.type) {
      case "string":
        if (typeof value !== "string" || field.minLength !== undefined && value.length < field.minLength || field.maxLength !== undefined && value.length > field.maxLength || field.pattern !== undefined && !new RegExp(field.pattern).test(value) || !validFormat(value, field.format) || field.choices && !field.choices.some((choice) => choice.value === value)) throw new Error(`form field ${field.label} is invalid`);
        break;
      case "number":
      case "integer":
        if (typeof value !== "number" || !Number.isFinite(value) || field.type === "integer" && !Number.isInteger(value) || field.minimum !== undefined && value < field.minimum || field.maximum !== undefined && value > field.maximum) throw new Error(`form field ${field.label} is invalid`);
        break;
      case "boolean":
        if (typeof value !== "boolean") throw new Error(`form field ${field.label} is invalid`);
        break;
      case "multi-select":
        if (!Array.isArray(value) || value.some((item) => typeof item !== "string") || new Set(value).size !== value.length || value.some((item) => !field.choices.some((choice) => choice.value === item)) || field.minItems !== undefined && value.length < field.minItems || field.maxItems !== undefined && value.length > field.maxItems) throw new Error(`form field ${field.label} is invalid`);
        break;
    }
    result[field.name] = value;
  }
  return result;
}

function finite(value: unknown): number | undefined {
  return typeof value === "number" && Number.isFinite(value) ? value : undefined;
}

function integer(value: unknown): number | undefined {
  return typeof value === "number" && Number.isInteger(value) && value >= 0 ? value : undefined;
}

function validFormat(value: string, format: Extract<FormField, { type: "string" }> ["format"]): boolean {
  switch (format) {
    case undefined: return true;
    case "email": return /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(value);
    case "uri":
      try {
        new URL(value);
        return true;
      } catch {
        return false;
      }
    case "date": return validDate(value);
    case "date-time": return /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}(?::\d{2}(?:\.\d{1,3})?)?(?:Z|[+-]\d{2}:\d{2})?$/.test(value) && validDate(value.slice(0, 10)) && !Number.isNaN(Date.parse(value));
  }
}

function validDate(value: string): boolean {
  if (!/^\d{4}-\d{2}-\d{2}$/.test(value)) return false;
  const date = new Date(`${value}T00:00:00.000Z`);
  return !Number.isNaN(date.valueOf()) && date.toISOString().slice(0, 10) === value;
}
