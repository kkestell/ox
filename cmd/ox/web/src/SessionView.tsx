import { For, Match, Show, Switch, untrack } from "solid-js";
import { ApprovalRequest } from "./ApprovalRequest";
import { Markdown } from "./Markdown";
import { type Entry, type Session, type SessionControl, type SessionSnapshot, sessionLabel, useOx } from "./ox";
import type { Prompt, Settings } from "./protocol";
import { ModelPicker, Select } from "./SettingsFields";
import { planOf, toolInputJSON, toolSubject } from "./toolInput";

type Tool = Extract<Entry, { kind: "tool" }>;

export function sendOnEnter(event: KeyboardEvent & { currentTarget: HTMLTextAreaElement }) {
  if (event.key === "Enter" && !event.shiftKey && !event.isComposing) {
    event.preventDefault();
    if (!event.repeat) event.currentTarget.form?.requestSubmit();
  }
}

export function SessionView(props: { session: Session; snapshot: SessionSnapshot }) {
  const ox = useOx();
  // Layout keys this view by session, so the control is stable for its lifetime.
  const control = ox.control(untrack(() => props.session.id));
  const workspace = () => ox.workspaceOf(props.session.id)?.path;
  // A stop sent before oxd reports a prompt running can overtake the send.
  const running = () => props.snapshot.state === "running" || props.snapshot.state === "requires_action";
  const send = (event: SubmitEvent) => {
    event.preventDefault();
    control.send(control.text());
  };

  return (
    <>
      <header class="context">
        <h1>{sessionLabel(props.session)}</h1>
        <p class="path">{workspace()}</p>
      </header>
      <div class="log">
        <div class="transcript">
          <Transcript snapshot={props.snapshot} />
        </div>
      </div>
      <Show when={control.error()}>{(error) => <p role="alert">{error()}</p>}</Show>
      <Show when={control.notice()}>{(notice) => <p role="status">{notice()}</p>}</Show>
      <div class="composer">
        <Show when={props.snapshot.approvals.length}>
          <section class="pending" aria-label="Waiting on you">
            <For each={props.snapshot.approvals}>
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
          <Show when={running()}>
            <button
              type="button"
              class="icon stop"
              aria-label="Stop"
              title="Stop"
              onClick={() => control.interrupt()}
            />
          </Show>
          <button type="submit" class="icon send" aria-label="Send" title="Send" disabled={!control.canSend()} />
        </form>
        <SettingsForm harness={props.session.harness} settings={props.snapshot.settings} control={control} />
      </div>
    </>
  );
}

// Transcript shows the conversation followed by every pending input, in order.
function Transcript(props: { snapshot: SessionSnapshot }) {
  return (
    <>
      <For each={props.snapshot.entries}>
        {(entry) => (
          <Switch>
            <Match when={entry.kind === "prompt" && entry}>{(prompt) => <PromptBubble input={prompt().input} />}</Match>
            <Match when={entry.kind === "reply" && entry}>
              {(reply) => <Markdown class="reply" text={reply().text} />}
            </Match>
            <Match when={entry.kind === "tool" && entry}>{(tool) => <ToolCall tool={tool()} />}</Match>
            {/* A successful result repeats the reply, so only a failed turn shows its result. */}
            <Match when={entry.kind === "result" && entry.result.is_error && entry}>
              {(result) => <div class="error">{result().result.text}</div>}
            </Match>
            <Match when={entry.kind === "exited" && entry}>
              {(exited) => <div class="error">{exited().error}</div>}
            </Match>
          </Switch>
        )}
      </For>
      <For each={props.snapshot.queued}>{(input) => <PromptBubble input={input} queued />}</For>
    </>
  );
}

// PromptBubble shows a prompt, or a message headed with its sender's label.
function PromptBubble(props: { input: Prompt; queued?: boolean }) {
  const ox = useOx();
  const senderLabel = (id: string) => {
    if (props.input.from_label !== undefined) return props.input.from_label;
    const sender = ox.session(id);
    return sender ? sessionLabel(sender) : id.slice(0, 8);
  };
  return (
    <div class={["prompt", { queued: props.queued, message: props.input.from !== undefined }]}>
      <Show when={props.input.from}>{(from) => <div class="from">{senderLabel(from())}</div>}</Show>
      {props.input.text}
    </div>
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
        <Show when={planOf(props.tool.name, props.tool.input)}>
          {(plan) => <Markdown class="plan" text={plan()} />}
        </Show>
        <pre>{toolInputJSON(props.tool.input)}</pre>
        <Show when={props.tool.result}>{(result) => <pre>{result().text}</pre>}</Show>
      </div>
    </details>
  );
}

// SettingsForm changes a session's settings through its control, which shows
// a change as pending while it is in flight and the stored settings once it
// settles.
function SettingsForm(props: { harness: string; settings: Settings; control: SessionControl }) {
  const ox = useOx();
  const shown = () => ({ ...props.settings, ...props.control.requested() });
  const pending = (key: keyof Settings) => props.control.requested()?.[key] !== undefined;
  // A harness the configuration no longer offers has no choices, and its
  // session still shows the settings it has.
  const harness = () => ox.state.harnesses[props.harness] ?? { models: [], permission_modes: [] };

  // Once the session starts, the picker offers only its harness's models.
  return (
    <div class="settings">
      <ModelPicker
        harnesses={{ [props.harness]: harness() }}
        value={{ harness: props.harness, model: shown().model, effort: shown().effort }}
        disabled={!props.control.canConfigure()}
        pending={pending("model") || pending("effort")}
        onChange={(value) => props.control.configure({ model: value.model, effort: value.effort })}
      />
      <Select
        name="permission_mode"
        options={harness().permission_modes}
        value={shown().permission_mode}
        disabled={!props.control.canConfigure()}
        pending={pending("permission_mode")}
        onChange={(mode) => props.control.configure({ permission_mode: mode })}
      />
    </div>
  );
}
