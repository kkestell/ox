import { Channel, invoke } from "@tauri-apps/api/core";
import { useEffect, useState } from "react";
import type { SessionInfo, WatchMessage, WorkspaceInfo } from "./protocol";

/**
 * The daemon's workspaces and sessions, from a watch that restarts,
 * reconnecting, when the connection drops.
 */
export function useWorkspaces() {
  const [workspaces, setWorkspaces] = useState<WorkspaceInfo[]>([]);
  const [sessions, setSessions] = useState<SessionInfo[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [attempt, setAttempt] = useState(0);
  const retry = () => setAttempt((attempt) => attempt + 1);

  // biome-ignore lint/correctness/useExhaustiveDependencies: each attempt starts a new watch
  useEffect(() => {
    // Only the latest watch's channel is read.
    let current = true;
    const channel = new Channel<WatchMessage>();
    channel.onmessage = (message) => {
      if (!current) return;
      if (message.type === "list") {
        setWorkspaces(message.workspaces);
        setSessions(message.sessions);
        setError(null);
      } else if (message.type === "error") {
        setError(`Cannot reach oxd: ${message.message}`);
      } else {
        setAttempt((attempt) => attempt + 1);
      }
    };
    invoke("watch", { channel });
    return () => {
      current = false;
    };
  }, [attempt]);

  return { workspaces, sessions, error, retry };
}
