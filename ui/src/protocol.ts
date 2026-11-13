// The messages the backend sends, and the fields of oxd's types the UI reads.

export type SessionState =
  | "starting"
  | "idle"
  | "running"
  | "awaiting_permission"
  | "awaiting_input"
  | "exited";

export interface SelectOption {
  value: string;
  name: string;
}

export interface SelectGroup {
  group: string;
  name: string;
  options: SelectOption[];
}

export type ConfigOption = {
  configId: string;
  name: string;
  /** What the option selects, such as `model`, `thought_level`, or `mode`. */
  category?: string;
} & (
  | { type: "select"; currentValue: string; options: SelectOption[] | SelectGroup[] }
  | { type: "boolean"; currentValue: boolean }
);

export interface WorkspaceInfo {
  id: string;
  path: string;
  /** Whether sessions can run in a worktree of the workspace. */
  git_working_tree: boolean;
}

/** Where a new session runs. */
export type Checkout =
  | { type: "workspace" }
  | { type: "worktree"; branch: string | null; start: string | null };

export interface WorktreeInfo {
  path: string;
  branch: string;
  base: string;
  created_branch: boolean;
}

/** A workspace's branches, each list most recently committed first. */
export interface Branches {
  local: string[];
  /** Remote-tracking branches with their remote, such as `origin/main`. */
  remote: string[];
  fetch_error: string | null;
}

/** The outcome of deleting a session. */
export type Deletion =
  | { type: "deleted"; kept_branch: string | null }
  | { type: "unsaved_work"; message: string };

export type Archival = { type: "archived" } | { type: "unsaved_work"; message: string };

export interface SessionInfo {
  id: string;
  workspace_id: string;
  cwd: string;
  state: SessionState;
  title: string | null;
  agent_session_id: string | null;
  config_options: ConfigOption[];
  worktree: WorktreeInfo | null;
  archived: boolean;
}

export interface PermissionOption {
  optionId: string;
  name: string;
  kind: "allow_once" | "allow_always" | "reject_once" | "reject_always";
}

/** A choice in a form field: the value it sends and its label. */
export interface EnumOption {
  const: string;
  title: string;
  description?: string;
}

/** One property of an agent's form, with its JSON Schema keywords. */
export type Field = {
  name: string;
  required: boolean;
  title?: string;
  description?: string;
} & (
  | {
      type: "string";
      minLength?: number;
      maxLength?: number;
      pattern?: string;
      format?: string;
      default?: string;
      enum?: string[];
      oneOf?: EnumOption[];
    }
  | { type: "number" | "integer"; minimum?: number; maximum?: number; default?: number }
  | { type: "boolean"; default?: boolean }
  | {
      type: "array";
      minItems?: number;
      maxItems?: number;
      items: { enum?: string[]; anyOf?: EnumOption[] };
      default?: string[];
    }
);

export type ToolCallStatus = "pending" | "in_progress" | "completed" | "failed";

export type ToolContent =
  | { type: "text"; text: string }
  | { type: "image"; mime_type: string; data: string };

export type Item =
  | { type: "prompt"; key: string; text: string; attachments: string[] }
  | { type: "text"; key: string; text: string; replace: boolean }
  | { type: "thought"; key: string; text: string; replace: boolean }
  | {
      type: "tool_call";
      key: string;
      title: string;
      status: ToolCallStatus;
      /** The tool's input as JSON, or `null`. */
      raw_input: unknown;
      content: ToolContent[];
    }
  | {
      type: "permission";
      key: string;
      request_id: string;
      title: string;
      options: PermissionOption[];
      answered: boolean;
    }
  | {
      type: "elicitation";
      key: string;
      request_id: string;
      message: string;
      fields: Field[];
      answered: boolean;
    }
  | { type: "turn_error"; key: string; message: string }
  | { type: "agent_exited"; key: string; error: string | null }
  | { type: "session_closed"; key: string };

export type WatchMessage =
  | { type: "list"; workspaces: WorkspaceInfo[]; sessions: SessionInfo[] }
  | { type: "error"; message: string }
  | { type: "disconnected" };
