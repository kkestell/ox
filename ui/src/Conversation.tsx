import { invoke } from "@tauri-apps/api/core";
import { useLayoutEffect, useRef, useState } from "react";
import type { Item } from "./protocol";
import { useConversation } from "./useConversation";

export function Conversation({ sessionId }: { sessionId: string | null }) {
  const items = useConversation(sessionId);
  const element = useRef<HTMLDivElement>(null);
  // Whether the view follows the end of the conversation as it grows.
  const following = useRef(true);

  // biome-ignore lint/correctness/useExhaustiveDependencies: a new session is followed from its end
  useLayoutEffect(() => {
    following.current = true;
  }, [sessionId]);

  // biome-ignore lint/correctness/useExhaustiveDependencies: each change to the items scrolls a followed view
  useLayoutEffect(() => {
    const node = element.current!;
    if (following.current) node.scrollTop = node.scrollHeight;
  }, [items]);

  return (
    <div
      className="conversation"
      ref={element}
      onScroll={(event) => {
        const node = event.currentTarget;
        following.current = node.scrollHeight - node.scrollTop - node.clientHeight < 1;
      }}
    >
      {sessionId &&
        items.map((item) => <ItemView key={item.key} item={item} sessionId={sessionId} />)}
    </div>
  );
}

function ItemView({ item, sessionId }: { item: Item; sessionId: string }) {
  switch (item.type) {
    case "prompt":
      return (
        <div className="prompt">
          <p className="text">{item.text}</p>
          {item.attachments.map((name, index) => (
            // biome-ignore lint/suspicious/noArrayIndexKey: a prompt's attachments never change, and names can repeat
            <span key={index} className="attachment">
              {name}
            </span>
          ))}
        </div>
      );
    case "text":
      return <p className="text">{item.text}</p>;
    case "thought":
      return (
        <details className="thought">
          <summary>Thinking</summary>
          <p className="text">{item.text}</p>
        </details>
      );
    case "tool_call":
      return (
        <div className="tool-call" data-kind={item.kind} data-status={item.status}>
          <span className="title">{item.title}</span>
          <span className="status">{item.status.replace("_", " ")}</span>
        </div>
      );
    case "permission":
      return <Permission item={item} sessionId={sessionId} />;
    case "turn_error":
      return <p className="turn-error">{item.message}</p>;
    case "agent_exited":
      return (
        <p className="agent-exited">
          {item.error ? `The agent exited: ${item.error}` : "The agent exited"}
        </p>
      );
    case "session_closed":
      return <p className="session-closed">Session closed</p>;
  }
}

function Permission({
  item,
  sessionId,
}: {
  item: Extract<Item, { type: "permission" }>;
  sessionId: string;
}) {
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const permit = async (optionId: string) => {
    setPending(true);
    try {
      await invoke("permit", { sessionId, requestId: item.request_id, optionId });
    } catch (e) {
      setError(String(e));
      setPending(false);
    }
  };

  return (
    <div className="permission">
      <p className="title">{item.title}</p>
      {item.outcome === null ? (
        <div className="options">
          {item.options.map((option) => (
            <button
              key={option.optionId}
              type="button"
              data-kind={option.kind}
              disabled={pending}
              onClick={() => permit(option.optionId)}
            >
              {option.name}
            </button>
          ))}
        </div>
      ) : (
        <p className="outcome">{item.outcome}</p>
      )}
      {error && <p className="error">{error}</p>}
    </div>
  );
}
