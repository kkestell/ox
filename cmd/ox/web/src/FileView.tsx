import { Show, untrack } from "solid-js";
import { useOx } from "./ox";
import { createPoll } from "./poll";

// FileView shows one file of a workspace under its tab, read when it appears
// and on each tick of the page's clock. It is keyed by its workspace and path,
// so its text is always that file's.
export function FileView(props: { workspaceId: string; path: string; onClose: () => void }) {
  const ox = useOx();
  const workspaceId = untrack(() => props.workspaceId);
  const path = untrack(() => props.path);
  const name = path.slice(path.lastIndexOf("/") + 1);
  const read = createPoll(() => ox.readFile(workspaceId, path));
  const text = () => {
    const current = read();
    return current?.kind === "value" ? current.value.text : undefined;
  };
  const error = () => {
    const current = read();
    return current?.kind === "error" ? current.message : undefined;
  };
  return (
    <section class="viewer" aria-label="File">
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
      </header>
      <Show when={error()} fallback={<pre role="tabpanel">{text()}</pre>}>
        {(message) => <p role="tabpanel">{message()}</p>}
      </Show>
    </section>
  );
}
