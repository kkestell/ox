import { invoke } from "@tauri-apps/api/core";
import { open } from "@tauri-apps/plugin-dialog";
import { CircleAlert, CircleX, LoaderCircle, type LucideIcon, Plus, X } from "lucide-react";
import { useState } from "react";
import type { SessionInfo, SessionState } from "./protocol";

const agents = [
  { id: "claude", name: "Claude" },
  { id: "opencode", name: "OpenCode" },
];

const stateIcons: Partial<Record<SessionState, { icon: LucideIcon; label: string }>> = {
  running: { icon: LoaderCircle, label: "Running" },
  awaiting_permission: { icon: CircleAlert, label: "Awaiting permission" },
  exited: { icon: CircleX, label: "Exited" },
};

export function SessionList({
  sessions,
  selected,
  onSelect,
  error,
  onRetry,
}: {
  sessions: SessionInfo[];
  selected: string | null;
  onSelect: (sessionId: string) => void;
  error: string | null;
  onRetry: () => void;
}) {
  const [menuOpen, setMenuOpen] = useState(false);
  const [actionError, setActionError] = useState<string | null>(null);

  const newSession = async (agent: (typeof agents)[number]) => {
    setMenuOpen(false);
    const cwd = await open({ directory: true, title: `New ${agent.name} Session` });
    if (cwd === null) return;
    try {
      const session = await invoke<SessionInfo>("new_session", { agent: agent.id, cwd });
      setActionError(null);
      onSelect(session.id);
    } catch (e) {
      setActionError(String(e));
    }
  };

  const close = async (sessionId: string) => {
    try {
      await invoke("close_session", { sessionId });
      setActionError(null);
    } catch (e) {
      setActionError(String(e));
    }
  };

  return (
    <div className="session-list">
      <div className="new-session">
        <button type="button" aria-expanded={menuOpen} onClick={() => setMenuOpen(!menuOpen)}>
          <Plus /> New Session
        </button>
        {menuOpen && (
          <div className="menu" role="menu">
            {agents.map((agent) => (
              <button
                key={agent.id}
                type="button"
                role="menuitem"
                onClick={() => newSession(agent)}
              >
                {agent.name}
              </button>
            ))}
          </div>
        )}
      </div>
      {error && (
        <p className="error">
          {error}{" "}
          <button type="button" onClick={onRetry}>
            Retry
          </button>
        </p>
      )}
      {actionError && <p className="error">{actionError}</p>}
      <ul className="sessions">
        {sessions.map((session) => {
          const state = stateIcons[session.state];
          return (
            <li key={session.id} className="session">
              <button
                type="button"
                className="select"
                aria-current={session.id === selected}
                onClick={() => onSelect(session.id)}
              >
                <span className="title">{session.title ?? "New session"}</span>
                <span className="folder" title={session.cwd}>
                  {session.cwd.split("/").filter(Boolean).pop() ?? session.cwd}
                </span>
                {state && (
                  <state.icon className={`state ${session.state}`} aria-label={state.label} />
                )}
              </button>
              <button
                type="button"
                className="close"
                title="Close Session"
                aria-label="Close Session"
                onClick={() => close(session.id)}
              >
                <X />
              </button>
            </li>
          );
        })}
      </ul>
    </div>
  );
}
