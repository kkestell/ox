import { createSignal, onSettled } from "solid-js";

// Below this width the page shows one tab at a time, with the side panels as
// drawers. The layout's root carries the result as its narrow class, which the
// stylesheet reads.
const narrowQuery = "(max-width: 767px)";

type Drawer = "sessions" | "right";

// createPanels holds which of the page's panels show, for the life of the
// page. The wide layout shows the session list and the right panel while
// their flags are set, each at its own width. The narrow layout shows at most
// one drawer over the page, the session list or the right panel, starting with
// drawerAtLoad. Crossing the breakpoint closes the drawer and leaves the wide
// layout's panels as they were.
export function createPanels(drawerAtLoad: "sessions" | undefined) {
  const media = matchMedia(narrowQuery);
  const [narrow, setNarrow] = createSignal(media.matches);
  const [drawer, setDrawer] = createSignal<Drawer | undefined>(drawerAtLoad);
  const [left, setLeft] = createSignal(true);
  const [right, setRight] = createSignal(false);
  const [leftWidth, setLeftWidth] = createSignal(288);
  const [rightWidth, setRightWidth] = createSignal(240);
  onSettled(() => {
    const follow = () => {
      setNarrow(media.matches);
      setDrawer(undefined);
    };
    media.addEventListener("change", follow);
    return () => media.removeEventListener("change", follow);
  });
  const toggle = (which: Drawer) => setDrawer((open) => (open === which ? undefined : which));
  return {
    narrow,
    sessions: () => (narrow() ? drawer() === "sessions" : left()),
    right: () => (narrow() ? drawer() === "right" : right()),
    toggleSessions: () => (narrow() ? toggle("sessions") : setLeft((shown) => !shown)),
    toggleRight: () => (narrow() ? toggle("right") : setRight((shown) => !shown)),
    // dismiss closes the open drawer; the wide layout has none.
    dismiss: () => setDrawer(undefined),
    leftWidth,
    rightWidth,
    setLeftWidth,
    setRightWidth,
  };
}

export type Panels = ReturnType<typeof createPanels>;

// SessionsToggle shows and hides the session list.
export function SessionsToggle(props: { panels: Panels }) {
  return (
    <button
      type="button"
      class="icon panel-left"
      aria-label="Toggle sessions"
      title="Toggle sessions"
      aria-pressed={props.panels.sessions() ? "true" : "false"}
      onClick={() => props.panels.toggleSessions()}
    />
  );
}

// PanelToggle shows and hides the panel at the right.
export function PanelToggle(props: { panels: Panels }) {
  return (
    <button
      type="button"
      class="icon panel-right"
      aria-label="Toggle panel"
      title="Toggle panel"
      aria-pressed={props.panels.right() ? "true" : "false"}
      onClick={() => props.panels.toggleRight()}
    />
  );
}

type Axis = "x" | "y";

// span reads an element's size along axis and its minimum and maximum sizes
// from the layout. An unset maximum is unbounded.
function span(element: Element | null, axis: Axis) {
  if (!element) return { size: 0, min: 0, max: Number.POSITIVE_INFINITY };
  const style = getComputedStyle(element);
  const [min, max] = axis === "x" ? [style.minWidth, style.maxWidth] : [style.minHeight, style.maxHeight];
  const box = element.getBoundingClientRect();
  return {
    size: axis === "x" ? box.width : box.height,
    min: Number.parseFloat(min) || 0,
    max: max === "none" ? Number.POSITIVE_INFINITY : Number.parseFloat(max),
  };
}

// Sash resizes the panels on either side of it, by pointer only, as VS Code's
// sashes do. A vertical bar, the default, sits between columns and drags
// horizontally; a horizontal one sits between rows. A drag passes both
// panels' sizes at its start to drag, and the pointer's travel since then to
// the function drag returns. The drag stops where either panel reaches its
// minimum or maximum size, and passes travel only when it changes.
export function Sash(props: {
  label: string;
  horizontal?: boolean;
  drag(before: number, after: number): (travel: number) => void;
}) {
  const axis = (): Axis => (props.horizontal ? "y" : "x");
  const [move, setMove] = createSignal<(at: number) => void>();
  const start = (event: PointerEvent & { currentTarget: HTMLHRElement }) => {
    if (event.button !== 0) return;
    event.preventDefault();
    const sash = event.currentTarget;
    sash.setPointerCapture(event.pointerId);
    const before = span(sash.previousElementSibling, axis());
    const after = span(sash.nextElementSibling, axis());
    // The layout rendered both panels within their limits, so the range
    // holds the current position. Layout rounding leaves sizes a fraction of a
    // pixel from their limits on either side, so the range ignores less than a
    // pixel of room in either direction.
    const lowest = Math.min(0, Math.ceil(Math.max(before.min - before.size, after.size - after.max)));
    const highest = Math.max(0, Math.floor(Math.min(before.max - before.size, after.size - after.min)));
    const resize = props.drag(before.size, after.size);
    const from = axis() === "x" ? event.clientX : event.clientY;
    let last = 0;
    setMove(() => (at: number) => {
      const travel = Math.min(Math.max(at - from, lowest), highest);
      if (travel === last) return;
      last = travel;
      resize(travel);
    });
  };
  return (
    <hr
      class={["sash", { horizontal: props.horizontal, resizing: move() !== undefined }]}
      aria-orientation={props.horizontal ? "horizontal" : "vertical"}
      aria-label={props.label}
      onPointerDown={start}
      onPointerMove={(event) => move()?.(axis() === "x" ? event.clientX : event.clientY)}
      onLostPointerCapture={() => setMove(undefined)}
    />
  );
}
