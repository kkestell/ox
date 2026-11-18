import { For } from "solid-js";
import { efforts, models, permissionModes, type Settings } from "./protocol";

// SettingsFields chooses a session's model, effort, and permission mode,
// read back with settingsFrom.
export function SettingsFields(props: { value: Settings; disabled?: boolean }) {
  return (
    <>
      <Select name="model" options={models} value={props.value.model} disabled={props.disabled} />
      <Select name="effort" options={efforts} value={props.value.effort} disabled={props.disabled} />
      <Select name="permission_mode" options={permissionModes} value={props.value.permission_mode} disabled={props.disabled} />
    </>
  );
}

function Select(props: { name: string; options: string[]; value: string; disabled?: boolean }) {
  return (
    <select name={props.name} disabled={props.disabled}>
      <For each={props.options}>{(option) => <option selected={option === props.value}>{option}</option>}</For>
    </select>
  );
}

export function settingsFrom(form: FormData): Settings {
  return {
    model: String(form.get("model")),
    effort: String(form.get("effort")),
    permission_mode: String(form.get("permission_mode")),
  };
}
