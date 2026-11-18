import { action, createOptimistic, createSignal, For, Match, Show, snapshot, Switch } from "solid-js";
import { useOx, type Approval, type Entry, type Session } from "./ox";
import type { ApprovalResponse, Settings } from "./protocol";
import { SettingsFields, settingsFrom } from "./SettingsFields";

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

  // The destroyed event removes the session from every page.
  const destroy = () => {
    if (confirm(`Delete session ${props.session.id} and its history?`)) attempt(ox.destroy(props.session.id));
  };

  const send = (event: SubmitEvent & { currentTarget: HTMLFormElement }) => {
    event.preventDefault();
    const form = event.currentTarget;
    const text = String(new FormData(form).get("text"));
    attempt(ox.send(props.session.id, text).then(() => form.reset()));
  };

  return (
    <>
      <p>
        {workspace()} · {props.session.id} · {props.session.state} <button onClick={destroy}>Delete</button>
      </p>
      <Transcript entries={props.session.entries} />
      <For each={props.session.approvals}>
        {(approval) => (
          <ApprovalRequest
            approval={approval}
            onAnswer={(response) => attempt(ox.approve(props.session.id, approval.id, response))}
          />
        )}
      </For>
      <form onSubmit={send}>
        <textarea name="text" rows="3" cols="60" placeholder="Prompt" required />
        <button>Send</button>
      </form>
      <SettingsForm session={props.session} attempt={attempt} />
      <Show when={error()}>{(error) => <p role="alert">{error()}</p>}</Show>
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
      <button onClick={() => props.onAnswer({ behavior: "allow", updatedInput: snapshot(props.approval.request.input) })}>
        Allow
      </button>
      <button onClick={() => props.onAnswer({ behavior: "deny", message: "Denied by user" })}>Deny</button>
    </fieldset>
  );
}

// SettingsForm shows a change to the session's settings while it is being
// applied, until the session/configured event confirms it or the request
// fails and the fields return to the stored settings.
function SettingsForm(props: { session: Session; attempt(request: Promise<unknown>): void }) {
  const ox = useOx();
  const [shown, setShown] = createOptimistic(() => props.session.settings);
  const [configuring, setConfiguring] = createOptimistic(false);
  const configure = action(function* (settings: Settings) {
    setShown(settings);
    setConfiguring(true);
    yield ox.configure(props.session.id, settings);
  });
  // Claude takes configuration only while idle; a parked session stores it.
  const configurable = () => !configuring() && (props.session.state === "idle" || props.session.state === "parked");

  return (
    <form onChange={(event) => props.attempt(configure(settingsFrom(new FormData(event.currentTarget))))}>
      <SettingsFields value={shown()} disabled={!configurable()} />
    </form>
  );
}
