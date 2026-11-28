import type { ChangedFile, FolderEntry } from "./protocol";

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

// deletedEntries gives, for each folder above a deleted file, the deleted
// files and folders the file's path names in it: the next folder on the path,
// or the file itself. The root folder is "".
export function deletedEntries(changed: ChangedFile[]): Map<string, FolderEntry[]> {
  const byFolder = new Map<string, FolderEntry[]>();
  const seen = new Set<string>();
  for (const file of changed) {
    if (file.status !== "deleted") continue;
    const names = file.path.split("/");
    names.forEach((name, i) => {
      const folder = names.slice(0, i).join("/");
      const entry = { name, folder: i < names.length - 1 };
      const key = JSON.stringify([folder, entry.name, entry.folder]);
      if (seen.has(key)) return;
      seen.add(key);
      const entries = byFolder.get(folder);
      if (entries) entries.push(entry);
      else byFolder.set(folder, [entry]);
    });
  }
  return byFolder;
}

// TreeEntry is a folder's entry in the file tree: one on disk, or one the
// changes record as deleted.
export type TreeEntry = FolderEntry & { onDisk: boolean };

const collator = new Intl.Collator(undefined, { numeric: true, sensitivity: "base" });
const byFolderThenName = (a: FolderEntry, b: FolderEntry) =>
  Number(b.folder) - Number(a.folder) || collator.compare(a.name, b.name);

// treeEntries gives a folder's entries in the file tree, folders first: its
// entries on disk, and each deleted entry whose name the disk does not list.
export function treeEntries(disk: FolderEntry[], deleted: FolderEntry[]): TreeEntry[] {
  const names = new Set(disk.map((entry) => entry.name));
  return [
    ...disk.map((entry) => ({ ...entry, onDisk: true })),
    ...deleted.filter((entry) => !names.has(entry.name)).map((entry) => ({ ...entry, onDisk: false })),
  ].toSorted(byFolderThenName);
}
