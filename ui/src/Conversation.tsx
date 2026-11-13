import { Check, X } from "lucide-react";
import { useLayoutEffect, useRef } from "react";
import { CodeSpans } from "./CodeSpans";
import type { Item, ToolCallStatus } from "./protocol";

export function Conversation({ sessionId, items }: { sessionId: string | null; items: Item[] }) {
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
      {sessionId && items.map((item) => <ItemView key={item.key} item={item} />)}
    </div>
  );
}

function ItemView({ item }: { item: Item }) {
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
      if (item.text.trim() === "") return null;
      return (
        <details className="thought">
          <summary>Thinking</summary>
          <p className="text">{item.text}</p>
        </details>
      );
    case "tool_call":
      return <ToolCall item={item} />;
    // Permission requests and forms show in the composer while they await
    // an answer.
    case "permission":
    case "elicitation":
      return null;
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

function ToolStatus({ status }: { status: ToolCallStatus }) {
  switch (status) {
    case "pending":
    case "in_progress":
      return <span className="status running" role="img" aria-label="Running" title="Running" />;
    case "completed":
      return (
        <span className="status completed" role="img" aria-label="Completed" title="Completed">
          <Check />
        </span>
      );
    case "failed":
      return (
        <span className="status failed" role="img" aria-label="Failed" title="Failed">
          <X />
        </span>
      );
  }
}

function ToolCall({ item }: { item: Extract<Item, { type: "tool_call" }> }) {
  const heading = (
    <>
      <ToolStatus status={item.status} />
      <span className="title">
        <CodeSpans text={item.title} />
      </span>
    </>
  );
  const input = item.raw_input;
  if (input === null && item.content.length === 0) {
    return (
      <div className="tool-call">
        <div className="heading">{heading}</div>
      </div>
    );
  }
  return (
    <details className="tool-call">
      <summary className="heading">{heading}</summary>
      <div className="details">
        {input !== null && (
          <section>
            <p className="label">Arguments</p>
            <pre>{JSON.stringify(input, null, 2)}</pre>
          </section>
        )}
        {item.content.length > 0 && (
          <section>
            <p className="label">Output</p>
            {item.content.map((content, index) =>
              content.type === "text" ? (
                // biome-ignore lint/suspicious/noArrayIndexKey: a tool call's content is replaced whole
                <pre key={index}>{content.text}</pre>
              ) : (
                <img
                  // biome-ignore lint/suspicious/noArrayIndexKey: a tool call's content is replaced whole
                  key={index}
                  alt=""
                  src={`data:${content.mime_type};base64,${content.data}`}
                />
              ),
            )}
          </section>
        )}
      </div>
    </details>
  );
}
