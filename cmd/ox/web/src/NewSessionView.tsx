import type { JSX } from "@solidjs/web";
import { createSignal, Show } from "solid-js";
import { AttachButton, createImageDrop, pasteImages, Thumbnails } from "./Attachments";
import { newID } from "./id";
import { useOx } from "./ox";
import type { Defaults } from "./protocol";
import { keepPromptFocus, SubmittedPrompt, sendOnEnter } from "./SessionView";
import { defaultPermissionMode, type ModelChoice, ModelPicker, Select } from "./SettingsFields";

// A submitted draft lives on the page through navigation and reconnect. Its
// composer belongs to the same control that the new session will use.
export function createDraft(workspaceId: string, ox: ReturnType<typeof useOx>) {
  const id = newID();
  const [chosen, setChoice] = createSignal<Defaults>();
  const choice = () => chosen() ?? ox.state.defaults;
  const [worktree, setWorktree] = createSignal(false);
  return { workspaceId, id, control: ox.control(id), choice, setChoice, worktree, setWorktree };
}

// locations are where a new session runs: the workspace directory, or a
// worktree of its own.
const locations = [
  { id: "local", name: "Local" },
  { id: "worktree", name: "Worktree" },
];

export type Draft = ReturnType<typeof createDraft>;

export function NewSessionView(props: { draft: Draft; onCreated(): void; toggles?: JSX.Element }) {
  const ox = useOx();
  const draft = props.draft;
  const workspace = () => ox.workspaces().find((w) => w.id === draft.workspaceId)?.path;
  // The prompt being created shows pending until the view moves to its session.
  const creating = () => {
    const prompt = draft.control.outgoing();
    return prompt?.phase === "creating" ? prompt : undefined;
  };
  const pick = (value: ModelChoice) => {
    const catalog = ox.state.harnesses[value.harness];
    const currentMode = draft.choice()?.permission_mode;
    const mode =
      currentMode && catalog.permission_modes.some((m) => m.id === currentMode)
        ? currentMode
        : defaultPermissionMode(value.harness, catalog, ox.state.defaults);
    draft.setChoice({ ...value, permission_mode: mode });
  };
  const create = (event: SubmitEvent) => {
    event.preventDefault();
    const choice = draft.choice();
    if (!choice) return;
    const { harness, ...settings } = choice;
    draft.control.create(draft.workspaceId, harness, settings, draft.worktree(), props.onCreated);
  };
  const drop = createImageDrop(draft.control, () => true);
  return (
    <>
      <header class="context">
        <div class="title">
          <h1>New session</h1>
          {props.toggles}
        </div>
        <p class="path">
          <span class="directory">{workspace()}</span>
        </p>
      </header>
      <div
        class="log"
        role="log"
        aria-label="Transcript"
        onDragOver={drop.handlers.onDragOver}
        onDragLeave={drop.handlers.onDragLeave}
        onDrop={drop.handlers.onDrop}
      >
        <Show when={creating()} keyed>
          {(prompt) => (
            <div class="transcript">
              <section class="queue" aria-label="Pending">
                <SubmittedPrompt prompt={prompt} />
              </section>
            </div>
          )}
        </Show>
      </div>
      <Show when={draft.control.error()}>{(error) => <p role="alert">{error()}</p>}</Show>
      {/* biome-ignore lint/a11y/noStaticElementInteractions: a drop attaches images, which Attach Image and a paste attach from the keyboard. */}
      <div
        class="composer"
        onDragOver={drop.handlers.onDragOver}
        onDragLeave={drop.handlers.onDragLeave}
        onDrop={drop.handlers.onDrop}
      >
        <form class={["box", { dragging: drop.dragging() }]} onSubmit={create} onPointerDown={keepPromptFocus}>
          <Thumbnails control={draft.control} />
          <div class="field">
            <AttachButton control={draft.control} />
            <textarea
              value={draft.control.text()}
              onInput={(event) => draft.control.setText(event.currentTarget.value)}
              name="text"
              rows="1"
              aria-label="Prompt"
              required={!draft.control.images().length}
              autofocus
              onKeyDown={sendOnEnter}
              onPaste={pasteImages(draft.control)}
            />
            <button
              type="submit"
              class="icon send"
              aria-label="Send"
              title="Send"
              disabled={!draft.control.canCreate()}
            />
          </div>
        </form>
        <Show when={draft.choice()}>
          {(choice) => (
            <div class="settings">
              <ModelPicker
                harnesses={ox.state.harnesses}
                value={choice()}
                disabled={!draft.control.canCreate()}
                onChange={pick}
              />
              <Select
                name="permission_mode"
                label="Permission mode"
                options={ox.state.harnesses[choice().harness]?.permission_modes ?? []}
                value={choice().permission_mode}
                disabled={!draft.control.canCreate()}
                onChange={(mode) => draft.setChoice({ ...choice(), permission_mode: mode })}
              />
              <Select
                name="worktree"
                label="Location"
                options={locations}
                value={draft.worktree() ? "worktree" : "local"}
                disabled={!draft.control.canCreate()}
                onChange={(location) => draft.setWorktree(location === "worktree")}
              />
            </div>
          )}
        </Show>
      </div>
    </>
  );
}
