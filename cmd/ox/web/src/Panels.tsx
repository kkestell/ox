import { createSignal } from "solid-js";

const clamp = (value: number, min: number, max: number) => Math.min(Math.max(value, min), max);
const panelWidth = (width: number) => clamp(width, 160, 640);

// createPanels holds which of the page's side panels show and their widths,
// for the life of the page.
export function createPanels() {
  const [left, setLeft] = createSignal(true);
  const [right, setRight] = createSignal(true);
  const [leftWidth, writeLeftWidth] = createSignal(288);
  const [rightWidth, writeRightWidth] = createSignal(288);
  return {
    left,
    right,
    leftWidth,
    rightWidth,
    setLeft: (shown: boolean) => setLeft(shown),
    setRight: (shown: boolean) => setRight(shown),
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
        aria-pressed={props.panels.left() ? "true" : "false"}
        onClick={() => props.panels.setLeft(!props.panels.left())}
      />
      <button
        type="button"
        class="icon panel-right"
        aria-label="Toggle files"
        title="Toggle files"
        aria-pressed={props.panels.right() ? "true" : "false"}
        onClick={() => props.panels.setRight(!props.panels.right())}
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
