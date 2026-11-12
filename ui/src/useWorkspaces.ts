import { Channel, invoke } from "@tauri-apps/api/core";
import { ask } from "@tauri-apps/plugin-dialog";
import { useEffect, useState } from "react";
import type { SessionInfo, WatchMessage, WorkspaceInfo } from "./protocol";

/**
 * The daemon's workspaces and sessions, from a watch that restarts,
 * reconnecting, when the connection drops. `connection` changes each time a
 * watch connects.
 */
export function useWorkspaces() {
  const [workspaces, setWorkspaces] = useState<WorkspaceInfo[]>([]);
  const [sessions, setSessions] = useState<SessionInfo[]>([]);
  const [attempt, setAttempt] = useState(0);
  const [connection, setConnection] = useState(0);

  // biome-ignore lint/correctness/useExhaustiveDependencies: each attempt starts a new watch
  useEffect(() => {
    // Only the latest watch's channel is read.
    let current = true;
    let listed = false;
    const channel = new Channel<WatchMessage>();
    channel.onmessage = (message) => {
      if (!current) return;
      if (message.type === "list") {
        if (!listed) {
          listed = true;
          setConnection((connection) => connection + 1);
        }
        setWorkspaces(message.workspaces);
        setSessions(message.sessions);
      } else if (message.type === "error") {
        ask(`Cannot reach oxd: ${message.message}`, {
          kind: "error",
          okLabel: "Retry",
          cancelLabel: "Cancel",
        }).then((retry) => {
          if (retry) setAttempt((attempt) => attempt + 1);
        });
      } else {
        setAttempt((attempt) => attempt + 1);
      }
    };
    invoke("watch", { channel });
    return () => {
      current = false;
    };
  }, [attempt]);

  return { workspaces, sessions, connection };
}
