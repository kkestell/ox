import type { JSX } from "@solidjs/web";
import { createMemo, For, Match, Show, Switch, untrack } from "solid-js";
import { wholeFile } from "./diff";
import { highlight, languageFor, rowsHtml } from "./highlight";
import { Markdown } from "./Markdown";
import { type FileMode, useOx } from "./ox";
import { createPoll } from "./poll";
import type { ChangedFile, FileRoot } from "./protocol";

const modes: { mode: FileMode; label: string; icon: string }[] = [
  { mode: "diff", label: "Diff", icon: "git-compare" },
  { mode: "source", label: "Source", icon: "code" },
  { mode: "preview", label: "Preview", icon: "eye" },
];

// FileView shows one file beneath a root, read when it appears and on each tick
// of the page's clock, under a header that starts with start and ends with
// end, and a bar below it with a tab with the file's name that closes it and
// the file's modes and wrap toggle. It is keyed by its selection
// and path, so its text is always that file's. A changed file can show its
// changes in place, and a Markdown file rendered; without a chosen mode, a
// changed file shows its changes, a Markdown file shows rendered, and any other
// shows its source. Until change says whether the file changed, it shows no
// mode.
export function FileView(props: {
  root: FileRoot;
  path: string;
  change: { file?: ChangedFile } | undefined;
  mode?: FileMode;
  onMode: (mode: FileMode) => void;
  wrap: boolean;
  onWrap: (wrap: boolean) => void;
  onClose: () => void;
  start: JSX.Element;
  end?: JSX.Element;
}) {
  const ox = useOx();
  const root = untrack(() => props.root);
  const path = untrack(() => props.path);
  const name = path.slice(path.lastIndexOf("/") + 1);
  const read = createPoll(() => ox.readFile(root, path));
  // A memo, so an unchanged read is not highlighted or rendered again.
  const text = createMemo(() => {
    const current = read();
    return current?.kind === "value" ? current.value.text : undefined;
  });
  const error = () => {
    const current = read();
    return current?.kind === "error" ? current.message : undefined;
  };
  const hunks = () => props.change?.file?.hunks ?? undefined;
  const available = (mode: FileMode) =>
    mode === "source" || (mode === "diff" ? hunks() !== undefined : languageFor(path) === "markdown");
  const mode = (): FileMode | undefined => {
    if (props.change === undefined) return undefined;
    if (props.mode && available(props.mode)) return props.mode;
    if (available("diff")) return "diff";
    if (available("preview")) return "preview";
    return "source";
  };
  const source = createMemo(() => {
    const current = text();
    return current === undefined ? { html: "", lines: 0 } : highlight(path, current);
  });
  // A memo, so a poll that returns the same text and hunks highlights nothing
  // again.
  const joined = createMemo(() => {
    const current = text();
    const each = hunks();
    return current === undefined || each === undefined ? "" : wholeFile(current, each).join("\n");
  });
  const diff = createMemo(() =>
    joined() === "" ? { html: "", digits: 1 } : rowsHtml(path, joined().split("\n"), 1, 1),
  );
  const shown = createMemo(() => modes.filter((each) => available(each.mode)));
  return (
    <section class="viewer" aria-label="File">
      <header>
        {props.start}
        <div class="editor-actions">{props.end}</div>
      </header>
      <div class="file-bar">
        <div role="tablist" aria-label="Open files" class="file-tabs">
          <div class="tab">
            <button type="button" role="tab" aria-selected="true" title={path}>
              <span class="icon file" />
              <span>{name}</span>
            </button>
            <button
              type="button"
              class="icon x"
              aria-label={`Close ${name}`}
              title={`Close ${name}`}
              onClick={() => props.onClose()}
            />
          </div>
        </div>
        <div class="editor-actions">
          <Show when={mode() !== undefined && shown().length > 1}>
            <fieldset aria-label="Show as" class="segmented">
              <For each={shown()}>
                {(each) => (
                  <button
                    type="button"
                    class={["icon", each.icon]}
                    aria-label={each.label}
                    title={each.label}
                    aria-pressed={mode() === each.mode ? "true" : "false"}
                    onClick={() => props.onMode(each.mode)}
                  />
                )}
              </For>
            </fieldset>
          </Show>
          <Show when={mode() !== undefined && mode() !== "preview"}>
            <button
              type="button"
              class="icon wrap-text"
              aria-label="Toggle word wrap"
              title="Toggle word wrap"
              aria-pressed={props.wrap ? "true" : "false"}
              onClick={() => props.onWrap(!props.wrap)}
            />
          </Show>
        </div>
      </div>
      <Switch>
        <Match when={error()}>
          {(message) => (
            <p role="tabpanel" class="message">
              {message()}
            </p>
          )}
        </Match>
        <Match when={mode() === "preview"}>
          <div role="tabpanel" class="preview">
            <Markdown text={text() ?? ""} />
          </div>
        </Match>
        <Match when={mode() === "diff"}>
          <pre role="tabpanel" class={["diff", { wrap: props.wrap }]} style={{ "--digits": diff().digits }}>
            <code innerHTML={diff().html} />
          </pre>
        </Match>
        <Match when={mode() === "source"}>
          <pre
            role="tabpanel"
            class={["source", { wrap: props.wrap }]}
            style={{ "--digits": String(source().lines).length }}
          >
            <code innerHTML={source().html} />
          </pre>
        </Match>
      </Switch>
    </section>
  );
}
