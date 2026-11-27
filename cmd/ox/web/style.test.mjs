// Checks that src/style.css takes its design values from the tokens in its
// :root, and keeps one rule per selector.
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { test } from "node:test";

const css = readFileSync(new URL("src/style.css", import.meta.url), "utf8");

// Custom properties the page's markup sets inline, or Shiki sets on its spans.
const inline = new Set([
  "--depth",
  "--rise",
  "--tree",
  "--columns",
  "--digits",
  "--p",
  "--shiki-light-font-style",
  "--shiki-light-font-weight",
]);

// parse returns the stylesheet's rules: each one's selector, the at-rules it
// sits in, and its declarations.
function parse(text) {
  const source = text.replace(/\/\*[\s\S]*?\*\//g, "");
  const rules = [];
  const contexts = [];
  let buffer = "";
  let quote = "";
  let depth = 0;
  for (const char of source) {
    if (quote) {
      buffer += char;
      if (char === quote) quote = "";
      continue;
    }
    if (char === '"' || char === "'") quote = char;
    else if (char === "(") depth++;
    else if (char === ")") depth--;
    else if (depth === 0 && char === "{") {
      const prelude = buffer.trim();
      buffer = "";
      if (prelude.startsWith("@")) {
        contexts.push({ prelude, rule: undefined });
      } else {
        const rule = {
          selector: prelude.replace(/\s+/g, " "),
          context: contexts.map((c) => c.prelude).join(" "),
          body: "",
        };
        contexts.push({ prelude, rule });
      }
      continue;
    } else if (depth === 0 && char === "}") {
      const closed = contexts.pop();
      if (closed?.rule) {
        closed.rule.declarations = declarations(buffer);
        rules.push(closed.rule);
      }
      buffer = "";
      continue;
    } else if (depth === 0 && char === ";" && !contexts.at(-1)?.rule) {
      buffer = "";
      continue;
    }
    buffer += char;
  }
  return rules;
}

// declarations splits a rule's body at semicolons outside strings and
// parentheses.
function declarations(body) {
  const parts = [];
  let current = "";
  let quote = "";
  let depth = 0;
  for (const char of body) {
    if (quote) {
      if (char === quote) quote = "";
    } else if (char === '"' || char === "'") quote = char;
    else if (char === "(") depth++;
    else if (char === ")") depth--;
    else if (char === ";" && depth === 0) {
      parts.push(current);
      current = "";
      continue;
    }
    current += char;
  }
  parts.push(current);
  return parts
    .map((part) => part.trim())
    .filter(Boolean)
    .map((part) => {
      const colon = part.indexOf(":");
      return { property: part.slice(0, colon).trim(), value: part.slice(colon + 1).trim() };
    });
}

const rules = parse(css);
const isRoot = (rule) => rule.selector === ":root";
const where = (rule, property) => `${rule.context ? `${rule.context} ` : ""}${rule.selector} { ${property} }`;

// bare strips a value of what the literal rule ignores: url()s, strings, and
// custom property names.
const bare = (value) =>
  value
    .replace(/url\((?:"[^"]*"|'[^']*'|[^)]*)\)/g, "")
    .replace(/"[^"]*"|'[^']*'/g, "")
    .replace(/--[\w-]+/g, "");

const colorFunctions = /\b(?:rgba?|hsla?|hwb|lab|lch|oklab|oklch|color|color-mix|light-dark)\(/i;
const colorWords = /(?<![\w-])(?:white|black)(?![\w-])/i;
const hex = /#[0-9a-f]{3,8}(?![\w-])/i;
const number = /(?<![\w.-])(-?)(\d*\.?\d+)([a-z%]*)/gi;
const unitlessOne = new Set(["flex", "flex-grow", "flex-shrink", "opacity"]);

// insideCalc says whether the position at index in value lies within a calc().
function insideCalc(value, index) {
  const open = [];
  for (let i = 0; i < index; i++) {
    if (value[i] === "(") open.push(/calc$/i.test(value.slice(0, i)));
    else if (value[i] === ")") open.pop();
  }
  return open.includes(true);
}

// allowed says whether a number in a declaration is one of the geometric
// literals the token rule permits.
function allowed(property, value, match) {
  const [text, sign, digits, unit] = match;
  const n = Number(digits);
  if (n === 0) return true;
  if (unit === "%" && (n === 50 || n === 100)) return true;
  if (unit.toLowerCase() === "deg") return true;
  if (unit === "fr" && n === 1 && !sign) return true;
  if (unit === "" && n === 1 && !sign && unitlessOne.has(property)) return true;
  if (unit === "" && Number.isInteger(n) && /^\d+$/.test(digits) && insideCalc(value, match.index)) {
    const before = value.slice(0, match.index).trimEnd();
    const after = value.slice(match.index + text.length).trimStart();
    if (/[*/]$/.test(before) || after.startsWith("*") || after.startsWith("/")) return true;
  }
  return false;
}

test("rules outside :root take colors, lengths, and other values from tokens", () => {
  const found = [];
  for (const rule of rules) {
    if (isRoot(rule)) continue;
    for (const { property, value } of rule.declarations) {
      const text = bare(value);
      for (const pattern of [hex, colorFunctions, colorWords]) {
        const hit = text.match(pattern);
        if (hit) found.push(`${where(rule, property)}: ${hit[0]}`);
      }
      for (const match of text.matchAll(number)) {
        if (!allowed(property, text, match)) found.push(`${where(rule, property)}: ${match[0]}`);
      }
    }
  }
  assert.deepEqual(found, []);
});

test("no declaration is !important", () => {
  const found = rules.flatMap((rule) =>
    rule.declarations
      .filter((d) => /!\s*important/i.test(d.value))
      .map((d) => `${where(rule, d.property)}: ${d.value}`),
  );
  assert.deepEqual(found, []);
});

test("each selector has one rule in each at-rule context", () => {
  const seen = new Set();
  const found = [];
  for (const rule of rules) {
    const key = `${rule.context}\n${rule.selector}`;
    if (seen.has(key)) found.push(`${rule.context ? `${rule.context} ` : ""}${rule.selector}`);
    seen.add(key);
  }
  assert.deepEqual(found, []);
});

const defined = new Set(
  rules.flatMap((rule) => rule.declarations.map((d) => d.property).filter((p) => p.startsWith("--"))),
);
const reads = (value) => [...value.matchAll(/var\(\s*(--[\w-]+)/g)].map((m) => m[1]);

test("every var() names a custom property the stylesheet or the markup defines", () => {
  const found = rules.flatMap((rule) =>
    rule.declarations.flatMap((d) =>
      reads(d.value)
        .filter((name) => !defined.has(name) && !inline.has(name))
        .map((name) => `${where(rule, d.property)}: var(${name})`),
    ),
  );
  assert.deepEqual(found, []);
});

test("every :root token is read", () => {
  const read = new Set(rules.flatMap((rule) => rule.declarations.flatMap((d) => reads(d.value))));
  const found = rules
    .filter(isRoot)
    .flatMap((rule) =>
      rule.declarations
        .filter((d) => d.property.startsWith("--") && !read.has(d.property))
        .map((d) => where(rule, d.property)),
    );
  assert.deepEqual(found, []);
});
