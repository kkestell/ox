import type { JSX } from "@solidjs/web";
import { AttachButton, pasteImages, Thumbnails } from "./Attachments";
import type { SessionControl } from "./sessionControl";

function sendOnEnter(event: KeyboardEvent & { currentTarget: HTMLTextAreaElement }) {
  // A phone keyboard has no Shift+Return, so Return there starts a new line and Send sends.
  if (matchMedia("(pointer: coarse)").matches) return;
  if (event.key === "Enter" && !event.shiftKey && !event.isComposing) {
    event.preventDefault();
    if (!event.repeat) event.currentTarget.form?.requestSubmit();
  }
}

// keepPromptFocus leaves focus in the prompt when the user presses a button in
// the composer's box, as a chat app keeps its keyboard up after Send. The
// composer's inset follows the prompt's focus, so a button that took focus
// would move under the pointer before the click.
export function keepPromptFocus(event: PointerEvent) {
  if (!(event.target instanceof HTMLTextAreaElement)) event.preventDefault();
}

// PromptBox is the composer's box: attached images, the attach button, the
// prompt, and the action button its children supply.
export function PromptBox(props: {
  control: SessionControl;
  onSubmit: (event: SubmitEvent) => void;
  dragging: boolean;
  autofocus?: boolean;
  children: JSX.Element;
}) {
  return (
    <form class={["box", { dragging: props.dragging }]} onSubmit={props.onSubmit} onPointerDown={keepPromptFocus}>
      <Thumbnails control={props.control} />
      <div class="field">
        <AttachButton control={props.control} />
        <textarea
          name="text"
          rows="1"
          aria-label="Prompt"
          required={!props.control.images().length}
          autofocus={props.autofocus}
          value={props.control.text()}
          onInput={(event) => props.control.setText(event.currentTarget.value)}
          onKeyDown={sendOnEnter}
          onPaste={pasteImages(props.control)}
        />
        {props.children}
      </div>
    </form>
  );
}
