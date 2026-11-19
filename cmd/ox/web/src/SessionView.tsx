import { For, Match, onSettled, Show, Switch } from "solid-js";
import { ApprovalRequest } from "./ApprovalRequest";
import { type Entry, type Session, type SessionControl, toolInputJSON, toolSubject, useOx } from "./ox";
import type { Settings } from "./protocol";
import { ModelPicker, Select } from "./SettingsFields";

type Tool = Extract<Entry, { kind: "tool" }>;

export function sendOnEnter(event: KeyboardEvent & { currentTarget: HTMLTextAreaElement }) {
  if (event.key === "Enter" && !event.shiftKey && !event.isComposing) {
    event.preventDefault();
    if (!event.repeat) event.currentTarget.form?.requestSubmit();
  }
}

// pinToBottom keeps log scrolled to the bottom as content arrives or the log
// shrinks, as when an approval grows the composer, unless the user has
// scrolled up.
function pinToBottom(log: HTMLElement) {
  let pinned = true;
  let top = log.scrollTop;
  // Chrome also fires scroll when the log shrinks without moving; only a
  // move counts as the user scrolling.
  log.addEventListener("scroll", () => {
    if (log.scrollTop === top) return;
    top = log.scrollTop;
    pinned = log.scrollHeight - log.scrollTop - log.clientHeight < 32;
  });
  const follow = () => {
    if (pinned) log.scrollTop = log.scrollHeight;
  };
  const mutations = new MutationObserver(follow);
  mutations.observe(log, { childList: true, subtree: true, characterData: true });
  const resizes = new ResizeObserver(follow);
  resizes.observe(log);
  log.scrollTop = log.scrollHeight;
  return () => {
    mutations.disconnect();
    resizes.disconnect();
  };
}

export function SessionView(props: { session: Session }) {
  const ox = useOx();
  const control = ox.control(props.session.id);
  const workspace = () => ox.state.workspaces.find((w) => w.id === props.session.workspaceId)?.path;
  // Stop shows once oxd reports the prompt running. oxd can take a stop sent
  // before the page's send is accepted ahead of the send, and it stops nothing.
  const running = () => !control.idle();
  let log!: HTMLDivElement;

  onSettled(() => pinToBottom(log));

  const send = (event: SubmitEvent) => {
    event.preventDefault();
    control.send(control.text());
  };

  return (
    <>
      <p class="context">
        {workspace()} · {props.session.harness} · {props.session.id}
      </p>
      <div class="log" ref={log}>
        <Transcript entries={props.session.entries} />
      </div>
      <Show when={control.error()}>{(error) => <p role="alert">{error()}</p>}</Show>
      <Show when={control.notice()}>{(notice) => <p role="status">{notice()}</p>}</Show>
      <div class="composer">
        <Show when={props.session.approvals.length}>
          <section class="pending" aria-label="Waiting on you">
            <For each={props.session.approvals}>
              {(approval) => (
                <ApprovalRequest approval={approval} onAnswer={(response) => control.approve(approval.id, response)} />
              )}
            </For>
          </section>
        </Show>
        <form class="box" onSubmit={send}>
          <textarea
            name="text"
            rows="1"
            aria-label="Prompt"
            required
            value={control.text()}
            onInput={(event) => control.setText(event.currentTarget.value)}
            onKeyDown={sendOnEnter}
          />
          <Show
            when={running()}
            fallback={
              <button type="submit" class="icon send" aria-label="Send" title="Send" disabled={!control.available()} />
            }
          >
            <button
              type="button"
              class="icon stop"
              aria-label="Stop"
              title="Stop"
              onClick={() => control.interrupt()}
            />
          </Show>
        </form>
        <SettingsForm session={props.session} control={control} />
      </div>
    </>
  );
}

function Transcript(props: { entries: Entry[] }) {
  return (
    <For each={props.entries}>
      {(entry) => (
        <Switch>
          <Match when={entry.kind === "prompt" && entry}>{(prompt) => <div class="prompt">{prompt().text}</div>}</Match>
          <Match when={entry.kind === "reply" && entry}>{(reply) => <div class="reply">{reply().text}</div>}</Match>
          <Match when={entry.kind === "tool" && entry}>{(tool) => <ToolCall tool={tool()} />}</Match>
          {/* A successful result repeats the reply, so only a failed turn shows its result. */}
          <Match when={entry.kind === "result" && entry.result.is_error && entry}>
            {(result) => <div class="error">{result().result.text}</div>}
          </Match>
          <Match when={entry.kind === "exited" && entry}>{(exited) => <div class="error">{exited().error}</div>}</Match>
        </Switch>
      )}
    </For>
  );
}

function ToolCall(props: { tool: Tool }) {
  const detail = () => toolSubject(props.tool.input).split("\n")[0].slice(0, 120);
  const status = () => {
    if (props.tool.result) return props.tool.result.is_error ? "failed" : "done";
    return props.tool.stopped ? "stopped" : "running";
  };
  return (
    <details class="tool">
      <summary>
        <span class={["marker", status()]} role="img" title={status()} aria-label={status()} />
        <span class="name">{props.tool.name}</span> <span class="detail">{detail()}</span>
      </summary>
      <div class="card">
        <pre>{toolInputJSON(props.tool.input)}</pre>
        <Show when={props.tool.result}>{(result) => <pre>{result().text}</pre>}</Show>
      </div>
    </details>
  );
}

// SettingsForm changes a session's settings through its control, which shows
// a change as pending while it is in flight and the stored settings once it
// settles.
function SettingsForm(props: { session: Session; control: SessionControl }) {
  const ox = useOx();
  const shown = () => props.control.requested() ?? props.session.settings;
  const pending = (key: keyof Settings) => shown()[key] !== props.session.settings[key];

  // Once the session starts, the picker offers only its harness's models.
  return (
    <Show when={ox.state.harnesses[props.session.harness]}>
      {(harness) => (
        <div class="settings">
          <ModelPicker
            harnesses={{ [props.session.harness]: harness() }}
            value={{ harness: props.session.harness, model: shown().model, effort: shown().effort }}
            disabled={!props.control.available()}
            pending={pending("model") || pending("effort")}
            onChange={(value) => props.control.configure({ model: value.model, effort: value.effort })}
          />
          <Select
            name="permission_mode"
            options={harness().permission_modes}
            value={shown().permission_mode}
            disabled={!props.control.available()}
            pending={pending("permission_mode")}
            onChange={(mode) => props.control.configure({ permission_mode: mode })}
          />
        </div>
      )}
    </Show>
  );
}
