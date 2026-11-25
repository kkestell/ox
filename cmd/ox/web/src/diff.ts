import type { ChangedFile, Hunk } from "./protocol";

// counts returns how many of the lines a diff adds and removes.
export function counts(lines: string[]) {
  let added = 0;
  let removed = 0;
  for (const line of lines) {
    if (line[0] === "+") added++;
    else if (line[0] === "-") removed++;
  }
  return { added, removed };
}

export const fileCounts = (file: ChangedFile) => counts(file.hunks?.flatMap((hunk) => hunk.lines) ?? []);

// wholeFile writes a file's text and its hunks as the lines of one hunk that
// covers the whole file, each with git's one-character prefix, so rowsHtml
// shows the file with its changes in place. A text read at another moment than
// the hunks may not match them; the lines then follow the hunks.
export function wholeFile(text: string, hunks: Hunk[]): string[] {
  const lines = text.endsWith("\n") ? text.slice(0, -1).split("\n") : text.split("\n");
  if (text === "") lines.length = 0;
  const out: string[] = [];
  let next = 1;
  for (const hunk of hunks) {
    while (next < hunk.new_start && next <= lines.length) out.push(` ${lines[next++ - 1]}`);
    for (const line of hunk.lines) {
      out.push(line);
      if (line[0] !== "-") next++;
    }
  }
  while (next <= lines.length) out.push(` ${lines[next++ - 1]}`);
  return out;
}

// statuses gives the status of each changed file and of each folder above one:
// a folder's status is its changed files' when they share one, and modified
// otherwise.
export function statuses(files: ChangedFile[]): Map<string, ChangedFile["status"]> {
  const all = new Map<string, ChangedFile["status"]>();
  for (const file of files) {
    all.set(file.path, file.status);
    for (let end = file.path.lastIndexOf("/"); end > 0; end = file.path.lastIndexOf("/", end - 1)) {
      const folder = file.path.slice(0, end);
      const current = all.get(folder);
      all.set(folder, current === undefined || current === file.status ? file.status : "modified");
    }
  }
  return all;
}
