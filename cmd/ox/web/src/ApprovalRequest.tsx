import { createSignal, createUniqueId, Show, snapshot } from "solid-js";
import { Markdown } from "./Markdown";
import { type Approval, planOf, toolInputJSON, toolSubject } from "./ox";
import type { ApprovalResponse } from "./protocol";

type Choice = "allow" | "deny" | "other";

// ApprovalRequest answers one approval with Allow, Deny, or a typed reply,
// which denies with the reply as Claude's message. Typing picks Other, and
// picking a choice clears the reply.
export function ApprovalRequest(props: { approval: Approval; onAnswer(response: ApprovalResponse): Promise<unknown> }) {
  const id = createUniqueId();
  const [choice, setChoice] = createSignal<Choice>();
  const [reply, setReply] = createSignal("");
  const [sending, setSending] = createSignal(false);
  let field!: HTMLInputElement;
  const plan = () => planOf(props.approval.request.tool_name, props.approval.request.input);
  const ready = () => choice() === "allow" || choice() === "deny" || (choice() === "other" && !!reply().trim());

  const pick = (picked: Choice) => {
    setChoice(picked);
    if (picked === "other") field.focus();
    else setReply("");
  };
  const submit = () => {
    if (!ready() || sending()) return;
    const response: ApprovalResponse =
      choice() === "allow"
        ? { behavior: "allow", updatedInput: snapshot(props.approval.request.input) }
        : { behavior: "deny", message: choice() === "other" ? reply().trim() : "Denied by user" };
    setSending(true);
    props.onAnswer(response).finally(() => setSending(false));
  };

  const radio = (value: Choice, label?: string) => (
    <input
      type="radio"
      id={`${id}-${value}`}
      name={id}
      checked={choice() === value}
      disabled={sending()}
      aria-label={label}
      onChange={() => pick(value)}
    />
  );

  return (
    <div class="approval">
      <p>Allow {props.approval.request.tool_name}?</p>
      <Show when={plan()}>{(text) => <Markdown class="plan" text={text()} />}</Show>
      {/* The subject summarizes the input; expanding shows all of it. */}
      <details class="input">
        <summary>
          <span class="subject">{plan() ? "Input" : toolSubject(props.approval.request.input) || "Input"}</span>
          <span class="icon chevron-down" />
        </summary>
        <pre>{toolInputJSON(props.approval.request.input)}</pre>
      </details>
      <div class="choices" role="radiogroup" aria-label="Choices">
        <div class="choice">
          {radio("allow")}
          <label for={`${id}-allow`}>Allow</label>
        </div>
        <div class="choice">
          {radio("deny")}
          <label for={`${id}-deny`}>Deny</label>
        </div>
        <div class="choice">
          {radio("other", "Other")}
          <input
            ref={field}
            type="text"
            placeholder="Other"
            aria-label="Other response"
            value={reply()}
            disabled={sending()}
            onInput={(event) => {
              setReply(event.currentTarget.value);
              setChoice("other");
            }}
            onKeyDown={(event) => {
              if (event.key === "Enter" && !event.isComposing) submit();
            }}
          />
        </div>
      </div>
      <div class="actions">
        <button type="button" class="primary" disabled={!ready() || sending()} onClick={submit}>
          Submit
        </button>
      </div>
    </div>
  );
}
