import { z } from "zod";

const requestID = z.string().min(1).max(128);
const sessionID = z.string().min(1).max(512);

/** The most sessions a snapshot can carry, and therefore the most the host pages in. */
export const maximumSessions = 500;

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
            type: z.string().min(1),
            currentValue: z.union([z.string(), z.boolean()]),
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

export const browserCommandSchema = z.discriminatedUnion("type", [
  z
    .object({
      type: z.literal("ping"),
      requestId: requestID,
    })
    .strict(),
  z
    .object({
      type: z.literal("authenticate"),
      requestId: requestID,
      methodId: z.string().min(1).max(128),
    })
    .strict(),
  z
    .object({
      type: z.literal("login"),
      requestId: requestID,
      methodId: z.string().min(1).max(128),
      credential: z.string().trim().min(1).max(4096),
    })
    .strict(),
  z
    .object({
      type: z.literal("logout"),
      requestId: requestID,
    })
    .strict(),
  z.object({ type: z.literal("new-session"), requestId: requestID }).strict(),
  z.object({ type: z.literal("refresh-sessions"), requestId: requestID }).strict(),
  z.object({ type: z.literal("next-session-page"), requestId: requestID }).strict(),
  z
    .object({ type: z.literal("load-session"), requestId: requestID, sessionId: sessionID })
    .strict(),
  z
    .object({ type: z.literal("resume-session"), requestId: requestID, sessionId: sessionID })
    .strict(),
  z
    .object({ type: z.literal("close-session"), requestId: requestID, sessionId: sessionID })
    .strict(),
  z
    .object({ type: z.literal("delete-session"), requestId: requestID, sessionId: sessionID })
    .strict(),
]);

export type BrowserCommand = z.infer<typeof browserCommandSchema>;

export const snapshotSchema = z
  .object({
    type: z.literal("snapshot"),
    revision: z.number().int().nonnegative(),
    connection: z.object({
      status: z.enum(["ready", "unavailable"]),
    }),
    workspace: z
      .object({
        status: z.enum(["starting", "ready", "unavailable", "stopped"]),
        diagnostics: z.array(z.string()).max(16),
      })
      .strict(),
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
          .object({ id: z.string().min(1), transcript: sessionTranscriptSchema })
          .strict()
          .optional(),
        nextCursor: z.string().min(1).optional(),
        selectedId: z.string().min(1).optional(),
        values: z
          .array(
            z
              .object({
                id: z.string().min(1),
                status: z.enum(["inactive", "loading", "active"]),
                title: z.string().min(1).optional(),
                updatedAt: z.string().min(1).optional(),
              })
              .strict(),
          )
          .max(maximumSessions),
      })
      .strict(),
  })
  .strict();

export type Snapshot = z.infer<typeof snapshotSchema>;

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
  workspace: Snapshot["workspace"],
  authentication: Snapshot["authentication"] = {
    status: "unavailable",
    methods: [],
    logoutAvailable: false,
  },
  sessions: Snapshot["sessions"] = { values: [] },
): Snapshot {
  return {
    type: "snapshot",
    revision: 0,
    connection: { status: workspace.status === "ready" ? "ready" : "unavailable" },
    workspace,
    authentication,
    sessions,
  };
}
