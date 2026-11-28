import { createUniqueId, For } from "solid-js";
import { harnessName } from "./HarnessIcon";
import { useOx } from "./ox";
import type { UsageWindow } from "./protocol";

// windowLabel names a window by its length.
function windowLabel(seconds: number) {
  if (seconds >= 86400) return `${Math.round(seconds / 86400)}d`;
  if (seconds >= 3600) return `${Math.round(seconds / 3600)}h`;
  return `${Math.round(seconds / 60)}m`;
}

// timeLeft formats minutes as days and hours, hours and minutes, or minutes.
function timeLeft(minutes: number) {
  const hours = Math.floor(minutes / 60);
  if (minutes >= 1440) return `${Math.floor(hours / 24)}d ${hours % 24}h`;
  return hours > 0 ? `${hours}h ${minutes % 60}m` : `${minutes}m`;
}

// Usage is the composer's subscription usage: the shortest window of the
// session's latest reading, and a popover listing every window. Each window
// shows an open ring of the limit used with a marker for how much of the
// window has passed. The button is named "Subscription usage" followed by its
// content, so its visible text stays part of its name.
//
// The popover opens over the button, longest window first, so the shortest
// window's row stays where the button showed it. The button lays out every
// window in hidden rows, so its columns are as wide as the popover's.
export function Usage(props: { harness: string; windows: UsageWindow[] }) {
  const id = createUniqueId();
  return (
    <>
      <span id={`${id}-label`} hidden>
        Subscription usage
      </span>
      <button
        type="button"
        id={`${id}-button`}
        class="usage"
        aria-labelledby={`${id}-label ${id}-button`}
        popovertarget={id}
        style={{ "anchor-name": `--${id}` }}
      >
        <For each={props.windows}>
          {(window, i) => <WindowRow harness={props.harness} window={window} hidden={i() > 0} />}
        </For>
      </button>
      <div
        id={id}
        popover="auto"
        role="dialog"
        aria-label="Subscription usage"
        class="usage-windows"
        style={{ "position-anchor": `--${id}` }}
        onClick={(e) => e.currentTarget.hidePopover()}
      >
        <div class="rows">
          <For each={props.windows.toReversed()}>
            {(window) => <WindowRow harness={props.harness} window={window} />}
          </For>
        </div>
      </div>
    </>
  );
}

// WindowRow fills one row of its container's four-column grid: the window's
// length, its ring, the percent used, and the time until it resets. A window
// that has reset dims until the harness reports a new reading. A hidden row
// takes space in the grid's columns and shows nothing.
function WindowRow(props: { harness: string; window: UsageWindow; hidden?: boolean }) {
  const ox = useOx();
  const label = () => windowLabel(props.window.interval_seconds);
  const used = () => Math.round(props.window.used_percent);
  const left = () => Math.max(0, Math.ceil((Date.parse(props.window.resets_at) - ox.clock()) / 60000));
  const reset = () => left() === 0;
  const elapsed = () => Math.min(100, Math.max(0, 100 - ((left() * 60) / props.window.interval_seconds) * 100));
  const waiting = () =>
    reset()
      ? `${used()}% last reported; this window reset. Waiting for a new reading from ${harnessName(props.harness)}.`
      : undefined;
  const level = () => (used() >= 90 ? "high" : used() >= 75 ? "warn" : undefined);
  const cell = (name: string) => [name, { reset: reset(), hidden: props.hidden }];
  return (
    <>
      <span class={cell("label")} title={waiting()}>
        {label()}
      </span>
      {/* biome-ignore lint/a11y/useSemanticElements: the ring holds the elapsed marker, and a <meter> renders no children. */}
      <span
        class={cell("ring")}
        role="meter"
        aria-label={`${label()} usage`}
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={used()}
        aria-valuetext={waiting()}
        title={waiting()}
      >
        {/* A 270° arc with its gap at the bottom: each circle's path is 100 long, and 135° puts the arc's start at the
            bottom left. The marker is drawn at the top, so 225° puts it on the arc's start. */}
        <svg viewBox="0 0 16 16" aria-hidden="true">
          <circle
            class="track"
            cx="8"
            cy="8"
            r="7"
            pathLength="100"
            stroke-dasharray="75 25"
            transform="rotate(135 8 8)"
          />
          <circle
            class={["fill", level()]}
            cx="8"
            cy="8"
            r="7"
            pathLength="100"
            stroke-dasharray={`${0.75 * Math.min(100, used())} 100`}
            transform="rotate(135 8 8)"
          />
          <line class="elapsed" x1="8" y1="0" x2="8" y2="3" transform={`rotate(${225 + 2.7 * elapsed()} 8 8)`} />
        </svg>
      </span>
      <span class={cell("percent")} title={waiting()}>
        {used()}%
      </span>
      <span class={cell("left")} title={waiting()}>
        {reset() ? "Waiting" : timeLeft(left())}
      </span>
    </>
  );
}
