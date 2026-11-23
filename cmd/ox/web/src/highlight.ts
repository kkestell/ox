import bash from "@shikijs/langs/bash";
import c from "@shikijs/langs/c";
import css from "@shikijs/langs/css";
import diff from "@shikijs/langs/diff";
import docker from "@shikijs/langs/docker";
import go from "@shikijs/langs/go";
import html from "@shikijs/langs/html";
import ini from "@shikijs/langs/ini";
import java from "@shikijs/langs/java";
import javascript from "@shikijs/langs/javascript";
import json from "@shikijs/langs/json";
import jsonc from "@shikijs/langs/jsonc";
import jsx from "@shikijs/langs/jsx";
import make from "@shikijs/langs/make";
import markdown from "@shikijs/langs/markdown";
import ocaml from "@shikijs/langs/ocaml";
import python from "@shikijs/langs/python";
import rust from "@shikijs/langs/rust";
import scss from "@shikijs/langs/scss";
import sql from "@shikijs/langs/sql";
import swift from "@shikijs/langs/swift";
import toml from "@shikijs/langs/toml";
import tsx from "@shikijs/langs/tsx";
import typescript from "@shikijs/langs/typescript";
import xml from "@shikijs/langs/xml";
import yaml from "@shikijs/langs/yaml";
import githubDark from "@shikijs/themes/github-dark-default";
import githubLight from "@shikijs/themes/github-light-default";
import { createHighlighterCoreSync, stringifyTokenStyle } from "shiki/core";
import { createJavaScriptRegexEngine } from "shiki/engine/javascript";

const languages = {
  bash,
  c,
  css,
  diff,
  docker,
  go,
  ini,
  java,
  javascript,
  jsx,
  json,
  jsonc,
  make,
  markdown,
  ocaml,
  python,
  rust,
  scss,
  sql,
  swift,
  typescript,
  tsx,
  xml,
  html,
  yaml,
  toml,
};
type Language = keyof typeof languages;

const highlighter = createHighlighterCoreSync({
  themes: [githubLight, githubDark],
  langs: Object.values(languages),
  engine: createJavaScriptRegexEngine(),
});

// Languages of whole base names.
const names: Record<string, Language> = {
  Dockerfile: "docker",
  Containerfile: "docker",
  Makefile: "make",
  makefile: "make",
  GNUmakefile: "make",
};

// Languages of the lowercased text after a base name's last dot.
const extensions: Record<string, Language> = {
  c: "c",
  h: "c",
  css: "css",
  scss: "scss",
  diff: "diff",
  patch: "diff",
  dockerfile: "docker",
  mk: "make",
  go: "go",
  java: "java",
  rs: "rust",
  swift: "swift",
  html: "html",
  htm: "html",
  xhtml: "html",
  xml: "xml",
  svg: "xml",
  plist: "xml",
  ini: "ini",
  toml: "toml",
  js: "javascript",
  mjs: "javascript",
  cjs: "javascript",
  jsx: "jsx",
  ts: "typescript",
  mts: "typescript",
  cts: "typescript",
  tsx: "tsx",
  json: "json",
  jsonc: "jsonc",
  jsonl: "json",
  md: "markdown",
  markdown: "markdown",
  ml: "ocaml",
  mli: "ocaml",
  py: "python",
  pyi: "python",
  sh: "bash",
  bash: "bash",
  zsh: "bash",
  sql: "sql",
  yml: "yaml",
  yaml: "yaml",
};

export function languageFor(path: string): Language | undefined {
  const base = path.slice(path.lastIndexOf("/") + 1);
  const named = Object.hasOwn(names, base) ? names[base] : undefined;
  if (named) return named;
  const dot = base.lastIndexOf(".");
  if (dot <= 0) return undefined;
  const extension = base.slice(dot + 1).toLowerCase();
  return Object.hasOwn(extensions, extension) ? extensions[extension] : undefined;
}

// A carriage return is written as a character reference, which the HTML
// parser keeps where it would turn a raw one into a newline.
function escapeHtml(text: string): string {
  return text.replaceAll("&", "&amp;").replaceAll("<", "&lt;").replaceAll("\r", "&#13;");
}

// The file's text reaches the DOM only through escapeHtml, inside the <span>
// elements written here: one <span class="line"> per line, holding the line's
// token spans and its \n, and token spans whose style attributes hold only the
// bundled themes' colors. The HTML adds no other elements and loads nothing.
// A file name no language claims is plain text, escaped the same way. The
// empty piece after a final \n is not a line, so an empty file has none.
export function highlight(path: string, text: string): { html: string; lines: number } {
  const pieces = text.split("\n");
  const lines = pieces.at(-1) === "" ? pieces.length - 1 : pieces.length;
  const html = linesOf(path, text, pieces)
    .slice(0, lines)
    .map((line, i) => `<span class="line">${line}${i < pieces.length - 1 ? "\n" : ""}</span>`)
    .join("");
  return { html, lines };
}

// linesOf returns the HTML of each of the text's pieces between newlines.
function linesOf(path: string, text: string, pieces: string[]): string[] {
  const language = languageFor(path);
  if (!language) return pieces.map(escapeHtml);
  const { tokens } = highlighter.codeToTokens(text, {
    lang: language,
    themes: { light: "github-light-default", dark: "github-dark-default" },
    defaultColor: "light-dark()",
  });
  // Shiki drops the \r of a \r\n and keeps any other, so only a line that a \n
  // follows gets its \r restored.
  return tokens.map((line, i) => {
    const spans = line
      .map((token) => `<span style="${stringifyTokenStyle(token.htmlStyle ?? {})}">${escapeHtml(token.content)}</span>`)
      .join("");
    return i < pieces.length - 1 && pieces[i].endsWith("\r") ? `${spans}&#13;` : spans;
  });
}
