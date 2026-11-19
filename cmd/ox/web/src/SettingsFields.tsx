import { createSignal, createUniqueId, For, flush, Show } from "solid-js";
import { HarnessIcon, harnessName } from "./HarnessIcon";
import type { Defaults, Harness, Model, Option, Settings } from "./protocol";

export type ModelChoice = { harness: string; model: string; effort: string };

type Entry = { harness: string; model: Model };

// ModelPicker chooses a model from every harness in harnesses, and its effort.
// A model keeps the current effort when it offers it. A pending value shows a
// spinner until it is confirmed.
export function ModelPicker(props: {
  harnesses: Record<string, Harness>;
  value: ModelChoice;
  disabled?: boolean;
  pending?: boolean;
  onChange(value: ModelChoice): void;
}) {
  const id = createUniqueId();
  const [query, setQuery] = createSignal("");
  const [active, setActive] = createSignal(0);
  let input!: HTMLInputElement;
  let list!: HTMLDivElement;

  const entries = (): Entry[] =>
    Object.entries(props.harnesses).flatMap(([harness, catalog]) =>
      catalog.models.map((model) => ({ harness, model })),
    );
  const matches = () => {
    const q = query().trim().toLowerCase();
    return entries().filter((e) => `${e.model.name} ${harnessName(e.harness)}`.toLowerCase().includes(q));
  };
  const isCurrent = (e: Entry) => e.harness === props.value.harness && e.model.id === props.value.model;
  const current = () => entries().find(isCurrent);
  const effortName = () =>
    current()?.model.efforts.find((e) => e.id === props.value.effort)?.name ?? props.value.effort;

  const choose = (entry: Entry) => {
    if (props.disabled || isCurrent(entry)) return;
    const efforts = entry.model.efforts;
    const effort = efforts.some((e) => e.id === props.value.effort) ? props.value.effort : (efforts[0]?.id ?? "");
    props.onChange({ harness: entry.harness, model: entry.model.id, effort });
  };
  const move = (step: number) => {
    const n = matches().length;
    if (!n) return;
    setActive((i) => (i + step + n) % n);
    flush();
    list.querySelector("[aria-selected=true]")?.scrollIntoView({ block: "nearest" });
  };
  const onKeyDown = (event: KeyboardEvent) => {
    if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      event.preventDefault();
      move(event.key === "ArrowDown" ? 1 : -1);
    } else if (event.key === "Enter") {
      event.preventDefault();
      const entry = matches()[active()];
      if (entry) choose(entry);
    }
  };

  return (
    <>
      <button
        type="button"
        class="picker"
        popovertarget={id}
        disabled={props.disabled}
        aria-busy={props.pending ? "true" : undefined}
        style={{ "anchor-name": `--${id}` }}
      >
        <HarnessIcon harness={props.value.harness} />
        <span class="model">{current()?.model.name ?? props.value.model}</span>
        <Show when={effortName()}>
          <span class="effort-name">{effortName()}</span>
        </Show>
        <span class={["icon", props.pending ? "loader" : "chevron-down"]} />
      </button>
      <div
        id={id}
        popover="auto"
        class="model-picker"
        style={{ "position-anchor": `--${id}` }}
        onToggle={(event) => {
          if (event.newState !== "open") return;
          setQuery("");
          setActive(0);
          input.focus();
        }}
      >
        <div class="search">
          <span class="icon search-icon" />
          <input
            ref={input}
            role="combobox"
            aria-controls={`${id}-list`}
            aria-expanded="true"
            aria-activedescendant={matches().length ? `${id}-${active()}` : undefined}
            placeholder="Search models"
            aria-label="Search models"
            value={query()}
            onInput={(event) => {
              setQuery(event.currentTarget.value);
              setActive(0);
            }}
            onKeyDown={onKeyDown}
          />
        </div>
        <div id={`${id}-list`} class="options" role="listbox" ref={list}>
          <For each={matches()} fallback={<p class="empty">No models match.</p>}>
            {(entry, i) => (
              // biome-ignore lint/a11y/useFocusableInteractive: focus stays in the search field, which names the active option with aria-activedescendant.
              <div
                role="option"
                id={`${id}-${i()}`}
                class="option"
                aria-selected={i() === active() ? "true" : "false"}
                onPointerMove={() => setActive(i())}
                onClick={() => choose(entry)}
              >
                <HarnessIcon harness={entry.harness} />
                <span class="model">{entry.model.name}</span>
                <span class={["icon check", { invisible: !isCurrent(entry) }]} />
              </div>
            )}
          </For>
        </div>
        <Effort
          efforts={current()?.model.efforts ?? []}
          value={props.value.effort}
          disabled={props.disabled}
          onChange={(effort) => props.onChange({ ...props.value, effort })}
        />
      </div>
    </>
  );
}

// Effort is always shown; it moves only when the model offers a choice.
function Effort(props: { efforts: Option[]; value: string; disabled?: boolean; onChange(id: string): void }) {
  const [dragged, setDragged] = createSignal<number>();
  const last = () => Math.max(props.efforts.length - 1, 1);
  const position = () =>
    dragged() ??
    Math.max(
      props.efforts.findIndex((e) => e.id === props.value),
      0,
    );
  return (
    <div class="effort">
      <span>Effort</span>
      <input
        type="range"
        min="0"
        max={last()}
        step="1"
        value={position()}
        disabled={props.disabled || props.efforts.length < 2}
        aria-label="Effort"
        aria-valuetext={props.efforts[position()]?.name}
        style={{ "--p": position() / last() }}
        onInput={(event) => setDragged(Number(event.currentTarget.value))}
        onChange={(event) => {
          setDragged(undefined);
          const effort = props.efforts[Number(event.currentTarget.value)];
          if (effort && effort.id !== props.value) props.onChange(effort.id);
        }}
      />
      <Show when={props.efforts.length}>
        <div class="ticks" aria-hidden="true">
          <For each={props.efforts}>{(effort, i) => <span style={{ "--p": i() / last() }}>{effort.name}</span>}</For>
        </div>
      </Show>
    </div>
  );
}

// A saved setting can outlive its entry in the harness catalog. A pending
// value shows a spinner until it is confirmed.
export function Select(props: {
  name: string;
  options: Option[];
  value: string;
  disabled?: boolean;
  pending?: boolean;
  onChange?(id: string): void;
}) {
  const options = () =>
    props.value && !props.options.some((o) => o.id === props.value)
      ? [...props.options, { id: props.value, name: props.value }]
      : props.options;
  // Chrome treats a clicked select as keyboard-focused; the ring is for focus
  // that arrives by keyboard.
  const [pointer, setPointer] = createSignal(false);
  return (
    <span class={["select", { pointer: pointer() }]}>
      <select
        name={props.name}
        disabled={props.disabled}
        aria-busy={props.pending ? "true" : undefined}
        onPointerDown={() => setPointer(true)}
        onBlur={() => setPointer(false)}
        onChange={(event) => props.onChange?.(event.currentTarget.value)}
      >
        <For each={options()}>
          {(option) => (
            <option value={option.id} selected={option.id === props.value}>
              {option.name}
            </option>
          )}
        </For>
      </select>
      <span class={["icon", props.pending ? "loader" : "chevron-down"]} />
    </span>
  );
}

export function defaultSettings(name: string, harness: Harness, defaults?: Defaults): Settings {
  if (defaults?.harness === name) {
    return { model: defaults.model, effort: defaults.effort, permission_mode: defaults.permission_mode };
  }
  const model = harness.models[0];
  return {
    model: model?.id ?? "",
    effort: model?.efforts[0]?.id ?? "",
    permission_mode: harness.permission_modes[0]?.id ?? "",
  };
}
