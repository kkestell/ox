import type { JSX } from "@solidjs/web";
import { For, Match, onCleanup, Show, Switch, untrack } from "solid-js";
import { ApprovalRequest } from "./ApprovalRequest";
import { AttachButton, createImageDrop, pasteImages, Thumbnails } from "./Attachments";
import { Markdown } from "./Markdown";
import { type Entry, type Outgoing, type Session, type SessionControl, type SessionSnapshot, useOx } from "./ox";
import type { Prompt, Settings } from "./protocol";
import { ModelPicker, Select } from "./SettingsFields";
import { planOf, toolInputJSON, toolLabel, toolSummary } from "./toolInput";

type Tool = Extract<Entry, { kind: "tool" }>;

export function sendOnEnter(event: KeyboardEvent & { currentTarget: HTMLTextAreaElement }) {
  // A phone keyboard has no Shift+Return, so Return there starts a new line and Send sends.
  if (matchMedia("(pointer: coarse)").matches) return;
  if (event.key === "Enter" && !event.shiftKey && !event.isComposing) {
    event.preventDefault();
    if (!event.repeat) event.currentTarget.form?.requestSubmit();
  }
}

// keepRowInPlace opens or closes the tool call whose summary was clicked
// and scrolls the transcript so the summary stays where it was on screen. The
// transcript is anchored at its bottom, so without this a row opened at the
// bottom unfolds upward and pushes its summary out of view.
function keepRowInPlace(event: MouseEvent & { currentTarget: HTMLElement }) {
  const summary = (event.target as Element).closest("summary");
  const details = summary?.parentElement;
  if (!summary || !(details instanceof HTMLDetailsElement)) return;
  event.preventDefault();
  const top = summary.getBoundingClientRect().top;
  details.open = !details.open;
  event.currentTarget.scrollTop += summary.getBoundingClientRect().top - top;
}

export function SessionView(props: { session: Session; snapshot: SessionSnapshot; toggles?: JSX.Element }) {
  const ox = useOx();
  // Layout keys this view by session, so the control is stable for its lifetime.
  const control = ox.control(untrack(() => props.session.id));
  const directory = () => props.session.child?.directory ?? ox.workspaceOf(props.session.id)?.path;
  // A stop sent before oxd reports a prompt running can overtake the send.
  const running = () => props.session.state === "running" || props.session.state === "requires_action";
  const archived = () => ox.archived(props.session);
  // A child of an archived session is unarchived with its parent.
  const parent = () => props.session.child && ox.session(props.session.child.parent_id);
  const parentArchived = () => {
    const p = parent();
    return p !== undefined && ox.archived(p);
  };
  const send = (event: SubmitEvent) => {
    event.preventDefault();
    control.send();
  };
  const drop = createImageDrop(control, () => !archived());
  const answering = (approvalId: string) => control.answering().has(approvalId);

  return (
    <>
      <header class="context">
        <div class="title">
          <h1>{props.session.label}</h1>
          {props.toggles}
        </div>
        <p class="path">
          <span class="directory">{directory()}</span>
        </p>
      </header>
      <div
        class="log"
        role="log"
        aria-label="Transcript"
        onClick={keepRowInPlace}
        onDragOver={drop.handlers.onDragOver}
        onDragLeave={drop.handlers.onDragLeave}
        onDrop={drop.handlers.onDrop}
      >
        <div class="transcript">
          <Transcript sessionId={props.session.id} snapshot={props.snapshot} outgoing={control.outgoing()} />
        </div>
      </div>
      <Show when={control.error()}>{(error) => <p role="alert">{error()}</p>}</Show>
      <Show when={control.notice()}>{(notice) => <p role="status">{notice()}</p>}</Show>
      <Show
        when={!archived()}
        fallback={
          <div class="composer archived">
            <p>{parentArchived() ? "This session is archived with its parent." : "This session is archived."}</p>
            <button
              type="button"
              disabled={!ox.ready() || parent()?.archived === true || ox.busy(props.session.id)}
              onClick={() => control.unarchive()}
            >
              Unarchive
            </button>
          </div>
        }
      >
        {/* biome-ignore lint/a11y/noStaticElementInteractions: a drop attaches images, which Attach Image and a paste attach from the keyboard. */}
        <div
          class="composer"
          onDragOver={drop.handlers.onDragOver}
          onDragLeave={drop.handlers.onDragLeave}
          onDrop={drop.handlers.onDrop}
        >
          {/* An approval being answered stays mounted, hidden, so a refused
              answer brings it back as the user left it. */}
          <Show when={props.snapshot.approvals.length}>
            <section
              class="pending"
              aria-label="Waiting on you"
              hidden={props.snapshot.approvals.every((a) => answering(a.id))}
            >
              <For each={props.snapshot.approvals}>
                {(approval) => (
                  <ApprovalRequest
                    approval={approval}
                    hidden={answering(approval.id)}
                    onAnswer={(response) => control.approve(approval.id, response)}
                  />
                )}
              </For>
            </section>
          </Show>
          <form class={["box", { dragging: drop.dragging() }]} onSubmit={send}>
            <Thumbnails control={control} />
            <div class="field">
              <AttachButton control={control} />
              <textarea
                name="text"
                rows="1"
                aria-label="Prompt"
                required={!control.images().length}
                value={control.text()}
                onInput={(event) => control.setText(event.currentTarget.value)}
                onKeyDown={sendOnEnter}
                onPaste={pasteImages(control)}
              />
              <Show when={running() && !control.stopping()}>
                <button
                  type="button"
                  class="icon stop"
                  aria-label="Stop"
                  title="Stop"
                  onClick={() => control.interrupt()}
                />
              </Show>
              <button type="submit" class="icon send" aria-label="Send" title="Send" disabled={!control.canSend()} />
            </div>
          </form>
          <SettingsForm harness={props.session.harness} settings={props.snapshot.settings} control={control} />
        </div>
      </Show>
    </>
  );
}

// Transcript shows the conversation followed by every pending input, in order,
// ending with the page's prompt in flight until oxd reports it.
function Transcript(props: { sessionId: string; snapshot: SessionSnapshot; outgoing?: Outgoing }) {
  const submitted = () => {
    const prompt = props.outgoing;
    if (!prompt || prompt.phase === "sent") return;
    const reported =
      props.snapshot.queued.some((p) => p.message_id === prompt.id) ||
      props.snapshot.entries.some((e) => e.kind === "prompt" && e.input.message_id === prompt.id);
    return reported ? undefined : prompt;
  };
  return (
    <>
      <For each={props.snapshot.entries}>
        {(entry) => (
          <Switch>
            <Match when={entry.kind === "prompt" && entry}>
              {(prompt) => <PromptBubble sessionId={props.sessionId} input={prompt().input} />}
            </Match>
            <Match when={entry.kind === "reply" && entry}>
              {(reply) => (
                <article aria-label="Reply">
                  <Markdown text={reply().text} />
                </article>
              )}
            </Match>
            <Match when={entry.kind === "reasoning" && entry}>
              {(reasoning) => (
                <article class="reasoning" aria-label="Thinking">
                  <Markdown text={reasoning().text} />
                </article>
              )}
            </Match>
            <Match when={entry.kind === "tool" && entry}>{(tool) => <ToolCall tool={tool()} />}</Match>
            {/* A successful result repeats the reply, so only a failed turn shows its result. */}
            <Match when={entry.kind === "result" && entry.result.is_error && entry}>
              {(result) => (
                <article class="error" aria-label="Error">
                  {result().result.text}
                </article>
              )}
            </Match>
            <Match when={entry.kind === "exited" && entry}>
              {(exited) => (
                <article class="error" aria-label="Error">
                  {exited().error}
                </article>
              )}
            </Match>
          </Switch>
        )}
      </For>
      <Show when={props.snapshot.queued.length || submitted()}>
        <section class="queue" aria-label="Pending">
          <For each={props.snapshot.queued}>
            {(input) => <PromptBubble sessionId={props.sessionId} input={input} queued />}
          </For>
          <Show when={submitted()} keyed>
            {(prompt) => <SubmittedPrompt prompt={prompt} />}
          </Show>
        </section>
      </Show>
    </>
  );
}

// SubmittedPrompt is a prompt the page has sent and oxd has not reported,
// with its images from the composer.
export function SubmittedPrompt(props: { prompt: Outgoing }) {
  return (
    <PromptBubble
      sessionId=""
      input={{ message_id: props.prompt.id, text: props.prompt.text }}
      files={props.prompt.images}
      queued
    />
  );
}

// PromptBubble shows a prompt and its images, stored or local, a message
// headed with its sender's label, or child reports each headed with its
// child's label.
function PromptBubble(props: { sessionId: string; input: Prompt; files?: File[]; queued?: boolean }) {
  return (
    <Show when={props.input.reports} fallback={<MessageBubble {...props} />}>
      {(reports) => (
        <article class={["prompt", "message", "reports", { queued: props.queued }]} aria-label="Child reports">
          <For each={reports()}>
            {(report) => (
              <section aria-label={`Report from ${report.from_label}`}>
                <div class="from">{report.from_label}</div>
                {report.text}
              </section>
            )}
          </For>
        </article>
      )}
    </Show>
  );
}

function MessageBubble(props: { sessionId: string; input: Prompt; files?: File[]; queued?: boolean }) {
  return (
    <article
      class={["prompt", { queued: props.queued, message: props.input.from !== undefined }]}
      aria-label={props.input.from === undefined ? "Prompt" : `Message from ${props.input.from_label}`}
    >
      <Show when={props.input.from !== undefined}>
        <div class="from">{props.input.from_label}</div>
      </Show>
      {props.input.text}
      <Show when={props.input.images?.length || props.files?.length}>
        <div class="images">
          <For each={props.input.images}>
            {(image) => <img src={`/images/${props.sessionId}/${image}`} alt="Attachment" />}
          </For>
          <For each={props.files}>{(file) => <LocalImage file={file} />}</For>
        </div>
      </Show>
    </article>
  );
}

// LocalImage shows an image file the page holds.
function LocalImage(props: { file: File }) {
  const url = URL.createObjectURL(untrack(() => props.file));
  onCleanup(() => URL.revokeObjectURL(url));
  return <img src={url} alt="Attachment" />;
}

function ToolCall(props: { tool: Tool }) {
  const detail = () => toolSummary(props.tool.name, props.tool.input);
  const status = () => {
    if (props.tool.result) return props.tool.result.is_error ? "failed" : "done";
    return props.tool.stopped ? "stopped" : "running";
  };
  return (
    <details class="tool" aria-label={toolLabel(props.tool.name)}>
      <summary>
        <span class={["marker", status()]} role="img" title={status()} aria-label={status()} />
        <span class="name">{toolLabel(props.tool.name)}</span> <span class="detail">{detail()}</span>
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
        onChange={(value) =>
          value.model !== shown().model
            ? props.control.configure({ model: value.model })
            : props.control.configure({ effort: value.effort })
        }
      />
      <Select
        name="permission_mode"
        label="Permission mode"
        options={harness().permission_modes}
        value={shown().permission_mode}
        disabled={!props.control.canConfigure()}
        pending={pending("permission_mode")}
        onChange={(mode) => props.control.configure({ permission_mode: mode })}
      />
    </div>
  );
}
