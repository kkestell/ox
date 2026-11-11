// The messages the backend sends, and the fields of oxd's types the UI reads.

export type SessionState = "starting" | "idle" | "running" | "awaiting_permission" | "exited";

export interface SelectOption {
  value: string;
  name: string;
}

export interface SelectGroup {
  group: string;
  name: string;
  options: SelectOption[];
}

export type ConfigOption = { id: string; name: string } & (
  | { type: "select"; currentValue: string; options: SelectOption[] | SelectGroup[] }
  | { type: "boolean"; currentValue: boolean }
);

export interface SessionInfo {
  id: string;
  cwd: string;
  state: SessionState;
  title: string | null;
  config_options: ConfigOption[];
}

export interface PermissionOption {
  optionId: string;
  name: string;
  kind: "allow_once" | "allow_always" | "reject_once" | "reject_always";
}

export type Item =
  | { type: "prompt"; key: string; text: string; attachments: string[] }
  | { type: "text"; key: string; text: string }
  | { type: "thought"; key: string; text: string }
  | { type: "tool_call"; key: string; title: string; kind: string; status: string }
  | {
      type: "permission";
      key: string;
      request_id: string;
      title: string;
      options: PermissionOption[];
      outcome: string | null;
    }
  | { type: "turn_error"; key: string; message: string }
  | { type: "agent_exited"; key: string; error: string | null }
  | { type: "session_closed"; key: string };

export type SessionsMessage =
  | { type: "sessions"; sessions: SessionInfo[] }
  | { type: "error"; message: string }
  | { type: "disconnected" };

export type ViewMessage = { type: "item"; item: Item } | { type: "disconnected" };
