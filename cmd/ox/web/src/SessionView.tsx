import { action, createOptimistic, createSignal, For, Match, onSettled, Show, Switch } from "solid-js";
import { ApprovalRequest } from "./ApprovalRequest";
import { createAttempt, type Entry, type Session, toolSubject, useOx } from "./ox";
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
  const [error, attempt] = createAttempt();
  const [sending, setSending] = createSignal(false);
  const workspace = () => ox.state.workspaces.find((w) => w.id === props.session.workspaceId)?.path;
  const canSend = () => !sending() && (props.session.state === "idle" || props.session.state === "parked");
  const running = () => props.session.state === "running" || props.session.state === "requires_action";
  let log!: HTMLDivElement;
  let textarea!: HTMLTextAreaElement;

  onSettled(() => pinToBottom(log));

  const send = (event: SubmitEvent) => {
    event.preventDefault();
    if (!canSend()) return;
    const text = textarea.value;
    setSending(true);
    // Text typed while the send was pending stays in the box.
    const sent = ox.send(props.session.id, text).then(() => {
      if (textarea.value === text) textarea.value = "";
    });
    attempt(sent).finally(() => setSending(false));
  };

  return (
    <>
      <p class="context">
        {workspace()} · {props.session.harness} · {props.session.id}
      </p>
      <div class="log" ref={log}>
        <Transcript entries={props.session.entries} />
      </div>
      <Show when={error()}>{(error) => <p role="alert">{error()}</p>}</Show>
      <div class="composer">
        <Show when={props.session.approvals.length}>
          <section class="pending" aria-label="Waiting on you">
            <For each={props.session.approvals}>
              {(approval) => (
                <ApprovalRequest
                  approval={approval}
                  onAnswer={(response) => attempt(ox.approve(props.session.id, approval.id, response))}
                />
              )}
            </For>
          </section>
        </Show>
        <form class="box" onSubmit={send}>
          <textarea ref={textarea} name="text" rows="1" aria-label="Prompt" required onKeyDown={sendOnEnter} />
          <Show
            when={running()}
            fallback={<button type="submit" class="icon send" aria-label="Send" title="Send" disabled={!canSend()} />}
          >
            <button
              type="button"
              class="icon stop"
              aria-label="Stop"
              title="Stop"
              onClick={() => attempt(ox.interrupt(props.session.id))}
            />
          </Show>
        </form>
        <SettingsForm session={props.session} attempt={attempt} />
      </div>
    </>
  );
}

// NewSessionView creates a session and sends its first prompt. A session
// whose first send failed takes the retry, with the settings it was created
// with.
export function NewSessionView(props: { workspaceId: string; onSent(id: string): void }) {
  const ox = useOx();
  const [error, attempt] = createAttempt();
  const [sending, setSending] = createSignal(false);
  const [created, setCreated] = createSignal<string>();
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

  const send = (event: SubmitEvent & { currentTarget: HTMLFormElement }) => {
    event.preventDefault();
    const c = choice();
    if (sending() || !c) return;
    const text = String(new FormData(event.currentTarget).get("text"));
    const { harness, ...settings } = c;
    setSending(true);
    const sent = (async () => {
      let id = created();
      if (id === undefined) {
        id = await ox.createSession(props.workspaceId, harness, settings);
        setCreated(id);
      }
      await ox.send(id, text);
      props.onSent(id);
    })();
    attempt(sent).finally(() => setSending(false));
  };

  return (
    <>
      <p class="context">{workspace()} · new session</p>
      <div class="log" />
      <Show when={error()}>{(error) => <p role="alert">{error()}</p>}</Show>
      <form class="composer" onSubmit={send}>
        <div class="box">
          <textarea name="text" rows="1" aria-label="Prompt" required autofocus onKeyDown={sendOnEnter} />
          <button type="submit" class="icon send" aria-label="Send" title="Send" disabled={sending()} />
        </div>
        <Show when={choice()}>
          {(choice) => (
            <div class="settings">
              <ModelPicker
                harnesses={ox.state.harnesses}
                value={choice()}
                disabled={created() !== undefined}
                onChange={pick}
              />
              <Select
                name="permission_mode"
                options={ox.state.harnesses[choice().harness]?.permission_modes ?? []}
                value={choice().permission_mode}
                disabled={created() !== undefined}
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

function SettingsForm(props: { session: Session; attempt(request: Promise<unknown>): void }) {
  const ox = useOx();
  const [shown, setShown] = createOptimistic(() => props.session.settings);
  const [configuring, setConfiguring] = createOptimistic(false);
  const configure = action(function* (settings: Settings, changes: Partial<Settings>) {
    setShown(settings);
    setConfiguring(true);
    yield ox.configure(props.session.id, changes);
  });
  const configurable = () => !configuring() && (props.session.state === "idle" || props.session.state === "parked");
  const change = (requested: Partial<Settings>) => {
    const from = shown();
    const changes = Object.fromEntries(
      Object.entries(requested).filter(([key, value]) => from[key as keyof Settings] !== value),
    ) as Partial<Settings>;
    if (Object.keys(changes).length) props.attempt(configure({ ...from, ...changes }, changes));
  };

  // Once the session starts, the picker offers only its harness's models.
  return (
    <Show when={ox.state.harnesses[props.session.harness]}>
      {(harness) => (
        <div class="settings">
          <ModelPicker
            harnesses={{ [props.session.harness]: harness() }}
            value={{ harness: props.session.harness, model: shown().model, effort: shown().effort }}
            disabled={!configurable()}
            onChange={(value) => change({ model: value.model, effort: value.effort })}
          />
          <Select
            name="permission_mode"
            options={harness().permission_modes}
            value={shown().permission_mode}
            disabled={!configurable()}
            onChange={(mode) => change({ permission_mode: mode })}
          />
        </div>
      )}
    </Show>
  );
}
