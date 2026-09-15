export function toolLabel(tool: { name?: string; title: string }): string {
  const title = tool.title.trim();
  if (tool.name === "shell" && !/^run\b/i.test(title)) return `Run ${title}`;
  if (!title.startsWith("mcp__")) return title;
  const segment = title.split("__").at(-1) ?? title;
  const words = segment.replaceAll("_", " ");
  return words.charAt(0).toUpperCase() + words.slice(1);
}
