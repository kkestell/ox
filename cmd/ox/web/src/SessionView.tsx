import { action, createOptimistic, createSignal, For, Match, Show, Switch, snapshot } from "solid-js";
import { type Approval, type Entry, type Session, useOx } from "./ox";
import type { ApprovalResponse, Settings } from "./protocol";
import { changedSettings, defaultSettings, SettingsFields, settingsFrom } from "./SettingsFields";

type Tool = Extract<Entry, { kind: "tool" }>;

export function SessionView(props: { session: Session }) {
  const ox = useOx();
  const [error, setError] = createSignal<string>();
  // attempt clears the last error and shows this request's, if it fails.
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
      <p>
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
      {/* The settings are their own form, so Send joins the prompt's form by id. */}
      <div class="composer">
        <form id={`prompt-${props.session.id}`} onSubmit={send}>
          <textarea name="text" rows="3" placeholder="Prompt" required />
        </form>
        <div class="row">
          <SettingsForm session={props.session} attempt={attempt} />
          <button type="submit" form={`prompt-${props.session.id}`}>
            Send
          </button>
        </div>
      </div>
    </>
  );
}

// NewSessionView writes a workspace's next session: its first prompt and
// settings. Sending creates the session and starts it on the prompt.
export function NewSessionView(props: { workspaceId: string; onStarted(id: string): void }) {
  const ox = useOx();
  const [error, setError] = createSignal<string>();
  const workspace = () => ox.state.workspaces.find((w) => w.id === props.workspaceId)?.path;
  // The harness chosen in the form; its choices fill the settings fields.
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
      <p>{workspace()} · new session</p>
      <div class="log" />
      <Show when={error()}>{(error) => <p role="alert">{error()}</p>}</Show>
      <form class="composer" onSubmit={send}>
        <textarea name="text" rows="3" placeholder="Prompt" required autofocus />
        <div class="row">
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
          <button type="submit">Send</button>
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

// ToolCall shows a tool call as its name and first string input, expanding
// to its full input and result.
function ToolCall(props: { tool: Tool }) {
  const summary = () => {
    const first = Object.values(props.tool.input).find((v) => typeof v === "string") ?? "";
    const status = props.tool.result ? (props.tool.result.is_error ? " (error)" : "") : " …";
    return `${props.tool.name} ${first.split("\n")[0].slice(0, 120)}${status}`;
  };
  return (
    <details class={{ error: !!props.tool.result?.is_error }}>
      <summary>{summary()}</summary>
      <pre>{JSON.stringify(props.tool.input, null, 2)}</pre>
      <pre>{props.tool.result?.text}</pre>
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

// SettingsForm sends only the settings the user changed, with the effort a
// model change requires. It shows the change while it is being applied,
// until the session/configured event confirms it or the request fails and
// the fields return to the stored settings.
function SettingsForm(props: { session: Session; attempt(request: Promise<unknown>): void }) {
  const ox = useOx();
  const [shown, setShown] = createOptimistic(() => props.session.settings);
  const [configuring, setConfiguring] = createOptimistic(false);
  const configure = action(function* (settings: Settings, changes: Partial<Settings>) {
    setShown(settings);
    setConfiguring(true);
    yield ox.configure(props.session.id, changes);
  });
  // A running session takes configuration only while idle; a parked session
  // stores it.
  const configurable = () => !configuring() && (props.session.state === "idle" || props.session.state === "parked");

  return (
    <Show when={ox.state.harnesses[props.session.harness]}>
      {(harness) => (
        <form
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
