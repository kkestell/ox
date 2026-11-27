import { createMemo, Match, Switch, untrack } from "solid-js";
import { highlight } from "./highlight";
import { Markdown } from "./Markdown";
import { useOx } from "./ox";
import { createPoll } from "./poll";
import type { FileRoot } from "./protocol";
import type { FileMode } from "./tabs";

// FileView shows one file beneath a root, read when it appears and on each tick
// of the page's clock, as its source or, for a Markdown file, rendered. It is
// keyed by its selection and path, so its text is always that file's.
export function FileView(props: { root: FileRoot; path: string; mode: FileMode; wrap: boolean }) {
  const ox = useOx();
  const root = untrack(() => props.root);
  const path = untrack(() => props.path);
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
  const source = createMemo(() => {
    const current = text();
    return current === undefined ? { html: "", lines: 0 } : highlight(path, current);
  });
  return (
    <section class="viewer" aria-label="File">
      <Switch>
        <Match when={error()}>
          {(message) => (
            <p role="tabpanel" class="message">
              {message()}
            </p>
          )}
        </Match>
        <Match when={props.mode === "preview"}>
          <div role="tabpanel" class="preview">
            <Markdown text={text() ?? ""} file />
          </div>
        </Match>
        <Match when={props.mode === "source"}>
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
