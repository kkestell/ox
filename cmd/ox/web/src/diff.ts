import type { ChangedFile } from "./protocol";

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
