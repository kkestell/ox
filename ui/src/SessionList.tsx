import { invoke } from "@tauri-apps/api/core";
import { Menu } from "@tauri-apps/api/menu";
import { open } from "@tauri-apps/plugin-dialog";
import {
  Archive,
  ArchiveRestore,
  ChevronDown,
  ChevronRight,
  FolderPlus,
  GitBranch,
  Plus,
} from "lucide-react";
import { type MouseEvent, useState } from "react";
import { branchStatus, localMerge } from "./localMerge";
import type { Branches, Checkout, SessionInfo, SessionState, WorkspaceInfo } from "./protocol";
import { showError } from "./showError";
import type { SessionActions } from "./useSessionActions";

type Agent = { id: string; name: string };

const agents: [Agent, ...Agent[]] = [{ id: "claude", name: "Claude" }];

const stateLabels: Record<SessionState, string> = {
  starting: "Starting",
  idle: "Idle",
  running: "Running",
  awaiting_permission: "Awaiting permission",
  awaiting_input: "Awaiting input",
  exited: "Exited",
};

/** Opens a native context menu of `items` at the pointer. */
async function contextMenu(
  event: MouseEvent,
  items: { text: string; enabled: boolean; action: () => void }[],
) {
  event.preventDefault();
  const menu = await Menu.new({ items });
  await menu.popup();
}

/** The last component of a workspace's path. */
function directoryName(path: string) {
  return path.split("/").filter(Boolean).pop() ?? path;
}

export function SessionList({
  workspaces,
  sessions,
  selected,
  onSelect,
  actions: { busy, deleteSession, archiveSession, unarchiveSession },
}: {
  workspaces: WorkspaceInfo[];
  sessions: SessionInfo[];
  selected: string | null;
  onSelect: (sessionId: string) => void;
  actions: SessionActions;
}) {
  // The agent the New Task menus start.
  const [agent, setAgent] = useState(agents[0].id);
  // The workspaces whose Archived groups are open.
  const [openArchives, setOpenArchives] = useState<ReadonlySet<string>>(new Set());

  const run = async (action: () => Promise<unknown>) => {
    try {
      await action();
    } catch (e) {
      await showError(e);
    }
  };

  const addWorkspace = async () => {
    const path = await open({ directory: true, title: "Add Workspace" });
    if (path === null) return;
    await run(() => invoke("add_workspace", { path }));
  };

  const removeWorkspace = (workspaceId: string) =>
    run(() => invoke("remove_workspace", { workspaceId }));

  const newSession = (workspaceId: string, agent: string, checkout: Checkout) =>
    run(async () => {
      const session = await invoke<SessionInfo>("new_session", { agent, workspaceId, checkout });
      onSelect(session.id);
    });

  // A worktree session on a new `ox/<id8>` branch from `HEAD`, compared with
  // the branch the workspace's checkout is on.
  const newTask = (workspace: WorkspaceInfo, agent: string) =>
    run(async () => {
      const { head } = await invoke<Branches>("workspace_branches", {
        workspaceId: workspace.id,
      });
      if (head === null) {
        await showError(`${directoryName(workspace.path)} is not on a branch`);
        return;
      }
      const checkout: Checkout = { type: "worktree", compare: head };
      const session = await invoke<SessionInfo>("new_session", {
        agent,
        workspaceId: workspace.id,
        checkout,
      });
      onSelect(session.id);
    });

  // Viewing a saved session whose agent is not running starts its agent, which
  // replays the conversation.
  const select = async (session: SessionInfo) => {
    if (session.state === "exited" && !session.archived && session.agent_session_id !== null) {
      await run(() => invoke("load_session", { sessionId: session.id }));
    }
    onSelect(session.id);
  };

  const toggleArchive = (workspaceId: string) =>
    setOpenArchives((open) => {
      const next = new Set(open);
      if (!next.delete(workspaceId)) next.add(workspaceId);
      return next;
    });

  const sessionRow = (session: SessionInfo) => {
    const state = stateLabels[session.state];
    const merge = localMerge(session);
    const status = merge && branchStatus(merge.compare, merge.comparison);
    return (
      <li
        key={session.id}
        className="session"
        onContextMenu={(event) =>
          contextMenu(event, [
            {
              text: "Delete Session",
              enabled: !busy.has(session.id),
              action: () => deleteSession(session.id, false),
            },
          ])
        }
      >
        <button
          type="button"
          className="select"
          title={session.cwd}
          aria-current={session.id === selected}
          onClick={() => select(session)}
        >
          <span className={`status ${session.state}`} role="img" aria-label={state} title={state} />
          <span className="title">{session.title ?? "New session"}</span>
          {status && (
            <span
              className={`status branch-status${status.attention ? " attention" : ""}`}
              role="img"
              aria-label={status.tooltip}
              title={status.tooltip}
            >
              <GitBranch />
            </span>
          )}
        </button>
        <div className="row-actions">
          {session.archived ? (
            <button
              type="button"
              className="icon-button"
              title="Unarchive Session"
              aria-label="Unarchive Session"
              disabled={busy.has(session.id)}
              onClick={() => unarchiveSession(session.id)}
            >
              <ArchiveRestore />
            </button>
          ) : (
            <button
              type="button"
              className="icon-button"
              title="Archive Session"
              aria-label="Archive Session"
              disabled={busy.has(session.id)}
              onClick={() => archiveSession(session.id, false)}
            >
              <Archive />
            </button>
          )}
        </div>
      </li>
    );
  };

  return (
    <div className="sidebar">
      <header className="pane-header sidebar-header">
        <button
          type="button"
          className="icon-button"
          title="Add Workspace"
          aria-label="Add Workspace"
          onClick={addWorkspace}
        >
          <FolderPlus />
        </button>
      </header>
      <div className="session-list">
        {workspaces.map((workspace) => {
          const workspaceSessions = sessions.filter(
            (session) => session.workspace_id === workspace.id,
          );
          const active = workspaceSessions.filter((session) => !session.archived);
          const archived = workspaceSessions.filter((session) => session.archived);
          const archiveOpen = openArchives.has(workspace.id);
          return (
            <section
              key={workspace.id}
              className="workspace"
              aria-labelledby={`workspace-${workspace.id}`}
            >
              <div className="workspace-header">
                <h2
                  id={`workspace-${workspace.id}`}
                  title={workspace.path}
                  onContextMenu={(event) =>
                    contextMenu(event, [
                      {
                        text: "Remove Workspace",
                        enabled: workspaceSessions.length === 0,
                        action: () => removeWorkspace(workspace.id),
                      },
                    ])
                  }
                >
                  {directoryName(workspace.path)}
                </h2>
                <button
                  type="button"
                  className="icon-button"
                  title="New Task"
                  aria-label="New Task"
                  popoverTarget={`new-session-${workspace.id}`}
                  style={{ anchorName: `--new-session-${workspace.id}` }}
                >
                  <Plus />
                </button>
                <div
                  id={`new-session-${workspace.id}`}
                  className="menu new-session-menu"
                  role="menu"
                  popover="auto"
                  style={{ positionAnchor: `--new-session-${workspace.id}` }}
                >
                  <select
                    aria-label="Agent"
                    title="Agent"
                    value={agent}
                    onChange={(event) => setAgent(event.target.value)}
                  >
                    {agents.map((choice) => (
                      <option key={choice.id} value={choice.id}>
                        {choice.name}
                      </option>
                    ))}
                  </select>
                  {workspace.git_working_tree && (
                    <button
                      type="button"
                      role="menuitem"
                      popoverTarget={`new-session-${workspace.id}`}
                      popoverTargetAction="hide"
                      onClick={() => newTask(workspace, agent)}
                    >
                      New Task
                    </button>
                  )}
                  <button
                    type="button"
                    role="menuitem"
                    popoverTarget={`new-session-${workspace.id}`}
                    popoverTargetAction="hide"
                    onClick={() => newSession(workspace.id, agent, { type: "workspace" })}
                  >
                    Session in Checkout
                  </button>
                </div>
              </div>
              <ul className="sessions">
                {active.map(sessionRow)}
                {archived.length > 0 && (
                  <li className="session archived-toggle">
                    <button
                      type="button"
                      className="select"
                      aria-expanded={archiveOpen}
                      onClick={() => toggleArchive(workspace.id)}
                    >
                      <span className="status">
                        {archiveOpen ? <ChevronDown /> : <ChevronRight />}
                      </span>
                      <span className="title">Archived ({archived.length})</span>
                    </button>
                  </li>
                )}
                {archiveOpen && archived.map(sessionRow)}
              </ul>
            </section>
          );
        })}
      </div>
    </div>
  );
}
