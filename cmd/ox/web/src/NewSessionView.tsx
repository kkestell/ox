import { createSignal, Show } from "solid-js";
import { createAttempt, useOx } from "./ox";
import type { Defaults } from "./protocol";
import { sendOnEnter } from "./SessionView";
import { defaultSettings, type ModelChoice, ModelPicker, Select } from "./SettingsFields";

// A submitted draft lives on the page through navigation and reconnect. Its
// composer belongs to the same control that the new session will use.
export function createDraft(workspaceId: string, ox: ReturnType<typeof useOx>) {
  const id = Array.from(crypto.getRandomValues(new Uint8Array(16)), (n) => n.toString(16).padStart(2, "0")).join("");
  const control = ox.control(id);
  const [chosen, setChoice] = createSignal<Defaults>();
  const choice = () => chosen() ?? ox.state.defaults;
  const [error, attempt] = createAttempt();
  const recover = () => control.reportError("Session recovered. Review its settings and send your prompt.");
  function submit(onCreated: () => void) {
    const c = choice();
    const text = control.text();
    if (!ox.ready() || control.creating() || !c || !text.trim()) return;
    const { harness, ...settings } = c;
    control.setCreating(true);
    const created = ox.createSession(id, workspaceId, harness, settings).then((result) => {
      // An earlier create reached oxd. Return to that session with its
      // current settings and wait for the user to submit the retained text.
      control.setCreating(false);
      if (result.existing) recover();
      else control.send(text);
      onCreated();
    });
    attempt(created).finally(() => control.setCreating(false));
  }
  return { workspaceId, id, control, choice, setChoice, error, submit, recover };
}

export type Draft = ReturnType<typeof createDraft>;

export function NewSessionView(props: { draft: Draft; onCreated(): void }) {
  const ox = useOx();
  const draft = props.draft;
  const workspace = () => ox.state.workspaces.find((w) => w.id === draft.workspaceId)?.path;
  const pick = (value: ModelChoice) => {
    const catalog = ox.state.harnesses[value.harness];
    const currentMode = draft.choice()?.permission_mode;
    const mode =
      currentMode && catalog.permission_modes.some((m) => m.id === currentMode)
        ? currentMode
        : defaultSettings(value.harness, catalog, ox.state.defaults).permission_mode;
    draft.setChoice({ ...value, permission_mode: mode });
  };
  const create = (event: SubmitEvent) => {
    event.preventDefault();
    draft.submit(props.onCreated);
  };
  return (
    <>
      <p class="context">{workspace()} · new session</p>
      <div class="log" />
      <Show when={draft.error()}>{(error) => <p role="alert">{error()}</p>}</Show>
      <form class="composer" onSubmit={create}>
        <div class="box">
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
            disabled={draft.control.creating() || !ox.ready()}
          />
        </div>
        <Show when={draft.choice()}>
          {(choice) => (
            <div class="settings">
              <ModelPicker
                harnesses={ox.state.harnesses}
                value={choice()}
                disabled={draft.control.creating() || !ox.ready()}
                onChange={pick}
              />
              <Select
                name="permission_mode"
                options={ox.state.harnesses[choice().harness]?.permission_modes ?? []}
                value={choice().permission_mode}
                disabled={draft.control.creating() || !ox.ready()}
                onChange={(mode) => draft.setChoice({ ...choice(), permission_mode: mode })}
              />
            </div>
          )}
        </Show>
      </form>
    </>
  );
}
