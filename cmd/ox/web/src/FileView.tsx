import { createMemo, Show, untrack } from "solid-js";
import { highlight } from "./highlight";
import { Markdown } from "./Markdown";
import { type MarkdownView, useOx } from "./ox";
import { createPoll } from "./poll";
import type { FileRoot } from "./protocol";

// FileView shows one file beneath a root under its tab, read when it appears
// and on each tick of the page's clock. It is keyed by its selection and path,
// so its text is always that file's. A Markdown file has a view, which shows
// it rendered or as its numbered source; any other file shows its source.
export function FileView(props: {
  root: FileRoot;
  path: string;
  share?: number;
  view?: MarkdownView;
  onView: (view: MarkdownView) => void;
  wrap: boolean;
  onWrap: (wrap: boolean) => void;
  onClose: () => void;
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
  const source = createMemo(() => {
    const current = text();
    return current === undefined ? { html: "", lines: 0 } : highlight(path, current);
  });
  const error = () => {
    const current = read();
    return current?.kind === "error" ? current.message : undefined;
  };
  const preview = () => props.view === "preview";
  return (
    <section class="viewer" aria-label="File" style={{ "flex-grow": props.share }}>
      <header>
        <div role="tablist" aria-label="Open files">
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
          <Show when={!preview()}>
            <button
              type="button"
              class="icon wrap-text"
              aria-label="Toggle word wrap"
              title="Toggle word wrap"
              aria-pressed={props.wrap ? "true" : "false"}
              onClick={() => props.onWrap(!props.wrap)}
            />
          </Show>
          <Show when={props.view}>
            <button
              type="button"
              class="icon eye"
              aria-label="Toggle preview"
              title="Toggle preview"
              aria-pressed={preview() ? "true" : "false"}
              onClick={() => props.onView(preview() ? "source" : "preview")}
            />
          </Show>
        </div>
      </header>
      <Show
        when={error()}
        fallback={
          <Show
            when={preview()}
            fallback={
              <pre role="tabpanel" class={{ wrap: props.wrap }} style={{ "--digits": String(source().lines).length }}>
                <code innerHTML={source().html} />
              </pre>
            }
          >
            <div role="tabpanel" class="preview">
              <Markdown text={text() ?? ""} />
            </div>
          </Show>
        }
      >
        {(message) => <p role="tabpanel">{message()}</p>}
      </Show>
    </section>
  );
}
