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

/** A slash command advertised by the current agent. */
export interface Command {
  type: "command";
  name: string;
  description: string;
  input?: { type: string; hint?: string } | null;
}

export interface WorkspaceInfo {
  id: string;
  path: string;
  /** Whether sessions can run in a worktree of the workspace. */
  git_working_tree: boolean;
}

/**
 * Where a new session runs: the workspace's directory, or a new worktree on
 * an `ox/<id8>` branch from `HEAD`, compared with the ref `compare`.
 */
export type Checkout = { type: "workspace" } | { type: "worktree"; compare: string };

export interface WorktreeInfo {
  path: string;
  branch: string;
  base: string;
  created_branch: boolean;
  /** The ref the branch is compared with. */
  compare: string | null;
}

/**
 * How a session's branch compares with its comparison ref: the two tips and
 * the commits each has that the other lacks, or git's error.
 */
export type Comparison =
  | { type: "compared"; tip: string; compare_tip: string; ahead: number; behind: number }
  | { type: "failed"; error: string };

/** A workspace's branches, as the UI reads them. */
export interface Branches {
  /** The branch the workspace's checkout is on. */
  head: string | null;
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
  available_commands: Command[];
  worktree: WorktreeInfo | null;
  comparison: Comparison | null;
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
