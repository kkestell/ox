import { z } from "zod";

const requestID = z.string().min(1).max(128);

export const browserCommandSchema = z.discriminatedUnion("type", [
  z
    .object({
      type: z.literal("ping"),
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

export function initialSnapshot(): Snapshot {
  return {
    type: "snapshot",
    revision: 0,
    connection: { status: "ready" },
  };
}
