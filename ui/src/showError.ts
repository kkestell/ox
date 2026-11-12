import { message } from "@tauri-apps/plugin-dialog";

/** Shows an action's error in a message box. */
export function showError(error: unknown) {
  return message(String(error), { kind: "error" });
}
