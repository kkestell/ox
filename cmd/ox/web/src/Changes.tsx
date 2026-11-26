import { createEffect, createMemo, createSignal, For, Match, Show, Switch, untrack } from "solid-js";
import { counts, statuses } from "./diff";
import { rowsHtml } from "./highlight";
import { useOx } from "./ox";
import { createPoll } from "./poll";
import type { ChangedFile, FileRoot, Hunk } from "./protocol";

// createListing polls the files beneath root that differ from where its branch
// left the workspace's branch, read when it appears and on each tick of the
// page's clock. It gives their total counts and the status of each changed
// file and of each folder above one.
export function createListing(root: FileRoot) {
  const ox = useOx();
  const listing = createPoll(() => ox.listChanges(root));
  const changes = () => {
    const current = listing();
    return current?.kind === "value" ? current.value : undefined;
  };
  const error = () => {
    const current = listing();
    return current?.kind === "error" ? current.message : undefined;
  };
  const total = createMemo(() => {
    let added = 0;
    let removed = 0;
    for (const file of changes()?.files ?? []) {
      added += file.counts?.added ?? 0;
      removed += file.counts?.removed ?? 0;
    }
    return { added, removed };
  });
  const byPath = createMemo(() => statuses(changes()?.files ?? []));
  return {
    changes,
    error,
    total,
    fileStatus: (path: string) => byPath().files.get(path),
    folderStatus: (path: string) => byPath().folders.get(path),
  };
}

export type Listing = ReturnType<typeof createListing>;

// BranchSummary shows the branch and the lines the changes add and remove, or
// why they cannot be listed.
function BranchSummary(props: { listing: Listing }) {
  return (
    <Switch>
      <Match when={props.listing.error()}>
        {(message) => (
          <span class="branch-summary message" title={message()}>
            {message()}
          </span>
        )}
      </Match>
      <Match when={props.listing.changes()}>
        {(current) => (
          <span class="branch-summary">
            <Show when={current().branch}>
              {(branch) => (
                <>
                  <span class="icon git-branch" />
                  <span class="branch">{branch()}</span>
                </>
              )}
            </Show>
            <Counts added={props.listing.total().added} removed={props.listing.total().removed} />
          </span>
        )}
      </Match>
    </Switch>
  );
}

// ChangesList lists each changed file under a summary row with the branch and
// totals, or why the changes cannot be listed. The summary row opens the
// Changes tab, and a file's row opens it at that file.
export function ChangesList(props: { listing: Listing; onOpen(path?: string): void }) {
  return (
    <div class="tree">
      <button type="button" class="row summary" onClick={() => props.onOpen()}>
        <BranchSummary listing={props.listing} />
      </button>
      <Show when={props.listing.changes()}>
        {(current) => (
          <ul>
            <For each={current().files} keyed={(file) => file.path}>
              {(file) => {
                const path = () => file().path;
                const name = () => path().slice(path().lastIndexOf("/") + 1);
                const folder = () => path().slice(0, Math.max(0, path().lastIndexOf("/")));
                return (
                  <li>
                    <button
                      type="button"
                      class="row change-row"
                      title={path()}
                      data-status={file().status}
                      onClick={() => props.onOpen(path())}
                    >
                      <span class="name">{name()}</span>
                      <Show when={folder()}>
                        <span class="folder">{folder()}</span>
                      </Show>
                      <Show when={file().counts}>
                        {(each) => <Counts added={each().added} removed={each().removed} />}
                      </Show>
                    </button>
                  </li>
                );
              }}
            </For>
          </ul>
        )}
      </Show>
    </div>
  );
}

// ChangesView shows each changed file as a card after GitHub's pull request
// files, or why the changes cannot be listed. It scrolls to the card of
// reveal's path once for each reveal, as soon as the listing holds that file,
// so a poll that lists the file again leaves the user's scroll alone.
export function ChangesView(props: {
  listing: Listing;
  wrap: boolean;
  reveal?: { path: string };
  onOpen(path: string): void;
}) {
  let body!: HTMLDivElement;
  let applied: { path: string } | undefined;
  createEffect(
    () => {
      const reveal = props.reveal;
      if (!reveal || reveal === applied) return undefined;
      return props.listing.changes()?.files.some((file) => file.path === reveal.path) ? reveal : undefined;
    },
    (reveal) => {
      if (!reveal) return;
      for (const card of body.querySelectorAll("article")) {
        if (card.getAttribute("aria-label") === reveal.path) card.scrollIntoView({ block: "start" });
      }
      applied = reveal;
    },
  );
  return (
    <section class="viewer" aria-label="Changes">
      <div ref={body} class="changes">
        <Switch>
          <Match when={props.listing.error()}>{(message) => <p class="message">{message()}</p>}</Match>
          <Match when={props.listing.changes()}>
            {(current) => (
              <Show when={current().files.length > 0} fallback={<p class="message">No changes</p>}>
                <For each={current().files} keyed={(file) => file.path}>
                  {(file) => <FileCard file={file()} wrap={props.wrap} onOpen={(path) => props.onOpen(path)} />}
                </For>
              </Show>
            )}
          </Match>
        </Switch>
      </div>
    </section>
  );
}

export function Counts(props: { added: number; removed: number }) {
  return (
    <span class="counts">
      <span class="added">+{props.added}</span>
      <span class="removed">−{props.removed}</span>
    </span>
  );
}

// FileCard shows one changed file: its name and folder, its counts, a button
// that opens it in the file view unless it was deleted, and its hunks, or why
// they cannot be shown.
function FileCard(props: { file: ChangedFile; wrap: boolean; onOpen(path: string): void }) {
  const name = () => props.file.path.slice(props.file.path.lastIndexOf("/") + 1);
  const folder = () => props.file.path.slice(0, Math.max(0, props.file.path.lastIndexOf("/")));
  return (
    <article class="change" aria-label={props.file.path}>
      <header>
        <div class="title" title={props.file.path}>
          <span class="name">{name()}</span>
          <Show when={folder()}>
            <span class="folder">{folder()}</span>
          </Show>
        </div>
        <Show when={props.file.counts}>{(each) => <Counts added={each().added} removed={each().removed} />}</Show>
        <Show when={props.file.status !== "deleted"}>
          <button
            type="button"
            class="icon square-arrow-out-up-right"
            aria-label={`Open ${name()}`}
            title={`Open ${name()}`}
            onClick={() => props.onOpen(props.file.path)}
          />
        </Show>
      </header>
      <Show when={props.file.hunks} fallback={<p class="message">{props.file.reason}</p>}>
        {(hunks) => (
          <For each={hunks()} keyed={(hunk) => `${hunk.old_start},${hunk.new_start}`}>
            {(hunk) => <HunkView path={props.file.path} hunk={hunk()} wrap={props.wrap} />}
          </For>
        )}
      </Show>
    </article>
  );
}

// HunkView shows one hunk under a header that collapses it, starting
// expanded. It is keyed by where the hunk starts, so it keeps its state across
// polls until its position changes.
function HunkView(props: { path: string; hunk: Hunk; wrap: boolean }) {
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
        <pre class={{ wrap: props.wrap }} style={{ "--digits": rows().digits }}>
          <code innerHTML={rows().html} />
        </pre>
      </Show>
    </section>
  );
}
