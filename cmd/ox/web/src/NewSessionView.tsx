import { createSignal, Show } from "solid-js";
import { createImageDrop } from "./Attachments";
import { PromptBox } from "./Composer";
import { newID } from "./id";
import { useOx } from "./ox";
import type { Defaults } from "./protocol";
import { SubmittedPrompt } from "./SessionView";
import { type ModelChoice, ModelPicker, Select } from "./SettingsFields";

// A submitted draft lives on the page through navigation and reconnect. Its
// composer belongs to the same control that the new session will use.
export function createDraft(workspaceId: string, ox: ReturnType<typeof useOx>) {
  const id = newID();
  const [chosen, setChoice] = createSignal<Defaults>();
  const choice = () => chosen() ?? ox.state.defaults;
  const [worktree, setWorktree] = createSignal(true);
  return { workspaceId, id, control: ox.control(id), choice, setChoice, worktree, setWorktree };
}

// locations are where a new session runs: the workspace directory, or a
// worktree of its own.
const locations = [
  { id: "local", name: "Local" },
  { id: "worktree", name: "Worktree" },
];

export type Draft = ReturnType<typeof createDraft>;

export function NewSessionView(props: { draft: Draft; onCreated(): void }) {
  const ox = useOx();
  const draft = props.draft;
  // The prompt being created shows pending until the view moves to its session.
  const creating = () => {
    return draft.control.creating() ? draft.control.outgoing() : undefined;
  };
  // A draft keeps its permission mode through a change of harness, since
  // every harness takes every mode.
  const pick = (value: ModelChoice) => {
    const choice = draft.choice();
    if (choice) draft.setChoice({ ...choice, ...value });
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
        <PromptBox control={draft.control} onSubmit={create} dragging={drop.dragging()} autofocus>
          <button
            type="submit"
            class="icon send"
            aria-label="Send"
            title="Send"
            disabled={!draft.control.canCreate()}
          />
        </PromptBox>
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
                options={ox.state.permission_modes}
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
