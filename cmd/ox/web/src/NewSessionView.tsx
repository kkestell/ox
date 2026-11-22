import type { JSX } from "@solidjs/web";
import { createSignal, Show } from "solid-js";
import { useOx } from "./ox";
import type { Defaults } from "./protocol";
import { sendOnEnter } from "./SessionView";
import { defaultPermissionMode, type ModelChoice, ModelPicker, Select } from "./SettingsFields";

// A submitted draft lives on the page through navigation and reconnect. Its
// composer belongs to the same control that the new session will use.
export function createDraft(workspaceId: string, ox: ReturnType<typeof useOx>) {
  const id = Array.from(crypto.getRandomValues(new Uint8Array(16)), (n) => n.toString(16).padStart(2, "0")).join("");
  const [chosen, setChoice] = createSignal<Defaults>();
  const choice = () => chosen() ?? ox.state.defaults;
  return { workspaceId, id, control: ox.control(id), choice, setChoice };
}

export type Draft = ReturnType<typeof createDraft>;

export function NewSessionView(props: { draft: Draft; onCreated(): void; toggles?: JSX.Element }) {
  const ox = useOx();
  const draft = props.draft;
  const workspace = () => ox.state.workspaces.find((w) => w.id === draft.workspaceId)?.path;
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
    draft.control.create(draft.workspaceId, harness, settings, props.onCreated);
  };
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
      <div class="log" />
      <Show when={draft.control.error()}>{(error) => <p role="alert">{error()}</p>}</Show>
      <div class="composer">
        <form class="box" onSubmit={create}>
          <textarea
            value={draft.control.text()}
            onInput={(event) => draft.control.setText(event.currentTarget.value)}
            name="text"
            rows="1"
            aria-label="Prompt"
            required
            autofocus
            onKeyDown={sendOnEnter}
          />
          <button
            type="submit"
            class="icon send"
            aria-label="Send"
            title="Send"
            disabled={!draft.control.canCreate()}
          />
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
            </div>
          )}
        </Show>
      </div>
    </>
  );
}
