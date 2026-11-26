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

type Status = ChangedFile["status"];

// statuses gives the status of each changed file, and of each folder above
// one: a folder's status is its changed files' when they share one, and
// modified otherwise. A file can share its path with a folder that held
// another changed file, so the two are kept apart.
export function statuses(changed: ChangedFile[]): { files: Map<string, Status>; folders: Map<string, Status> } {
  const files = new Map<string, Status>();
  const folders = new Map<string, Status>();
  for (const file of changed) {
    files.set(file.path, file.status);
    for (let end = file.path.lastIndexOf("/"); end > 0; end = file.path.lastIndexOf("/", end - 1)) {
      const folder = file.path.slice(0, end);
      const current = folders.get(folder);
      folders.set(folder, current === undefined || current === file.status ? file.status : "modified");
    }
  }
  return { files, folders };
}
