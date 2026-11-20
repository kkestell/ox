// Types for the oxd protocol described in docs/protocol.md.

export type Option = { id: string; name: string };
export type Model = Option & { efforts: Option[] };
export type Harness = { models: Model[]; permission_modes: Option[] };
export type Defaults = { harness: string; model: string; effort: string; permission_mode: string };

export type Settings = { model: string; effort: string; permission_mode: string };
export type SettingsPatch = {
  [K in keyof Settings]: Pick<Settings, K> & Partial<Omit<Settings, K>>;
}[keyof Settings];

export const notFound = -32001;

// Child makes a session a child of the session parent_id.
export type Child = { parent_id: string; name: string; title: string; directory: string };

export type SessionState = "parked" | "running" | "requires_action" | "idle";

export type ToolResult = { text: string; is_error: boolean };

export type ApprovalRequest = { tool_name: string; input: Record<string, unknown> };

export type ApprovalResponse =
  | { behavior: "allow"; updatedInput: Record<string, unknown> }
  | { behavior: "deny"; message: string; interrupt?: boolean };

// Prompt is a client prompt, named by the message ID session/send returned,
// or a message from the session from.
export type Prompt = { message_id: string; text: string } & (
  | { from?: never; from_label?: never }
  | { from: string; from_label?: string }
);

export type Update = { time: string } & (
  | ({ type: "prompt" | "prompt_queued" | "prompt_cancelled" } & Prompt)
  | (OtherUpdate & { message_id?: string })
);

type OtherUpdate =
  | { type: "text_delta"; text: string }
  | { type: "tool_use"; tool_use_id: string; tool_name: string; input: Record<string, unknown> }
  | { type: "tool_result"; tool_use_id: string; result: ToolResult }
  | { type: "result"; result: ToolResult }
  | { type: "state"; state: Exclude<SessionState, "parked"> }
  | { type: "parked" }
  | { type: "approval"; request_id: string; request: ApprovalRequest }
  | { type: "approval_answered"; request_id: string }
  | { type: "approval_cancelled"; request_id: string }
  | { type: "exited"; error: string };

export type FolderEntry = { name: string; folder: boolean };

export type Notification =
  | { method: "session/update"; params: Update & { session_id: string } }
  | { method: "workspace/created"; params: { workspace_id: string; path: string; created_at: string } }
  | { method: "workspace/removed"; params: { workspace_id: string } }
  | {
      method: "session/created";
      params: { session_id: string; workspace_id: string; harness: string; created_at: string; child?: Child };
    }
  | { method: "session/configured"; params: Settings & { session_id: string } }
  | { method: "session/destroyed"; params: { session_id: string } }
  | { method: "session/archived"; params: { session_id: string } }
  | { method: "session/unarchived"; params: { session_id: string } };

type Empty = Record<string, never>;

export type Methods = {
  "harness/list": [Empty, { harnesses: Record<string, Harness>; defaults: Defaults }];
  "workspace/create": [{ path: string }, { workspace_id: string }];
  "workspace/list": [
    Empty,
    {
      workspaces: {
        workspace_id: string;
        path: string;
        created_at: string;
        sessions: {
          session_id: string;
          label: string;
          harness: string;
          created_at: string;
          child?: Child;
          archived?: boolean;
        }[];
      }[];
    },
  ];
  "workspace/remove": [{ workspace_id: string }, Empty];
  "workspace/list_folder": [{ workspace_id: string; path: string }, { entries: FolderEntry[] }];
  "workspace/read_file": [{ workspace_id: string; path: string }, { text: string }];
  "session/create": [
    Omit<Settings, "effort"> & { effort?: string; session_id?: string; workspace_id: string; harness: string },
    { session_id: string; existing?: boolean },
  ];
  "session/get": [
    { session_id: string },
    Settings & {
      state: SessionState;
      approvals: { request_id: string; request: ApprovalRequest }[];
      queued: Prompt[];
      history: Update[];
    },
  ];
  "session/send": [{ session_id: string; text: string }, { message_id: string }];
  "session/interrupt": [{ session_id: string }, Empty];
  "session/configure": [SettingsPatch & { session_id: string }, Empty];
  "session/approve": [{ session_id: string; request_id: string; response: ApprovalResponse }, Empty];
  "session/destroy": [{ session_id: string }, Empty];
  "session/archive": [{ session_id: string }, Empty];
  "session/unarchive": [{ session_id: string }, Empty];
};
