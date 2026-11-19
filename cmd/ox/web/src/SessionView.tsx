import { createSignal, For, Match, onSettled, Show, Switch } from "solid-js";
import { ApprovalRequest } from "./ApprovalRequest";
import { createAttempt, type Entry, type Session, type SessionControl, toolSubject, useOx } from "./ox";
import type { Settings } from "./protocol";
import { defaultSettings, type ModelChoice, ModelPicker, Select } from "./SettingsFields";

type Tool = Extract<Entry, { kind: "tool" }>;

function sendOnEnter(event: KeyboardEvent & { currentTarget: HTMLTextAreaElement }) {
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
  const idle = () => props.session.state === "idle" || props.session.state === "parked";
  const canSend = () => !control.busy() && idle();
  // A send shows Stop from the start, while a parked session starts its harness.
  const running = () => control.sending() || !idle();
  let log!: HTMLDivElement;

  onSettled(() => pinToBottom(log));

  const send = (event: SubmitEvent) => {
    event.preventDefault();
    if (canSend()) control.send(control.text());
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
            fallback={<button type="submit" class="icon send" aria-label="Send" title="Send" disabled={!canSend()} />}
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

// NewSessionView creates a session for its first prompt and hands over to it:
// the session gets the box's text, including edits made while it was being
// created, and sends the prompt. The page then shows the session, unless the
// user has gone elsewhere.
export function NewSessionView(props: { workspaceId: string; onCreated(id: string): void }) {
  const ox = useOx();
  const [error, attempt] = createAttempt();
  const [creating, setCreating] = createSignal(false);
  const workspace = () => ox.state.workspaces.find((w) => w.id === props.workspaceId)?.path;
  // Until the session starts, the picker offers every harness's models.
  const [choice, setChoice] = createSignal<(Settings & { harness: string }) | undefined>(() => {
    const defaults = ox.state.defaults;
    return defaults && { ...defaults };
  });
  // Another harness keeps the permission mode when it offers it.
  const pick = (value: ModelChoice) =>
    setChoice((current) => {
      const catalog = ox.state.harnesses[value.harness];
      const mode = catalog.permission_modes.some((m) => m.id === current?.permission_mode)
        ? (current?.permission_mode ?? "")
        : defaultSettings(value.harness, catalog, ox.state.defaults).permission_mode;
      return { ...value, permission_mode: mode };
    });
  let textarea!: HTMLTextAreaElement;
  let open = true;
  onSettled(() => () => {
    open = false;
  });

  const create = (event: SubmitEvent) => {
    event.preventDefault();
    const c = choice();
    const text = textarea.value;
    if (creating() || !c || !text.trim()) return;
    const { harness, ...settings } = c;
    setCreating(true);
    const created = ox.createSession(props.workspaceId, harness, settings).then((id) => {
      const session = ox.control(id);
      session.setText(textarea.value);
      session.send(text);
      if (open) props.onCreated(id);
    });
    attempt(created).finally(() => setCreating(false));
  };

  return (
    <>
      <p class="context">{workspace()} · new session</p>
      <div class="log" />
      <Show when={error()}>{(error) => <p role="alert">{error()}</p>}</Show>
      <form class="composer" onSubmit={create}>
        <div class="box">
          <textarea
            ref={textarea}
            name="text"
            rows="1"
            aria-label="Prompt"
            required
            autofocus
            onKeyDown={sendOnEnter}
          />
          <button type="submit" class="icon send" aria-label="Send" title="Send" disabled={creating()} />
        </div>
        <Show when={choice()}>
          {(choice) => (
            <div class="settings">
              <ModelPicker harnesses={ox.state.harnesses} value={choice()} disabled={creating()} onChange={pick} />
              <Select
                name="permission_mode"
                options={ox.state.harnesses[choice().harness]?.permission_modes ?? []}
                value={choice().permission_mode}
                disabled={creating()}
                onChange={(mode) => setChoice({ ...choice(), permission_mode: mode })}
              />
            </div>
          )}
        </Show>
      </form>
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
        <pre>{JSON.stringify(props.tool.input, null, 2)}</pre>
        <Show when={props.tool.result}>{(result) => <pre>{result().text}</pre>}</Show>
      </div>
    </details>
  );
}

// SettingsForm changes a session's settings through its control, which shows
// a change while it is in flight and the stored settings once it settles.
function SettingsForm(props: { session: Session; control: SessionControl }) {
  const ox = useOx();
  const configurable = () =>
    !props.control.busy() && (props.session.state === "idle" || props.session.state === "parked");
  const shown = () => props.control.requested() ?? props.session.settings;

  // Once the session starts, the picker offers only its harness's models.
  return (
    <Show when={ox.state.harnesses[props.session.harness]}>
      {(harness) => (
        <div class="settings">
          <ModelPicker
            harnesses={{ [props.session.harness]: harness() }}
            value={{ harness: props.session.harness, model: shown().model, effort: shown().effort }}
            disabled={!configurable()}
            onChange={(value) => props.control.configure({ model: value.model, effort: value.effort })}
          />
          <Select
            name="permission_mode"
            options={harness().permission_modes}
            value={shown().permission_mode}
            disabled={!configurable()}
            onChange={(mode) => props.control.configure({ permission_mode: mode })}
          />
        </div>
      )}
    </Show>
  );
}
