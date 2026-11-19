import { action, createOptimistic, createSignal, For, Match, Show, Switch, snapshot } from "solid-js";
import { type Approval, type Entry, type Session, useOx } from "./ox";
import type { ApprovalResponse, Settings } from "./protocol";
import { changedSettings, defaultSettings, SettingsFields, settingsFrom } from "./SettingsFields";

type Tool = Extract<Entry, { kind: "tool" }>;

export function SessionView(props: { session: Session }) {
  const ox = useOx();
  const [error, setError] = createSignal<string>();
  const attempt = (request: Promise<unknown>) => {
    setError(undefined);
    return request.catch((e: Error) => {
      setError(e.message);
    });
  };
  const workspace = () => ox.state.workspaces.find((w) => w.id === props.session.workspaceId)?.path;

  const send = (event: SubmitEvent & { currentTarget: HTMLFormElement }) => {
    event.preventDefault();
    const form = event.currentTarget;
    const text = String(new FormData(form).get("text"));
    attempt(ox.send(props.session.id, text).then(() => form.reset()));
  };

  return (
    <>
      <p class="context">
        {workspace()} · {props.session.harness} · {props.session.id}
      </p>
      <div class="log">
        <Transcript entries={props.session.entries} />
        <For each={props.session.approvals}>
          {(approval) => (
            <ApprovalRequest
              approval={approval}
              onAnswer={(response) => attempt(ox.approve(props.session.id, approval.id, response))}
            />
          )}
        </For>
      </div>
      <Show when={error()}>{(error) => <p role="alert">{error()}</p>}</Show>
      <div class="composer">
        <form class="box" onSubmit={send}>
          <textarea name="text" rows="1" placeholder="Prompt" required />
          <button type="submit" class="icon send" aria-label="Send" title="Send" />
        </form>
        <SettingsForm session={props.session} attempt={attempt} />
      </div>
    </>
  );
}

export function NewSessionView(props: { workspaceId: string; onStarted(id: string): void }) {
  const ox = useOx();
  const [error, setError] = createSignal<string>();
  const workspace = () => ox.state.workspaces.find((w) => w.id === props.workspaceId)?.path;
  const [harness, setHarness] = createSignal(() => ox.state.defaults?.harness ?? "");
  const catalog = () => ox.state.harnesses[harness()];

  const send = async (event: SubmitEvent & { currentTarget: HTMLFormElement }) => {
    event.preventDefault();
    setError(undefined);
    const form = new FormData(event.currentTarget);
    try {
      const settings = settingsFrom(catalog(), form);
      props.onStarted(await ox.startSession(props.workspaceId, harness(), settings, String(form.get("text"))));
    } catch (e) {
      setError((e as Error).message);
    }
  };

  return (
    <>
      <p class="context">{workspace()} · new session</p>
      <div class="log" />
      <Show when={error()}>{(error) => <p role="alert">{error()}</p>}</Show>
      <form class="composer" onSubmit={send}>
        <div class="box">
          <textarea name="text" rows="1" placeholder="Prompt" required autofocus />
          <button type="submit" class="icon send" aria-label="Send" title="Send" />
        </div>
        <div class="settings">
          <select name="harness" onChange={(event) => setHarness(event.currentTarget.value)}>
            <For each={Object.keys(ox.state.harnesses)}>
              {(name) => <option selected={name === harness()}>{name}</option>}
            </For>
          </select>
          {/* Keyed, so choosing another harness starts from its defaults. */}
          <Show when={catalog()} keyed>
            {(catalog) => (
              <SettingsFields harness={catalog} value={defaultSettings(harness(), catalog, ox.state.defaults)} />
            )}
          </Show>
        </div>
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
          <Match when={entry.kind === "exited" && entry}>
            {(exited) => <div class="error">Claude exited: {exited().error}</div>}
          </Match>
        </Switch>
      )}
    </For>
  );
}

function ToolCall(props: { tool: Tool }) {
  const detail = () => {
    const first = Object.values(props.tool.input).find((v) => typeof v === "string") ?? "";
    return first.split("\n")[0].slice(0, 120);
  };
  const status = () => (props.tool.result ? (props.tool.result.is_error ? "failed" : "done") : "running");
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

function ApprovalRequest(props: { approval: Approval; onAnswer(response: ApprovalResponse): void }) {
  return (
    <fieldset>
      <legend>{props.approval.request.tool_name}</legend>
      <pre>{JSON.stringify(props.approval.request.input, null, 2)}</pre>
      <button
        type="button"
        class="primary"
        onClick={() => props.onAnswer({ behavior: "allow", updatedInput: snapshot(props.approval.request.input) })}
      >
        Allow
      </button>
      <button type="button" onClick={() => props.onAnswer({ behavior: "deny", message: "Denied by user" })}>
        Deny
      </button>
    </fieldset>
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

  return (
    <Show when={ox.state.harnesses[props.session.harness]}>
      {(harness) => (
        <form
          class="settings"
          onChange={(event) => {
            const changes = changedSettings(harness(), shown(), new FormData(event.currentTarget));
            if (Object.keys(changes).length) props.attempt(configure({ ...shown(), ...changes }, changes));
          }}
        >
          <SettingsFields harness={harness()} value={shown()} disabled={!configurable()} />
        </form>
      )}
    </Show>
  );
}
