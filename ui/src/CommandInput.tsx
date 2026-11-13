import { useId, useLayoutEffect, useRef, useState } from "react";
import type { Command } from "./protocol";

/** The message editor and completion for the agent's leading slash command. */
export function CommandInput({
  value,
  commands,
  disabled,
  canSend,
  onChange,
  onSubmit,
}: {
  value: string;
  commands: Command[];
  disabled: boolean;
  canSend: boolean;
  onChange: (value: string) => void;
  onSubmit: () => void;
}) {
  const editor = useRef<HTMLTextAreaElement>(null);
  const selected = useRef<HTMLButtonElement>(null);
  const selection = useRef<number | null>(null);
  const listId = useId();
  const [caret, setCaret] = useState(0);
  const [focused, setFocused] = useState(false);
  const [dismissed, setDismissed] = useState(false);
  const [composing, setComposing] = useState(false);
  const [active, setActive] = useState(0);

  const token = /^(\s*\/)(\S*)/.exec(value);
  const editing = token && caret >= token[1]!.length && caret <= token[0].length;
  const matches = editing
    ? commands.filter(
        (command) =>
          command.type === "command" &&
          command.name.toLowerCase().startsWith(token[2]!.toLowerCase()),
      )
    : [];
  const index = Math.min(active, matches.length - 1);
  const open = focused && !disabled && !dismissed && !composing && matches.length > 0;
  const activeId = open ? `${listId}-${index}` : undefined;

  // Selection is applied after React has put the completed draft in the editor.
  useLayoutEffect(() => {
    if (selection.current === null) return;
    editor.current?.focus();
    editor.current?.setSelectionRange(selection.current, selection.current);
    selection.current = null;
  });
  // The active option remains visible when navigating a long catalog.
  // biome-ignore lint/correctness/useExhaustiveDependencies: the active ID changes when the menu opens or selection moves
  useLayoutEffect(() => selected.current?.scrollIntoView({ block: "nearest" }), [activeId]);

  const choose = (command: Command) => {
    if (!token) return;
    const suffix = value.slice(token[0].length);
    const space = /^\s/.test(suffix) ? "" : " ";
    const prefix = `${token[1]}${command.name}`;
    selection.current = prefix.length + 1;
    setDismissed(true);
    onChange(`${prefix}${space}${suffix}`);
  };

  return (
    <>
      <textarea
        ref={editor}
        rows={1}
        role="combobox"
        aria-label="Message"
        aria-autocomplete="list"
        aria-expanded={open}
        aria-controls={open ? listId : undefined}
        aria-activedescendant={activeId}
        value={value}
        disabled={disabled}
        onFocus={() => setFocused(true)}
        onBlur={() => setFocused(false)}
        onSelect={(event) => setCaret(event.currentTarget.selectionStart)}
        onCompositionStart={() => setComposing(true)}
        onCompositionEnd={() => setComposing(false)}
        onChange={(event) => {
          onChange(event.target.value);
          setCaret(event.target.selectionStart);
          setActive(0);
          setDismissed(false);
        }}
        onKeyDown={(event) => {
          if (event.nativeEvent.isComposing || composing) return;
          if (open && !event.shiftKey) {
            if (event.key === "ArrowDown") setActive(Math.min(index + 1, matches.length - 1));
            else if (event.key === "ArrowUp") setActive(Math.max(index - 1, 0));
            else if (event.key === "Enter" || event.key === "Tab") choose(matches[index]!);
            else if (event.key === "Escape") setDismissed(true);
            else return;
            event.preventDefault();
          } else if (event.key === "Enter" && !event.shiftKey) {
            event.preventDefault();
            if (canSend) onSubmit();
          }
        }}
      />
      {open && (
        <div className="menu command-menu">
          <div id={listId} className="menu-list" role="listbox" aria-label="Commands">
            {matches.map((command, optionIndex) => (
              <button
                key={command.name}
                ref={optionIndex === index ? selected : undefined}
                id={`${listId}-${optionIndex}`}
                type="button"
                role="option"
                tabIndex={-1}
                aria-selected={optionIndex === index}
                className={optionIndex === index ? "active" : undefined}
                onMouseEnter={() => setActive(optionIndex)}
                onMouseDown={(event) => event.preventDefault()}
                onClick={() => choose(command)}
              >
                <span className="menu-item-text">
                  <span>
                    /{command.name}
                    {command.input?.type === "text" && command.input.hint && (
                      <span className="menu-detail"> {command.input.hint}</span>
                    )}
                  </span>
                  <span className="menu-detail">{command.description}</span>
                </span>
              </button>
            ))}
          </div>
        </div>
      )}
    </>
  );
}
