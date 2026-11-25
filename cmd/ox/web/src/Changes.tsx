import { createMemo, createSignal, For, Match, Show, Switch, untrack } from "solid-js";
import { highlightLines } from "./highlight";
import { type SessionControl, useOx } from "./ox";
import { createPoll } from "./poll";
import type { ChangedFile, FileRoot, Hunk } from "./protocol";

// counts returns how many of the lines a diff adds and removes.
function counts(lines: string[]) {
  let added = 0;
  let removed = 0;
  for (const line of lines) {
    if (line[0] === "+") added++;
    else if (line[0] === "-") removed++;
  }
  return { added, removed };
}

const fileCounts = (file: ChangedFile) => counts(file.hunks?.flatMap((hunk) => hunk.lines) ?? []);

// Changes lists the files beneath root that differ from where its branch left
// the workspace's branch, read when it appears and on each tick of the page's
// clock. It is keyed by its selection, so its listing is always that root's.
export function Changes(props: { root: FileRoot; control: SessionControl; onOpen(): void }) {
  const ox = useOx();
  const root = untrack(() => props.root);
  const listing = createPoll(() => ox.listChanges(root));
  const error = () => {
    const current = listing();
    return current?.kind === "error" ? current.message : undefined;
  };
  const changes = () => {
    const current = listing();
    return current?.kind === "value" ? current.value : undefined;
  };
  const total = createMemo(() => {
    let added = 0;
    let removed = 0;
    for (const file of changes()?.files ?? []) {
      const each = fileCounts(file);
      added += each.added;
      removed += each.removed;
    }
    return { added, removed };
  });
  return (
    <div role="tabpanel" aria-label="Changes" class="changes">
      <Switch>
        <Match when={error()}>{(message) => <p class="message">{message()}</p>}</Match>
        <Match when={changes()}>
          {(current) => (
            <>
              <div class="summary">
                <Show when={current().branch}>
                  {(branch) => (
                    <>
                      <span class="icon git-branch" />
                      <span class="branch">{branch()}</span>
                    </>
                  )}
                </Show>
                <Counts added={total().added} removed={total().removed} />
              </div>
              <Show when={current().files.length > 0} fallback={<p class="message">No changes</p>}>
                <For each={current().files} keyed={(file) => file.path}>
                  {(file) => <FileCard file={file()} control={props.control} onOpen={() => props.onOpen()} />}
                </For>
              </Show>
            </>
          )}
        </Match>
      </Switch>
    </div>
  );
}

function Counts(props: { added: number; removed: number }) {
  return (
    <span class="counts">
      <span class="added">+{props.added}</span>
      <span class="removed">−{props.removed}</span>
    </span>
  );
}

// FileCard shows one changed file: its name and folder, its counts, a button
// that opens it in the file viewer unless it was deleted, and its hunks, or why
// they cannot be shown.
function FileCard(props: { file: ChangedFile; control: SessionControl; onOpen(): void }) {
  const name = () => props.file.path.slice(props.file.path.lastIndexOf("/") + 1);
  const folder = () => props.file.path.slice(0, Math.max(0, props.file.path.lastIndexOf("/")));
  const each = createMemo(() => fileCounts(props.file));
  return (
    <article class="change" aria-label={props.file.path}>
      <header>
        <div class="title" title={props.file.path}>
          <span class="name">{name()}</span>
          <Show when={folder()}>
            <span class="folder">{folder()}</span>
          </Show>
        </div>
        <Show when={props.file.hunks}>
          <Counts added={each().added} removed={each().removed} />
        </Show>
        <Show when={props.file.status !== "deleted"}>
          <button
            type="button"
            class="icon square-arrow-out-up-right"
            aria-label={`Open ${name()}`}
            title={`Open ${name()}`}
            onClick={() => {
              props.control.setFile(props.file.path);
              props.onOpen();
            }}
          />
        </Show>
      </header>
      <Show when={props.file.hunks} fallback={<p class="message">{props.file.reason}</p>}>
        {(hunks) => (
          <For each={hunks()} keyed={(hunk) => `${hunk.old_start},${hunk.new_start}`}>
            {(hunk) => <HunkView path={props.file.path} hunk={hunk()} />}
          </For>
        )}
      </Show>
    </article>
  );
}

// rowsHtml writes a hunk's lines as rows: an added line in <ins>, a removed one
// in <del>, a context line in a <span>, each with its line number in
// data-line, the new side's for context and added lines and the old side's for
// removed ones. Each side highlights as one text, so a line's colors follow
// the lines before it.
function rowsHtml(path: string, lines: string[], oldStart: number, newStart: number) {
  const side = (skip: string) =>
    highlightLines(
      path,
      lines
        .filter((line) => line[0] !== skip)
        .map((line) => `${line.slice(1)}\n`)
        .join(""),
    );
  const oldSide = side("+");
  const newSide = side("-");
  let oldLine = oldStart;
  let newLine = newStart;
  let o = 0;
  let n = 0;
  let html = "";
  let widest = 0;
  for (const line of lines) {
    let tag: string;
    let number: number;
    let text: string;
    if (line[0] === "-") {
      [tag, number, text] = ["del", oldLine++, oldSide[o++]];
    } else if (line[0] === "+") {
      [tag, number, text] = ["ins", newLine++, newSide[n++]];
    } else {
      [tag, number, text] = ["span", newLine++, newSide[n++]];
      oldLine++;
      o++;
    }
    widest = Math.max(widest, number);
    html += `<${tag} class="line" data-line="${number}">${text}\n</${tag}>`;
  }
  return { html, digits: String(widest).length };
}

// HunkView shows one hunk under a header that collapses it, starting
// expanded. It is keyed by where the hunk starts, so it keeps its state across
// polls until its position changes.
function HunkView(props: { path: string; hunk: Hunk }) {
  const [open, setOpen] = createSignal(true);
  // A memo, so a poll that returns the same lines highlights nothing again.
  const joined = createMemo(() => props.hunk.lines.join("\n"));
  const rows = createMemo(() =>
    rowsHtml(
      props.path,
      joined().split("\n"),
      untrack(() => props.hunk.old_start),
      untrack(() => props.hunk.new_start),
    ),
  );
  const each = createMemo(() => counts(props.hunk.lines));
  const label = () => {
    const { lines, old_start, new_start } = props.hunk;
    const { added } = each();
    const start = added > 0 ? new_start : old_start;
    const length = lines.filter((line) => line[0] !== (added > 0 ? "-" : "+")).length;
    const end = start + length - 1;
    return end > start ? `Lines ${start}–${end}` : `Line ${start}`;
  };
  return (
    <section class="hunk">
      <button type="button" aria-expanded={open() ? "true" : "false"} onClick={() => setOpen(!open())}>
        <span class={["icon", "chevron-right", { open: open() }]} />
        <span class="range">{label()}</span>
        <Counts added={each().added} removed={each().removed} />
      </button>
      <Show when={open()}>
        <pre style={{ "--digits": rows().digits }}>
          <code innerHTML={rows().html} />
        </pre>
      </Show>
    </section>
  );
}
