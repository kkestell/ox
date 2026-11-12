import { Check, ChevronDown, Search } from "lucide-react";
import { type CSSProperties, useEffect, useRef, useState } from "react";
import { ClaudeLogo } from "./ClaudeLogo";
import type { ConfigOption, SelectOption } from "./protocol";

export type SelectConfig = Extract<ConfigOption, { type: "select" }>;

/** A select option's choices, with its groups flattened. */
export function choices(option: SelectConfig): SelectOption[] {
  return option.options.flatMap((entry) => ("group" in entry ? entry.options : [entry]));
}

/**
 * The session's model and effort: a button showing both, which opens a menu
 * to search for and choose a model and to set the effort. The menu stays open
 * after a model is chosen, so the effort can be set next. An effort level
 * being set shows until the agent reports it or refuses it.
 */
export function ModelPicker({
  model,
  effort,
  onChange,
}: {
  model: SelectConfig;
  effort: SelectConfig | undefined;
  /** Sets `option` to `value` and resolves to whether the agent accepted it. */
  onChange: (option: SelectConfig, value: string) => Promise<boolean>;
}) {
  const search = useRef<HTMLInputElement>(null);
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(0);
  const [pendingEffort, setPendingEffort] = useState<string | null>(null);
  // biome-ignore lint/correctness/useExhaustiveDependencies: a new current level ends the pending one
  useEffect(() => setPendingEffort(null), [effort?.currentValue]);

  const models = choices(model).filter((choice) =>
    choice.name.toLowerCase().includes(query.trim().toLowerCase()),
  );
  const current = choices(model).find((choice) => choice.value === model.currentValue);
  const levels = effort ? choices(effort) : [];
  const level = levels.findIndex(
    (choice) => choice.value === (pendingEffort ?? effort?.currentValue),
  );

  const choose = (choice: SelectOption) => {
    if (choice.value !== model.currentValue) onChange(model, choice.value);
  };

  const setLevel = async (index: number) => {
    const value = levels[index]!.value;
    setPendingEffort(value);
    if (!(await onChange(effort!, value))) setPendingEffort(null);
  };

  return (
    <>
      <button
        type="button"
        className="model-picker"
        aria-haspopup="dialog"
        popoverTarget="model-menu"
        style={{ anchorName: "--model-picker" }}
      >
        <ClaudeLogo />
        <span>{current?.name ?? model.currentValue}</span>
        {levels[level] && <span className="effort-name">{levels[level].name}</span>}
        <ChevronDown />
      </button>
      <div
        id="model-menu"
        className="menu model-menu"
        role="dialog"
        aria-label={model.name}
        popover="auto"
        style={{ positionAnchor: "--model-picker" }}
        onToggle={(event) => {
          if (event.newState !== "open") return;
          setQuery("");
          setActive(0);
          search.current?.focus();
        }}
      >
        <label className="search">
          <Search />
          <input
            ref={search}
            type="text"
            role="combobox"
            aria-label="Search models"
            aria-controls="model-list"
            aria-expanded="true"
            aria-activedescendant={models[active] && `model-${models[active].value}`}
            placeholder="Search models"
            value={query}
            onChange={(event) => {
              setQuery(event.target.value);
              setActive(0);
            }}
            onKeyDown={(event) => {
              if (event.key === "ArrowDown") setActive(Math.min(active + 1, models.length - 1));
              else if (event.key === "ArrowUp") setActive(Math.max(active - 1, 0));
              else if (event.key === "Enter" && models[active]) choose(models[active]);
              else return;
              event.preventDefault();
            }}
          />
        </label>
        <div id="model-list" role="listbox" aria-label={model.name}>
          {models.map((choice, index) => (
            <button
              key={choice.value}
              id={`model-${choice.value}`}
              type="button"
              role="option"
              tabIndex={-1}
              aria-selected={choice.value === model.currentValue}
              className={index === active ? "active" : undefined}
              onMouseEnter={() => setActive(index)}
              onClick={() => choose(choice)}
            >
              <ClaudeLogo />
              {choice.name}
              {choice.value === model.currentValue && <Check />}
            </button>
          ))}
        </div>
        {effort && (
          <div className="effort">
            <label htmlFor="effort-level">{effort.name}</label>
            <input
              id="effort-level"
              type="range"
              min={0}
              max={levels.length - 1}
              step={1}
              value={level}
              aria-valuetext={levels[level]?.name}
              style={{ "--position": position(level, levels.length) } as CSSProperties}
              onChange={(event) => setLevel(Number(event.target.value))}
            />
            <div className="stops" aria-hidden="true">
              {levels.map((choice, index) => (
                <span
                  key={choice.value}
                  style={{ "--position": position(index, levels.length) } as CSSProperties}
                >
                  {choice.name}
                </span>
              ))}
            </div>
          </div>
        )}
      </div>
    </>
  );
}

/** Where stop `index` of `count` sits along a slider, from 0 to 1. */
function position(index: number, count: number): number {
  return count > 1 ? index / (count - 1) : 0;
}
