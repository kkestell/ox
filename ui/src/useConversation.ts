import { Channel, invoke } from "@tauri-apps/api/core";
import { useEffect, useReducer, useState } from "react";
import type { Item, ViewMessage } from "./protocol";

// Items by key, in the order their keys first appeared.
type State = Map<string, Item>;

const empty: State = new Map();

type Action = { type: "clear" } | { type: "item"; item: Item };

// An item creates the entry for its key or replaces it; text and thought
// items append their text to it unless they replace it.
function reduce(state: State, action: Action): State {
  if (action.type === "clear") return empty;
  const { item } = action;
  const existing = state.get(item.key);
  const next =
    existing !== undefined &&
    (item.type === "text" || item.type === "thought") &&
    !item.replace &&
    existing.type === item.type
      ? { ...item, text: existing.text + item.text }
      : item;
  return new Map(state).set(item.key, next);
}

/**
 * The items of a session's conversation, replayed from its first event and
 * followed live. A dropped connection views the session again.
 */
export function useConversation(sessionId: string | null): Item[] {
  const [state, dispatch] = useReducer(reduce, empty);
  const [attempt, setAttempt] = useState(0);

  // biome-ignore lint/correctness/useExhaustiveDependencies: each attempt views the session again
  useEffect(() => {
    dispatch({ type: "clear" });
    // Only the latest view's channel is read. Viewing no session ends the
    // previous view, such as one left by a webview reload.
    let current = true;
    const channel = new Channel<ViewMessage>();
    channel.onmessage = (message) => {
      if (!current) return;
      if (message.type === "item") dispatch({ type: "item", item: message.item });
      else setAttempt((attempt) => attempt + 1);
    };
    invoke("view_session", { sessionId, channel });
    return () => {
      current = false;
    };
  }, [sessionId, attempt]);

  return [...state.values()];
}
