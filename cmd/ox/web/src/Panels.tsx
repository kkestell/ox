import { createSignal, onSettled } from "solid-js";

// Below this width the page shows one pane at a time, with the side panels as
// drawers. The layout's root carries the result as its narrow class, which the
// stylesheet reads.
const narrowQuery = "(max-width: 767px)";

type Drawer = "sessions" | "files";

// createPanels holds which of the page's side panels show, for the life of the
// page. The wide layout shows each panel at its own width while its flag is
// set; the narrow layout shows at most one, as a drawer over the page, starting
// with drawerAtLoad. Crossing the breakpoint closes the drawer, and leaves the
// wide layout's panels as they were.
export function createPanels(drawerAtLoad: Drawer | undefined) {
  const media = matchMedia(narrowQuery);
  const [narrow, setNarrow] = createSignal(media.matches);
  const [drawer, setDrawer] = createSignal<Drawer | undefined>(drawerAtLoad);
  const [left, setLeft] = createSignal(true);
  const [right, setRight] = createSignal(true);
  const [leftWidth, setLeftWidth] = createSignal(288);
  const [rightWidth, setRightWidth] = createSignal(288);
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
    files: () => (narrow() ? drawer() === "files" : right()),
    toggleSessions: () => (narrow() ? toggle("sessions") : setLeft((shown) => !shown)),
    toggleFiles: () => (narrow() ? toggle("files") : setRight((shown) => !shown)),
    // dismiss closes the open drawer; the wide layout has none.
    dismiss: () => setDrawer(undefined),
    leftWidth,
    rightWidth,
    setLeftWidth,
    setRightWidth,
  };
}

export type Panels = ReturnType<typeof createPanels>;

// PanelToggles shows and hides the session list and the file tree.
export function PanelToggles(props: { panels: Panels }) {
  return (
    <div class="panel-toggles">
      <button
        type="button"
        class="icon panel-left"
        aria-label="Toggle sessions"
        title="Toggle sessions"
        aria-pressed={props.panels.sessions() ? "true" : "false"}
        onClick={() => props.panels.toggleSessions()}
      />
      <button
        type="button"
        class="icon panel-right"
        aria-label="Toggle files"
        title="Toggle files"
        aria-pressed={props.panels.files() ? "true" : "false"}
        onClick={() => props.panels.toggleFiles()}
      />
    </div>
  );
}

// column reads its width and its minimum and maximum widths from the layout.
// An unset maximum is unbounded.
function column(element: Element | null) {
  if (!element) return { width: 0, min: 0, max: Number.POSITIVE_INFINITY };
  const style = getComputedStyle(element);
  return {
    width: element.getBoundingClientRect().width,
    min: Number.parseFloat(style.minWidth) || 0,
    max: style.maxWidth === "none" ? Number.POSITIVE_INFINITY : Number.parseFloat(style.maxWidth),
  };
}

// Sash resizes the columns on either side of it, by pointer only, as VS Code's
// sashes do. A drag passes both columns' widths at its start to drag, and the
// pointer's horizontal travel since then to the function drag returns. The
// drag stops where either column reaches its minimum or maximum width, and
// passes travel only when it changes.
export function Sash(props: { label: string; drag(before: number, after: number): (dx: number) => void }) {
  const [move, setMove] = createSignal<(x: number) => void>();
  const start = (event: PointerEvent & { currentTarget: HTMLHRElement }) => {
    if (event.button !== 0) return;
    event.preventDefault();
    const sash = event.currentTarget;
    sash.setPointerCapture(event.pointerId);
    const before = column(sash.previousElementSibling);
    const after = column(sash.nextElementSibling);
    // The layout rendered both columns within their limits, so the range
    // holds the current position. Layout rounding leaves widths a fraction of a
    // pixel from their limits on either side, so the range ignores less than a
    // pixel of room in either direction.
    const lowest = Math.min(0, Math.ceil(Math.max(before.min - before.width, after.width - after.max)));
    const highest = Math.max(0, Math.floor(Math.min(before.max - before.width, after.width - after.min)));
    const resize = props.drag(before.width, after.width);
    const x = event.clientX;
    let last = 0;
    setMove(() => (clientX: number) => {
      const dx = Math.min(Math.max(clientX - x, lowest), highest);
      if (dx === last) return;
      last = dx;
      resize(dx);
    });
  };
  return (
    <hr
      class={["sash", { resizing: move() !== undefined }]}
      aria-orientation="vertical"
      aria-label={props.label}
      onPointerDown={start}
      onPointerMove={(event) => move()?.(event.clientX)}
      onLostPointerCapture={() => setMove(undefined)}
    />
  );
}
