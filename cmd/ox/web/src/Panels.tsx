import { createSignal, onSettled, Show } from "solid-js";

// Below this width the page shows one pane at a time, with the side panels as
// drawers. The layout's root carries the result as its narrow class, which the
// stylesheet reads.
const narrowQuery = "(max-width: 767px)";

type Drawer = "sessions" | "tree";

// Mode is what the workbench beside the transcript shows: a file beside the
// file tree, or the changes.
export type Mode = "files" | "changes";

// createPanels holds which of the page's panels show, for the life of the
// page. The wide layout shows the session list while its flag is set, and the
// workbench beside the transcript while it has a mode, each at its own width.
// The narrow layout shows the workbench in the transcript's place while it has
// a mode of its own, and at most one drawer over the page, the session list or
// the file tree, starting with drawerAtLoad. Crossing the breakpoint closes the
// drawer and the narrow layout's workbench, and leaves the wide layout's panels
// as they were. The workbench shows the same mode for every session.
export function createPanels(drawerAtLoad: "sessions" | undefined) {
  const media = matchMedia(narrowQuery);
  const [narrow, setNarrow] = createSignal(media.matches);
  const [drawer, setDrawer] = createSignal<Drawer | undefined>(drawerAtLoad);
  const [left, setLeft] = createSignal(true);
  const [wideMode, setWideMode] = createSignal<Mode>();
  const [narrowMode, setNarrowMode] = createSignal<Mode>();
  const [leftWidth, setLeftWidth] = createSignal(288);
  const [rightWidth, setRightWidth] = createSignal(240);
  onSettled(() => {
    const follow = () => {
      setNarrow(media.matches);
      setDrawer(undefined);
      setNarrowMode(undefined);
    };
    media.addEventListener("change", follow);
    return () => media.removeEventListener("change", follow);
  });
  const toggle = (which: Drawer) => setDrawer((open) => (open === which ? undefined : which));
  const mode = () => (narrow() ? narrowMode() : wideMode());
  // setMode shows the workbench in mode, or closes it.
  const setMode = (next: Mode | undefined) => {
    setDrawer(undefined);
    if (narrow()) setNarrowMode(next);
    else setWideMode(next);
  };
  return {
    narrow,
    sessions: () => (narrow() ? drawer() === "sessions" : left()),
    mode,
    setMode,
    // treeDrawer is whether the narrow layout shows the file tree as a drawer
    // over the file.
    treeDrawer: () => narrow() && mode() === "files" && drawer() === "tree",
    toggleSessions: () => (narrow() ? toggle("sessions") : setLeft((shown) => !shown)),
    toggleTree: () => toggle("tree"),
    // dismiss closes the open drawer; the wide layout has none.
    dismiss: () => setDrawer(undefined),
    leftWidth,
    rightWidth,
    setLeftWidth,
    setRightWidth,
  };
}

export type Panels = ReturnType<typeof createPanels>;

// PanelToggles shows the session list's toggle, while the list is hidden, and
// the workbench's tabs, while there is a workbench to show. The session list's
// toggle sits in the list's own header while the list shows.
export function PanelToggles(props: { panels: Panels; workbench: boolean }) {
  return (
    <div class="panel-toggles">
      <Show when={!props.panels.sessions()}>
        <SessionsToggle panels={props.panels} />
      </Show>
      <Show when={props.workbench}>
        <ModeTabs panels={props.panels} />
      </Show>
    </div>
  );
}

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

// ModeTabs opens the workbench on the files or the changes, with neither
// selected while it is closed: a group of icon toggles, as the file view's
// modes are.
export function ModeTabs(props: { panels: Panels }) {
  const tab = (mode: Mode, label: string, icon: string) => (
    <button
      type="button"
      role="tab"
      class={["icon", icon]}
      aria-label={label}
      title={label}
      aria-selected={props.panels.mode() === mode ? "true" : "false"}
      onClick={() => props.panels.setMode(mode)}
    />
  );
  return (
    <div role="tablist" aria-label="Workbench" class="toggle-group mode-tabs">
      {tab("files", "Files", "files")}
      {tab("changes", "Changes", "file-diff")}
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
