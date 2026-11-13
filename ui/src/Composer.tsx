import { invoke } from "@tauri-apps/api/core";
import { open } from "@tauri-apps/plugin-dialog";
import { ArrowUp, Paperclip, Square, X } from "lucide-react";
import { useState } from "react";
import { Elicitation } from "./Elicitation";
import { ModelPicker, type SelectConfig } from "./ModelPicker";
import { Permission } from "./Permission";
import type { ConfigOption, Item, SelectOption, SessionInfo } from "./protocol";
import { showError } from "./showError";

export function Composer({
  session,
  request,
}: {
  session: SessionInfo;
  /** The session's oldest unanswered permission request or form. */
  request: Extract<Item, { type: "permission" | "elicitation" }> | undefined;
}) {
  const [text, setText] = useState("");
  const [attachments, setAttachments] = useState<string[]>([]);

  const running =
    session.state === "running" ||
    session.state === "awaiting_permission" ||
    session.state === "awaiting_input";
  const canSend = session.state === "idle" && (text.trim() !== "" || attachments.length > 0);
  const disabled = session.state === "exited";

  /** Runs `action`, shows its error if it fails, and resolves to whether it succeeded. */
  const run = async (action: () => Promise<unknown>) => {
    try {
      await action();
      return true;
    } catch (e) {
      await showError(e);
      return false;
    }
  };

  const send = () =>
    run(async () => {
      await invoke("prompt", { sessionId: session.id, text: text.trim(), attachments });
      setText("");
      setAttachments([]);
    });

  const stop = () => run(() => invoke("cancel", { sessionId: session.id }));

  const setOption = (option: ConfigOption, value: string | boolean) =>
    run(() =>
      invoke("set_config_option", { sessionId: session.id, configId: option.configId, value }),
    );

  // The model picker shows the model and effort options; the rest show as
  // pickers of their own.
  const selects = session.config_options.filter(
    (option): option is SelectConfig => option.type === "select",
  );
  const model = selects.find((option) => option.category === "model");
  const effort = model && selects.find((option) => option.category === "thought_level");
  const others = session.config_options.filter((option) => option !== model && option !== effort);

  const attach = async () => {
    const paths = await open({ multiple: true, title: "Attach Files" });
    if (paths !== null) setAttachments((current) => [...new Set([...current, ...paths])]);
  };

  return (
    <div className="composer">
      <div className="stack">
        {request?.type === "permission" && (
          <Permission key={request.request_id} item={request} sessionId={session.id} />
        )}
        {request?.type === "elicitation" && (
          <Elicitation key={request.request_id} item={request} sessionId={session.id} />
        )}
        <form
          className="box"
          onSubmit={(event) => {
            event.preventDefault();
            if (running) stop();
            else if (canSend) send();
          }}
        >
          {attachments.length > 0 && (
            <ul className="attachments">
              {attachments.map((path) => (
                <li key={path} title={path}>
                  {path.split("/").pop()}
                  <button
                    type="button"
                    aria-label="Remove"
                    onClick={() => setAttachments((current) => current.filter((p) => p !== path))}
                  >
                    <X />
                  </button>
                </li>
              ))}
            </ul>
          )}
          <div className="input">
            <button
              type="button"
              className="icon-button"
              title="Attach Files"
              aria-label="Attach Files"
              disabled={disabled}
              onClick={attach}
            >
              <Paperclip />
            </button>
            <textarea
              rows={1}
              aria-label="Message"
              value={text}
              disabled={disabled}
              onChange={(event) => setText(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === "Enter" && !event.shiftKey && !event.nativeEvent.isComposing) {
                  event.preventDefault();
                  if (canSend) send();
                }
              }}
            />
            <button
              type="submit"
              className="icon-button"
              title={running ? "Stop" : "Send"}
              aria-label={running ? "Stop" : "Send"}
              disabled={!running && !canSend}
            >
              {running ? <Square /> : <ArrowUp />}
            </button>
          </div>
        </form>
      </div>
      {session.config_options.length > 0 && (
        <div className="settings">
          {model && <ModelPicker model={model} effort={effort} onChange={setOption} />}
          {others.map((option) => (
            <ConfigPicker
              key={option.configId}
              option={option}
              onChange={(value) => setOption(option, value)}
            />
          ))}
        </div>
      )}
    </div>
  );
}

function ConfigPicker({
  option,
  onChange,
}: {
  option: ConfigOption;
  onChange: (value: string | boolean) => void;
}) {
  if (option.type === "boolean") {
    return (
      <label className="choice">
        <input
          type="checkbox"
          checked={option.currentValue}
          onChange={(event) => onChange(event.target.checked)}
        />
        {option.name}
      </label>
    );
  }
  if (option.type !== "select") return null;
  const choice = (choice: SelectOption) => (
    <option key={choice.value} value={choice.value}>
      {choice.name}
    </option>
  );
  return (
    <select
      aria-label={option.name}
      title={option.name}
      value={option.currentValue}
      onChange={(event) => onChange(event.target.value)}
    >
      {option.options.map((entry) =>
        "group" in entry ? (
          <optgroup key={entry.group} label={entry.name}>
            {entry.options.map(choice)}
          </optgroup>
        ) : (
          choice(entry)
        ),
      )}
    </select>
  );
}
