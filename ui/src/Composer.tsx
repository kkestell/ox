import { invoke } from "@tauri-apps/api/core";
import { open } from "@tauri-apps/plugin-dialog";
import { ArrowUp, Paperclip, Square, X } from "lucide-react";
import { useState } from "react";
import { Permission } from "./Permission";
import type { ConfigOption, Item, SelectOption, SessionInfo } from "./protocol";

export function Composer({
  session,
  permission,
}: {
  session: SessionInfo;
  /** The session's oldest unanswered permission request. */
  permission: Extract<Item, { type: "permission" }> | undefined;
}) {
  const [text, setText] = useState("");
  const [attachments, setAttachments] = useState<string[]>([]);
  const [error, setError] = useState<string | null>(null);

  const running = session.state === "running" || session.state === "awaiting_permission";
  const canSend = session.state === "idle" && (text.trim() !== "" || attachments.length > 0);
  const disabled = session.state === "exited";

  const run = async (action: () => Promise<unknown>) => {
    try {
      await action();
      setError(null);
    } catch (e) {
      setError(String(e));
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

  const attach = async () => {
    const paths = await open({ multiple: true, title: "Attach Files" });
    if (paths !== null) setAttachments((current) => [...new Set([...current, ...paths])]);
  };

  return (
    <form
      className="composer"
      onSubmit={(event) => {
        event.preventDefault();
        if (running) stop();
        else if (canSend) send();
      }}
    >
      <div className="stack">
        {permission && (
          <Permission key={permission.request_id} item={permission} sessionId={session.id} />
        )}
        <div className="box">
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
        </div>
      </div>
      {session.config_options.length > 0 && (
        <div className="settings">
          {session.config_options.map((option) => (
            <ConfigPicker
              key={option.configId}
              option={option}
              onChange={(value) => setOption(option, value)}
            />
          ))}
        </div>
      )}
      {error && <p className="error">{error}</p>}
    </form>
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
      <label>
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
