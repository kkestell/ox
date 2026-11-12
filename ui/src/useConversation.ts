import { Channel, invoke } from "@tauri-apps/api/core";
import { useEffect, useReducer, useState } from "react";
import type { Item, ViewMessage } from "./protocol";

// The viewed session's items by key, in the order their keys first appeared.
interface State {
  sessionId: string | null;
  items: Map<string, Item>;
}

type Action = { type: "view"; sessionId: string | null } | { type: "item"; item: Item };

// Viewing a session starts it empty. An item creates the entry for its key
// or replaces it; text and thought items append their text to it unless they
// replace it.
function reduce(state: State, action: Action): State {
  if (action.type === "view") return { sessionId: action.sessionId, items: new Map() };
  const { item } = action;
  const existing = state.items.get(item.key);
  const next =
    existing !== undefined &&
    (item.type === "text" || item.type === "thought") &&
    !item.replace &&
    existing.type === item.type
      ? { ...item, text: existing.text + item.text }
      : item;
  return { ...state, items: new Map(state.items).set(item.key, next) };
}

/**
 * The items of a session's conversation, replayed from its first event and
 * followed live. A dropped connection views the session again.
 */
export function useConversation(sessionId: string | null): Item[] {
  const [state, dispatch] = useReducer(reduce, { sessionId, items: new Map() });
  const [attempt, setAttempt] = useState(0);

  // biome-ignore lint/correctness/useExhaustiveDependencies: each attempt views the session again
  useEffect(() => {
    dispatch({ type: "view", sessionId });
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

  // Until the effect views a newly chosen session, the state holds the
  // previous one's items.
  return state.sessionId === sessionId ? [...state.items.values()] : [];
}
