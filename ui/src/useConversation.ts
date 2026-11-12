import { Channel, invoke } from "@tauri-apps/api/core";
import { useEffect, useReducer } from "react";
import type { Item } from "./protocol";

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
 * followed live, viewed again each time the app reconnects to `oxd`.
 */
export function useConversation(sessionId: string | null, connection: number): Item[] {
  const [state, dispatch] = useReducer(reduce, { sessionId, items: new Map() });

  useEffect(() => {
    // The first view waits for the first watch to connect.
    if (connection === 0) return;
    dispatch({ type: "view", sessionId });
    // Only the latest view's channel is read. Viewing no session ends the
    // previous view, such as one left by a webview reload.
    let current = true;
    const channel = new Channel<Item>();
    channel.onmessage = (item) => {
      if (current) dispatch({ type: "item", item });
    };
    invoke("view_session", { sessionId, channel });
    return () => {
      current = false;
    };
  }, [sessionId, connection]);

  // Until the effect views a newly chosen session, the state holds the
  // previous one's items.
  return state.sessionId === sessionId ? [...state.items.values()] : [];
}
