import { createSignal, For, Show } from "solid-js";
import type { Defaults, Harness, Option, Settings } from "./protocol";

// SettingsFields chooses a session's model, effort, and permission mode from
// its harness's choices, read back with settingsFrom. The effort choices
// follow the model chosen in the form; a model without effort levels shows
// none.
export function SettingsFields(props: { harness: Harness; value: Settings; disabled?: boolean }) {
  const [model, setModel] = createSignal(() => props.value.model);
  const efforts = () => effortsOf(props.harness, model());
  return (
    <>
      <Select
        name="model"
        options={props.harness.models}
        value={props.value.model}
        disabled={props.disabled}
        onChange={setModel}
      />
      <Show when={efforts().length}>
        <Select name="effort" options={efforts()} value={props.value.effort} disabled={props.disabled} />
      </Show>
      <Select name="permission_mode" options={props.harness.modes} value={props.value.permission_mode} disabled={props.disabled} />
    </>
  );
}

// Select offers options by name. A value missing from them, such as one
// removed from the configuration, is offered by its ID.
function Select(props: {
  name: string;
  options: Option[];
  value: string;
  disabled?: boolean;
  onChange?(id: string): void;
}) {
  const options = () =>
    props.value && !props.options.some((o) => o.id === props.value)
      ? [...props.options, { id: props.value, name: props.value }]
      : props.options;
  return (
    <select name={props.name} disabled={props.disabled} onChange={(event) => props.onChange?.(event.currentTarget.value)}>
      <For each={options()}>
        {(option) => (
          <option value={option.id} selected={option.id === props.value}>
            {option.name}
          </option>
        )}
      </For>
    </select>
  );
}

function effortsOf(harness: Harness, model: string): Option[] {
  return harness.models.find((m) => m.id === model)?.efforts ?? [];
}

// settingsFrom reads the fields back. An effort the chosen model does not
// take, left over from the previous model, becomes the model's first.
export function settingsFrom(harness: Harness, form: FormData): Settings {
  const model = String(form.get("model"));
  const efforts = effortsOf(harness, model);
  const effort = String(form.get("effort") ?? "");
  return {
    model,
    effort: efforts.some((e) => e.id === effort) ? effort : (efforts[0]?.id ?? ""),
    permission_mode: String(form.get("permission_mode")),
  };
}

// changedSettings returns the settings in to that differ from those in from.
export function changedSettings(from: Settings, to: Settings): Partial<Settings> {
  return Object.fromEntries(Object.entries(to).filter(([key, value]) => from[key as keyof Settings] !== value));
}

// defaultSettings are a new session's settings: the configured defaults for
// the default harness, and the first choices for any other.
export function defaultSettings(name: string, harness: Harness, defaults?: Defaults): Settings {
  if (defaults?.harness === name) {
    return { model: defaults.model, effort: defaults.effort, permission_mode: defaults.mode };
  }
  const model = harness.models[0];
  return { model: model?.id ?? "", effort: model?.efforts[0]?.id ?? "", permission_mode: harness.modes[0]?.id ?? "" };
}
