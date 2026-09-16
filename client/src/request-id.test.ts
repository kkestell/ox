import { describe, expect, test } from "bun:test";

import { newRequestID } from "./request-id.ts";

describe("browser request IDs", () => {
  test("are unique without Web Crypto", () => {
    const first = newRequestID();
    const second = newRequestID();

    expect(first).not.toBe(second);
    expect(first.length).toBeLessThanOrEqual(128);
    expect(second.length).toBeLessThanOrEqual(128);
  });
});
