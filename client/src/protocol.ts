import { z } from "zod";

const requestID = z.string().min(1).max(128);

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
): Snapshot {
  return {
    type: "snapshot",
    revision: 0,
    connection: { status: workspace.status === "ready" ? "ready" : "unavailable" },
    workspace,
    authentication,
  };
}
