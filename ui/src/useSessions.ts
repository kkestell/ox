import { Channel, invoke } from "@tauri-apps/api/core";
import { useEffect, useState } from "react";
import type { SessionInfo, SessionsMessage } from "./protocol";

/**
 * The daemon's sessions, from a sessions watch that restarts, reconnecting,
 * when the connection drops.
 */
export function useSessions() {
  const [sessions, setSessions] = useState<SessionInfo[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [attempt, setAttempt] = useState(0);
  const retry = () => setAttempt((attempt) => attempt + 1);

  // biome-ignore lint/correctness/useExhaustiveDependencies: each attempt starts a new watch
  useEffect(() => {
    // Only the latest watch's channel is read.
    let current = true;
    const channel = new Channel<SessionsMessage>();
    channel.onmessage = (message) => {
      if (!current) return;
      if (message.type === "sessions") {
        setSessions(message.sessions);
        setError(null);
      } else if (message.type === "error") {
        setError(`Cannot reach oxd: ${message.message}`);
      } else {
        setAttempt((attempt) => attempt + 1);
      }
    };
    invoke("watch_sessions", { channel });
    return () => {
      current = false;
    };
  }, [attempt]);

  return { sessions, error, retry };
}
