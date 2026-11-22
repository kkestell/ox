import { createSignal, onSettled } from "solid-js";

const clamp = (value: number, min: number, max: number) => Math.min(Math.max(value, min), max);
const panelWidth = (width: number) => clamp(width, 160, 640);

// Below this width the page shows one pane at a time, with the side panels as
// drawers. The layout's root carries the result as its narrow class, which the
// stylesheet reads.
const narrowQuery = "(max-width: 767px)";

export type Drawer = "sessions" | "files";

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
  const [leftWidth, writeLeftWidth] = createSignal(288);
  const [rightWidth, writeRightWidth] = createSignal(288);
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
    setLeftWidth: (width: number) => writeLeftWidth(panelWidth(width)),
    setRightWidth: (width: number) => writeRightWidth(panelWidth(width)),
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

// Sash resizes the columns on either side of it, by pointer only, as VS Code's
// sashes do. A drag passes both columns' widths at its start to drag, and the
// pointer's horizontal travel since then to the function drag returns.
export function Sash(props: { label: string; drag(before: number, after: number): (dx: number) => void }) {
  const [move, setMove] = createSignal<(x: number) => void>();
  const start = (event: PointerEvent & { currentTarget: HTMLHRElement }) => {
    if (event.button !== 0) return;
    event.preventDefault();
    const sash = event.currentTarget;
    sash.setPointerCapture(event.pointerId);
    const width = (element: Element | null) => element?.getBoundingClientRect().width ?? 0;
    const resize = props.drag(width(sash.previousElementSibling), width(sash.nextElementSibling));
    const x = event.clientX;
    setMove(() => (clientX: number) => resize(clientX - x));
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
