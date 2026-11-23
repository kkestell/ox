import assert from "node:assert/strict";
import { test } from "node:test";
import { toolLabel, toolSubject } from "./src/toolInput.ts";

test("Ox's tools show their names in PascalCase and other tools show as they arrive", () => {
  for (const [name, label] of [
    ["mcp__ox__list_models", "ListModels"],
    ["mcp__ox__list_sessions", "ListSessions"],
    ["mcp__ox__send_message", "SendMessage"],
    ["mcp__ox__read_session", "ReadSession"],
    ["mcp__ox__create_session", "CreateSession"],
    ["Bash", "Bash"],
    ["mcp__context7__query-docs", "mcp__context7__query-docs"],
  ]) {
    assert.equal(toolLabel(name), label);
  }
});

test("a call is summarized by the field that says what it does", () => {
  assert.equal(toolSubject("mcp__ox__send_message", { to: "abc", text: "hello there" }), "hello there");
  assert.equal(toolSubject("mcp__ox__create_session", { name: "n", title: "Review: F2", message: "go" }), "Review: F2");
  assert.equal(toolSubject("mcp__ox__read_session", { to: "abc", cursor: 3 }), "abc");
  assert.equal(toolSubject("Bash", { command: "ls", description: "list" }), "ls");
});

test("a call with an empty input has no summary", () => {
  assert.equal(toolSubject("mcp__ox__list_sessions", {}), "");
  assert.equal(toolSubject("Bash", {}), "");
});
