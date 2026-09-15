import { z } from "zod";

const requestID = z.string().min(1).max(128);
const sessionID = z.string().min(1).max(512);
const workspaceID = z.string().uuid();
const workspaceStatus = z.enum(["starting", "ready", "unavailable", "stopped"]);

/** The most sessions a snapshot can carry, and therefore the most the host pages in. */
export const maximumSessions = 500;

/** The most conversations an unselected workspace's catalog entry carries. */
export const maximumRecentConversations = 10;

/** The most characters a browser-authored text or embedded text resource can carry. */
export const maximumPromptText = 1_000_000;

/** The most bytes a browser attachment can carry before base64 expansion. */
export const maximumAttachmentBytes = 6_000_000;

const boundedText = z.string().max(maximumPromptText);
const boundedData = z.string().max((maximumAttachmentBytes / 3) * 4);
const boundedURI = z.string().min(1).max(4_096);
const mimeType = z.string().min(1).max(256);
const mcpServerName = z.string().trim().min(1).max(512);
const mcpValue = z.string().max(16_384).refine((value) => !value.includes("\0"));
const mcpHeaderName = z
  .string()
  .min(1)
  .max(256)
  .refine((value) => /^[!#$%&'*+\-.^_`|~0-9A-Za-z]+$/.test(value));
const mcpHeaderValue = mcpValue.refine((value) => [...value].every((character) => character === "\t" || (character >= " " && character !== "\u007f")));
const mcpEnvironmentName = z
  .string()
  .min(1)
  .max(256)
  .refine((value) => !value.includes("=") && !value.includes("\0"));
const mcpHTTPURL = z
  .string()
  .url()
  .max(4_096)
  .refine(isValidMCPHTTPURL);

const mcpHeaders = z
  .array(z.object({ name: mcpHeaderName, value: mcpHeaderValue }).strict())
  .max(128)
  .superRefine((headers, context) => {
    const seen = new Set<string>();
    for (const [index, header] of headers.entries()) {
      const name = header.name.toLowerCase();
      if (seen.has(name)) {
        context.addIssue({ code: "custom", message: "header name must be unique", path: [index, "name"] });
      }
      seen.add(name);
    }
  });

const mcpEnvironment = z
  .array(z.object({ name: mcpEnvironmentName, value: mcpValue }).strict())
  .max(128)
  .superRefine((variables, context) => {
    const seen = new Set<string>();
    for (const [index, variable] of variables.entries()) {
      if (seen.has(variable.name)) {
        context.addIssue({ code: "custom", message: "environment name must be unique", path: [index, "name"] });
      }
      seen.add(variable.name);
    }
  });

const mcpServerSchema = z.discriminatedUnion("transport", [
  z
    .object({
      transport: z.literal("http"),
      name: mcpServerName,
      url: mcpHTTPURL,
      headers: mcpHeaders,
    })
    .strict(),
  z
    .object({
      transport: z.literal("stdio"),
      name: mcpServerName,
      command: z.string().startsWith("/").max(4_096),
      args: z.array(mcpValue).max(128),
      env: mcpEnvironment,
    })
    .strict(),
]);

export type MCPServer = z.infer<typeof mcpServerSchema>;
const mcpServers = z
  .array(mcpServerSchema)
  .max(64)
  .superRefine((servers, context) => {
    const seen = new Set<string>();
    for (const [index, server] of servers.entries()) {
      if (seen.has(server.name)) {
        context.addIssue({ code: "custom", message: "server name must be unique", path: [index, "name"] });
      }
      seen.add(server.name);
    }
  });

function isLocalMCPHost(host: string): boolean {
  const normalized = host.toLowerCase();
  return normalized === "localhost" || normalized.endsWith(".localhost") || normalized.startsWith("127.") || normalized === "[::1]";
}

function isValidMCPHTTPURL(value: string): boolean {
  try {
    const url = new URL(value);
    return !url.username && !url.password && (url.protocol === "https:" || (url.protocol === "http:" && isLocalMCPHost(url.hostname)));
  } catch {
    return false;
  }
}

const promptContentBlockSchema = z.discriminatedUnion("type", [
  z.object({ type: z.literal("text"), text: boundedText }).strict(),
  z
    .object({ type: z.literal("image"), data: boundedData, mimeType, uri: boundedURI.optional() })
    .strict(),
  z.object({ type: z.literal("audio"), data: boundedData, mimeType }).strict(),
  z
    .object({
      type: z.literal("resource_link"),
      description: boundedText.optional(),
      mimeType: mimeType.optional(),
      name: z.string().min(1).max(512),
      size: z.number().int().nonnegative().optional(),
      title: z.string().min(1).max(512).optional(),
      uri: boundedURI,
    })
    .strict(),
  z
    .object({
      type: z.literal("resource"),
      resource: z
        .object({
          mimeType: mimeType.optional(),
          text: boundedText.optional(),
          blob: boundedData.optional(),
          uri: boundedURI,
        })
        .strict()
        .refine((value) => (value.text === undefined) !== (value.blob === undefined), {
          message: "a resource must carry exactly one of text or blob",
        }),
    })
    .strict(),
]);

export type PromptContentBlock = z.infer<typeof promptContentBlockSchema>;

const promptCapabilitiesSchema = z
  .object({ audio: z.boolean(), embeddedContext: z.boolean(), image: z.boolean() })
  .strict();

export type PromptCapabilities = z.infer<typeof promptCapabilitiesSchema>;

const contentBlockSchema = z.discriminatedUnion("type", [
  z.object({ type: z.literal("text"), text: z.string() }).strict(),
  z
    .object({
      type: z.literal("image"),
      data: z.string(),
      mimeType: z.string().min(1),
      uri: z.string().min(1).optional(),
    })
    .strict(),
  z.object({ type: z.literal("audio"), data: z.string(), mimeType: z.string().min(1) }).strict(),
  z
    .object({
      type: z.literal("resource_link"),
      description: z.string().min(1).optional(),
      mimeType: z.string().min(1).optional(),
      name: z.string().min(1),
      size: z.number().nonnegative().optional(),
      title: z.string().min(1).optional(),
      uri: z.string().min(1),
    })
    .strict(),
  z
    .object({
      type: z.literal("resource"),
      mimeType: z.string().min(1).optional(),
      text: z.string().optional(),
      blob: z.string().optional(),
      uri: z.string().min(1),
    })
    .strict()
    .refine((value) => (value.text === undefined) !== (value.blob === undefined), {
      message: "a resource must carry exactly one of text or blob",
    }),
  z.object({ type: z.literal("unknown"), label: z.string().min(1) }).strict(),
]);

const toolContentSchema = z.discriminatedUnion("type", [
  z.object({ type: z.literal("content"), content: contentBlockSchema }).strict(),
  z
    .object({
      type: z.literal("diff"),
      path: z.string().min(1),
      oldText: z.string().optional(),
      newText: z.string(),
    })
    .strict(),
  z.object({ type: z.literal("terminal"), terminalId: z.string().min(1) }).strict(),
  z.object({ type: z.literal("unknown"), label: z.string().min(1) }).strict(),
]);

const transcriptEntrySchema = z.discriminatedUnion("kind", [
  z
    .object({
      id: z.string().min(1),
      kind: z.enum(["user", "agent", "thought"]),
      content: z.array(contentBlockSchema),
    })
    .strict(),
  z
    .object({
      id: z.string().min(1),
      kind: z.literal("tool"),
      title: z.string().min(1),
      arguments: z.string().min(1).optional(),
      name: z.string().min(1).optional(),
      toolKind: z.string().min(1).optional(),
      status: z.string().min(1).optional(),
      content: z.array(toolContentSchema),
      locations: z.array(z.object({ path: z.string().min(1), line: z.number().int().nonnegative().optional() }).strict()),
    })
    .strict(),
  z.object({ id: z.string().min(1), kind: z.literal("unknown"), label: z.string().min(1) }).strict(),
]);

const sessionTranscriptSchema = z
  .object({
    entries: z.array(transcriptEntrySchema),
    // This is an ephemeral presentation cue, not durable session state. The
    // host sets it only for a thought that was created by a live update.
    openReasoningID: z.string().min(1).optional(),
    plan: z.array(
      z
        .object({ content: z.string(), priority: z.string().min(1), status: z.string().min(1) })
        .strict(),
    ),
    usage: z
      .object({
        used: z.number().nonnegative(),
        size: z.number().positive(),
        cost: z.object({ amount: z.number(), currency: z.string().min(1) }).strict().optional(),
      })
      .strict()
      .optional(),
    configuration: z
      .array(
        z
          .object({
            id: z.string().min(1),
            name: z.string().min(1),
            description: z.string().min(1).optional(),
            category: z.string().min(1).optional(),
            type: z.literal("select"),
            currentValue: z.string(),
            options: z
              .array(
                z
                  .object({
                    description: z.string().min(1).optional(),
                    name: z.string().min(1),
                    value: z.string().min(1),
                  })
                  .strict(),
              )
              .max(256),
          })
          .strict(),
      )
      .max(128),
  })
  .strict();

export type SessionTranscript = z.infer<typeof sessionTranscriptSchema>;
export type TranscriptEntry = z.infer<typeof transcriptEntrySchema>;
export type TranscriptContent = z.infer<typeof contentBlockSchema>;
export type ToolTranscriptContent = z.infer<typeof toolContentSchema>;

const interactionID = z.string().min(1).max(128);
const optionID = z.string().min(1).max(256);
const interactionText = z.string().min(1).max(16_384);
const formValueSchema = z.union([z.string().max(maximumPromptText), z.number().finite(), z.boolean(), z.array(z.string().max(4_096)).max(256)]);

const formChoiceSchema = z.object({ description: boundedText.optional(), label: interactionText, value: z.string().min(1).max(4_096) }).strict();
const formFieldSchema = z.discriminatedUnion("type", [
  z.object({
    type: z.literal("string"), name: z.string().min(1).max(256), label: interactionText, description: boundedText.optional(), required: z.boolean(),
    minLength: z.number().int().nonnegative().optional(), maxLength: z.number().int().nonnegative().optional(), pattern: z.string().max(4_096).optional(),
    format: z.enum(["email", "uri", "date", "date-time"]).optional(), default: z.string().max(maximumPromptText).optional(), choices: z.array(formChoiceSchema).min(1).max(256).optional(),
  }).strict(),
  z.object({
    type: z.enum(["number", "integer"]), name: z.string().min(1).max(256), label: interactionText, description: boundedText.optional(), required: z.boolean(),
    minimum: z.number().finite().optional(), maximum: z.number().finite().optional(), default: z.number().finite().optional(),
  }).strict(),
  z.object({ type: z.literal("boolean"), name: z.string().min(1).max(256), label: interactionText, description: boundedText.optional(), required: z.boolean(), default: z.boolean().optional() }).strict(),
  z.object({
    type: z.literal("multi-select"), name: z.string().min(1).max(256), label: interactionText, description: boundedText.optional(), required: z.boolean(),
    minItems: z.number().int().nonnegative().optional(), maxItems: z.number().int().nonnegative().optional(), default: z.array(z.string().min(1).max(4_096)).max(256).optional(), choices: z.array(formChoiceSchema).min(1).max(256),
  }).strict(),
]);

const pendingInteractionSchema = z.discriminatedUnion("kind", [
  z.object({
    id: interactionID,
    kind: z.literal("permission"),
    tool: z.object({ id: z.string().min(1).max(512), title: interactionText, arguments: interactionText.optional(), name: z.string().min(1).max(512).optional(), toolKind: z.string().min(1).max(128).optional() }).strict(),
    options: z.array(z.object({ id: optionID, name: interactionText, kind: z.enum(["allow_once", "allow_always", "reject_once", "reject_always"]) }).strict()).min(1).max(16),
  }).strict(),
  z.object({
    id: interactionID,
    kind: z.literal("form"),
    message: interactionText,
    title: interactionText.optional(),
    description: boundedText.optional(),
    fields: z.array(formFieldSchema).min(1).max(64),
  }).strict(),
]);

export type PendingInteraction = z.infer<typeof pendingInteractionSchema>;
export type FormField = z.infer<typeof formFieldSchema>;
export type FormValue = z.infer<typeof formValueSchema>;

/** Returns only an interaction that is safe to include in a browser snapshot. */
export function parsePendingInteraction(value: unknown): PendingInteraction | undefined {
  const parsed = pendingInteractionSchema.safeParse(value);
  return parsed.success ? parsed.data : undefined;
}

export const browserCommandSchema = z.discriminatedUnion("type", [
  z
    .object({
      type: z.literal("ping"),
      requestId: requestID,
    })
    .strict(),
  z
    .object({
      type: z.literal("register-workspace"),
      requestId: requestID,
      path: z.string().startsWith("/").max(4_096),
    })
    .strict(),
  z.object({ type: z.literal("select-workspace"), requestId: requestID, workspaceId: workspaceID }).strict(),
  z.object({ type: z.literal("remove-workspace"), requestId: requestID, workspaceId: workspaceID }).strict(),
  z.object({ type: z.literal("restart-workspace"), requestId: requestID, workspaceId: workspaceID }).strict(),
  z
    .object({
      type: z.literal("authenticate"),
      requestId: requestID,
      workspaceId: workspaceID,
      methodId: z.string().min(1).max(128),
    })
    .strict(),
  z
    .object({
      type: z.literal("login"),
      requestId: requestID,
      workspaceId: workspaceID,
      methodId: z.string().min(1).max(128),
      credential: z.string().trim().min(1).max(4096),
    })
    .strict(),
  z
    .object({
      type: z.literal("logout"),
      requestId: requestID,
      workspaceId: workspaceID,
    })
    .strict(),
  z.object({ type: z.literal("new-conversation"), requestId: requestID, workspaceId: workspaceID }).strict(),
  z.object({ type: z.literal("refresh-history"), requestId: requestID, workspaceId: workspaceID }).strict(),
  z.object({ type: z.literal("next-history-page"), requestId: requestID, workspaceId: workspaceID }).strict(),
  z.object({ type: z.literal("open-conversation"), requestId: requestID, workspaceId: workspaceID, sessionId: sessionID }).strict(),
  z.object({ type: z.literal("close-conversation"), requestId: requestID, workspaceId: workspaceID, sessionId: sessionID }).strict(),
  z.object({ type: z.literal("delete-conversation"), requestId: requestID, workspaceId: workspaceID, sessionId: sessionID }).strict(),
  z.object({ type: z.literal("set-mcp-servers"), requestId: requestID, workspaceId: workspaceID, mcpServers }).strict(),
  z
    .object({
      type: z.literal("prompt"),
      requestId: requestID,
      workspaceId: workspaceID,
      sessionId: sessionID,
      prompt: z.array(promptContentBlockSchema).min(1).max(32),
    })
    .strict(),
  z.object({ type: z.literal("cancel-prompt"), requestId: requestID, workspaceId: workspaceID, sessionId: sessionID }).strict(),
  z
    .object({
      type: z.literal("set-config-option"),
      requestId: requestID,
      workspaceId: workspaceID,
      sessionId: sessionID,
      configId: z.string().min(1).max(128),
      value: z.string().min(1).max(512),
    })
    .strict(),
  z.object({ type: z.literal("resolve-permission"), requestId: requestID, workspaceId: workspaceID, sessionId: sessionID, interactionId: interactionID, optionId: optionID }).strict(),
  z.object({ type: z.literal("resolve-elicitation"), requestId: requestID, workspaceId: workspaceID, sessionId: sessionID, interactionId: interactionID, action: z.enum(["accept", "decline", "cancel"]), content: z.record(z.string().min(1).max(256), formValueSchema).refine((value) => Object.keys(value).length <= 64).optional() }).strict(),
]);

export type BrowserCommand = z.infer<typeof browserCommandSchema>;

const conversationSchema = z
  .object({
    id: z.string().min(1),
    status: z.enum(["inactive", "locked", "loading", "active"]),
    awaiting: z.boolean().optional(),
    title: z.string().min(1).optional(),
    updatedAt: z.string().min(1).optional(),
  })
  .strict();

export const snapshotSchema = z
  .object({
    type: z.literal("snapshot"),
    revision: z.number().int().nonnegative(),
    workspaces: z
      .object({
        selectedId: workspaceID.optional(),
        values: z
          .array(
            z
              .object({
                id: workspaceID,
                name: z.string().min(1).max(512),
                status: workspaceStatus,
                busy: z.boolean(),
                awaiting: z.boolean(),
                conversations: z.array(conversationSchema).max(maximumSessions),
              })
              .strict(),
          )
          .max(1_024),
      })
      .strict()
      .superRefine((workspaces, context) => {
        const ids = new Set(workspaces.values.map((workspace) => workspace.id));
        if (ids.size !== workspaces.values.length) {
          context.addIssue({ code: "custom", message: "workspace IDs must be unique", path: ["values"] });
        }
        const selectionRegistered =
          workspaces.selectedId === undefined ? workspaces.values.length === 0 : ids.has(workspaces.selectedId);
        if (!selectionRegistered) {
          context.addIssue({ code: "custom", message: "selected workspace must be registered", path: ["selectedId"] });
        }
      }),
    workspace: z
      .object({
        status: workspaceStatus,
        diagnostics: z.array(z.string()).max(16),
        mcpServerCount: z.number().int().nonnegative(),
        name: z.string().min(1).max(512),
        promptCapabilities: promptCapabilitiesSchema,
      })
      .strict()
      .optional(),
    authentication: z
      .object({
        status: z.enum(["required", "working", "authenticated", "unavailable"]),
        methods: z
          .array(
            z
              .object({
                id: z.string().min(1).max(128),
                type: z.enum(["agent", "terminal"]),
                name: z.string().min(1),
                description: z.string().min(1).optional(),
              })
              .strict(),
          )
          .max(16),
        logoutAvailable: z.boolean(),
        error: z.string().min(1).optional(),
      })
      .strict(),
    sessions: z
      .object({
        active: z
          .object({ id: z.string().min(1), busy: z.boolean(), transcript: sessionTranscriptSchema, interactions: z.array(pendingInteractionSchema).max(64) })
          .strict()
          .optional(),
        nextCursor: z.string().min(1).optional(),
        selectedId: z.string().min(1).optional(),
      })
      .strict(),
  })
  .strict();

export type Snapshot = z.infer<typeof snapshotSchema>;
export type Conversation = z.infer<typeof conversationSchema>;

const resultSchema = z.union([
  z
    .object({
      type: z.literal("result"),
      requestId: requestID,
      ok: z.literal(true),
      value: z.object({
        revision: z.number().int().nonnegative(),
      }),
    })
    .strict(),
  z
    .object({
      type: z.literal("result"),
      requestId: requestID,
      ok: z.literal(false),
      error: z.string().min(1),
    })
    .strict(),
]);

export const browserMessageSchema = z.union([snapshotSchema, resultSchema]);

export type BrowserMessage = z.infer<typeof browserMessageSchema>;

export function parseBrowserCommand(value: unknown):
  | { ok: true; value: BrowserCommand }
  | { ok: false; error: string } {
  const parsed = browserCommandSchema.safeParse(value);
  if (parsed.success) {
    return { ok: true, value: parsed.data };
  }
  return { ok: false, error: "invalid browser command" };
}

export function initialSnapshot(
  workspaces: Snapshot["workspaces"],
  workspace?: Snapshot["workspace"],
  authentication: Snapshot["authentication"] = {
    status: "unavailable",
    methods: [],
    logoutAvailable: false,
  },
  sessions: Snapshot["sessions"] = {},
): Snapshot {
  return {
    type: "snapshot",
    revision: 0,
    workspaces,
    ...(workspace === undefined ? {} : { workspace }),
    authentication,
    sessions,
  };
}
