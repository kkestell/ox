import { invoke } from "@tauri-apps/api/core";
import { useState } from "react";
import { CodeSpans } from "./CodeSpans";
import type { Item } from "./protocol";
import { showError } from "./showError";

/** A permission request awaiting its answer, with the options the agent offers. */
export function Permission({
  item,
  sessionId,
}: {
  item: Extract<Item, { type: "permission" }>;
  sessionId: string;
}) {
  const [pending, setPending] = useState(false);

  const permit = async (optionId: string) => {
    setPending(true);
    try {
      await invoke("permit", { sessionId, requestId: item.request_id, optionId });
    } catch (e) {
      setPending(false);
      await showError(e);
    }
  };

  return (
    <div className="permission">
      <p className="title">
        <CodeSpans text={item.title} />
      </p>
      <div className="options">
        {item.options.map((option) => (
          <button
            key={option.optionId}
            type="button"
            className="text-button"
            data-kind={option.kind}
            disabled={pending}
            onClick={() => permit(option.optionId)}
          >
            {option.name}
          </button>
        ))}
      </div>
    </div>
  );
}
