import assert from "node:assert/strict";
import { test } from "node:test";
import { encodeID, newID } from "./src/id.ts";

test("IDs are Bitcoin's base58 of 8 bytes", () => {
  const vectors = {
    "0000000000000000": "11111111",
    "0000000000000001": "11111112",
    "00ff000000000000": "1AfUXwLfh7d",
    "0123456789abcdef": "C3CPq7c8PY",
    ffffffffffffffff: "jpXCZedGfVQ",
  };
  for (const [hex, want] of Object.entries(vectors)) {
    assert.equal(encodeID(Uint8Array.from(Buffer.from(hex, "hex"))), want);
  }
  assert.match(newID(), /^[1-9A-HJ-NP-Za-km-z]{8,11}$/);
});
