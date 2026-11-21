import hljs from "highlight.js/lib/core";
import bash from "highlight.js/lib/languages/bash";
import c from "highlight.js/lib/languages/c";
import cpp from "highlight.js/lib/languages/cpp";
import css from "highlight.js/lib/languages/css";
import diff from "highlight.js/lib/languages/diff";
import dockerfile from "highlight.js/lib/languages/dockerfile";
import go from "highlight.js/lib/languages/go";
import ini from "highlight.js/lib/languages/ini";
import java from "highlight.js/lib/languages/java";
import javascript from "highlight.js/lib/languages/javascript";
import json from "highlight.js/lib/languages/json";
import makefile from "highlight.js/lib/languages/makefile";
import markdown from "highlight.js/lib/languages/markdown";
import ocaml from "highlight.js/lib/languages/ocaml";
import plaintext from "highlight.js/lib/languages/plaintext";
import python from "highlight.js/lib/languages/python";
import rust from "highlight.js/lib/languages/rust";
import scss from "highlight.js/lib/languages/scss";
import sql from "highlight.js/lib/languages/sql";
import swift from "highlight.js/lib/languages/swift";
import typescript from "highlight.js/lib/languages/typescript";
import xml from "highlight.js/lib/languages/xml";
import yaml from "highlight.js/lib/languages/yaml";

const languages = {
  bash,
  c,
  cpp,
  css,
  diff,
  dockerfile,
  go,
  ini,
  java,
  javascript,
  json,
  makefile,
  markdown,
  ocaml,
  plaintext,
  python,
  rust,
  scss,
  sql,
  swift,
  typescript,
  xml,
  yaml,
};
for (const [name, language] of Object.entries(languages)) hljs.registerLanguage(name, language);

// Languages of whole base names.
const names: Record<string, string> = {
  Dockerfile: "dockerfile",
  Containerfile: "dockerfile",
  Makefile: "makefile",
  makefile: "makefile",
  GNUmakefile: "makefile",
};

// Languages of the lowercased text after a base name's last dot.
const extensions: Record<string, string> = {
  c: "c",
  h: "c",
  cc: "cpp",
  cpp: "cpp",
  cxx: "cpp",
  hh: "cpp",
  hpp: "cpp",
  hxx: "cpp",
  css: "css",
  scss: "scss",
  diff: "diff",
  patch: "diff",
  dockerfile: "dockerfile",
  mk: "makefile",
  go: "go",
  java: "java",
  rs: "rust",
  swift: "swift",
  html: "xml",
  htm: "xml",
  xhtml: "xml",
  xml: "xml",
  svg: "xml",
  plist: "xml",
  ini: "ini",
  toml: "ini",
  js: "javascript",
  mjs: "javascript",
  cjs: "javascript",
  jsx: "javascript",
  ts: "typescript",
  mts: "typescript",
  cts: "typescript",
  tsx: "typescript",
  json: "json",
  jsonc: "json",
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

function languageFor(path: string): string {
  const base = path.slice(path.lastIndexOf("/") + 1);
  const named = Object.hasOwn(names, base) ? names[base] : undefined;
  if (named) return named;
  const dot = base.lastIndexOf(".");
  if (dot <= 0) return "plaintext";
  const extension = base.slice(dot + 1).toLowerCase();
  return Object.hasOwn(extensions, extension) ? extensions[extension] : "plaintext";
}

// highlight.js escapes the source text and emits only <span class="hljs-…">
// elements, so the returned HTML adds no elements from the file and loads
// nothing. A file name no language claims is plain text, escaped the same way.
export function highlight(path: string, text: string): string {
  return hljs.highlight(text, { language: languageFor(path), ignoreIllegals: true }).value;
}
