import { createSignal, Show, snapshot } from "solid-js";
import type { Approval } from "./ox";
import type { ApprovalResponse } from "./protocol";

export function ApprovalRequest(props: { approval: Approval; onAnswer(response: ApprovalResponse): void }) {
  const [message, setMessage] = createSignal("");
  const plan = () => {
    const request = props.approval.request;
    return request.tool_name === "ExitPlanMode" && typeof request.input.plan === "string"
      ? request.input.plan
      : undefined;
  };
  const deny = (interrupt: boolean) =>
    props.onAnswer({ behavior: "deny", message: message().trim() || "Denied by user", interrupt });

  return (
    <fieldset>
      <legend>{props.approval.request.tool_name}</legend>
      <Show when={plan() !== undefined} fallback={<pre>{JSON.stringify(props.approval.request.input, null, 2)}</pre>}>
        <div class="plan">{plan()}</div>
      </Show>
      <label class="denial-message">
        Reason for denial (optional)
        <input type="text" value={message()} onInput={(event) => setMessage(event.currentTarget.value)} />
      </label>
      <button
        type="button"
        class="primary"
        onClick={() => props.onAnswer({ behavior: "allow", updatedInput: snapshot(props.approval.request.input) })}
      >
        Allow
      </button>
      <button type="button" onClick={() => deny(false)}>
        Deny
      </button>
      <button type="button" onClick={() => deny(true)}>
        Deny and stop
      </button>
    </fieldset>
  );
}
