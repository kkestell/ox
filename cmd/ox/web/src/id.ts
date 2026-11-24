const alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz";

// encodeID writes bytes in Bitcoin's base58: each leading zero byte is a "1",
// and the rest is the big-endian number in base 58.
export function encodeID(bytes: Uint8Array): string {
  let zeros = 0;
  while (zeros < bytes.length && bytes[zeros] === 0) zeros++;
  let n = 0n;
  for (const b of bytes) n = (n << 8n) | BigInt(b);
  let digits = "";
  for (; n > 0n; n /= 58n) digits = alphabet[Number(n % 58n)] + digits;
  return "1".repeat(zeros) + digits;
}

// newID is 64 random bits in base58, the form oxd takes for a session or a
// message the page names.
export const newID = () => encodeID(crypto.getRandomValues(new Uint8Array(8)));
