import { type LucideIcon, PanelBottom, PanelLeft, PanelRight } from "lucide-react";
import {
  type CSSProperties,
  type PointerEvent,
  type ReactNode,
  useCallback,
  useLayoutEffect,
  useRef,
  useState,
} from "react";

type Panel = "left" | "bottom" | "right";

const panels: Panel[] = ["left", "bottom", "right"];
const minSizes: Record<Panel, number> = { left: 200, bottom: 200, right: 200 };
const minContentWidth = 200;
const minContentHeight = 100;

const toggles: Record<Panel, { icon: LucideIcon; title: string }> = {
  left: { icon: PanelLeft, title: "Toggle Primary Side Bar" },
  bottom: { icon: PanelBottom, title: "Toggle Panel" },
  right: { icon: PanelRight, title: "Toggle Secondary Side Bar" },
};

interface Drag {
  panel: Panel;
  start: number;
  startSize: number;
}

export function Workbench({ left, children }: { left: ReactNode; children: ReactNode }) {
  const workbench = useRef<HTMLDivElement>(null);
  const center = useRef<HTMLDivElement>(null);
  const drag = useRef<Drag | null>(null);
  const [visible, setVisible] = useState<Record<Panel, boolean>>({
    left: true,
    bottom: false,
    right: false,
  });
  const [sizes, setSizes] = useState<Record<Panel, number>>(minSizes);

  // `size` limited to what leaves the content its minimum, beside the other
  // visible panels and the sashes.
  const clamp = useCallback(
    (panel: Panel, size: number, current: Record<Panel, number>) => {
      let max: number;
      if (panel === "bottom") {
        max = center.current!.clientHeight - 1 - minContentHeight;
      } else {
        const other = panel === "left" ? "right" : "left";
        const otherWidth = visible[other] ? current[other] + 1 : 0;
        max = workbench.current!.clientWidth - otherWidth - 1 - minContentWidth;
      }
      return Math.max(minSizes[panel], Math.min(size, max));
    },
    [visible],
  );

  const resize = (panel: Panel, size: number) =>
    setSizes((current) => ({ ...current, [panel]: clamp(panel, size, current) }));

  // Fits the visible panels to the window, now and whenever either changes.
  useLayoutEffect(() => {
    const fit = () =>
      setSizes((current) => {
        let next = current;
        for (const panel of panels) {
          if (visible[panel]) next = { ...next, [panel]: clamp(panel, next[panel], next) };
        }
        return next;
      });
    fit();
    window.addEventListener("resize", fit);
    return () => window.removeEventListener("resize", fit);
  }, [visible, clamp]);

  const sash = (panel: Panel) => {
    const vertical = panel !== "bottom";
    const direction = panel === "left" ? 1 : -1;
    const position = (event: PointerEvent) => (vertical ? event.clientX : event.clientY);
    return (
      <div
        className={`sash ${vertical ? "vertical" : "horizontal"}`}
        hidden={!visible[panel]}
        onPointerDown={(event) => {
          if (event.button !== 0) return;
          event.preventDefault();
          event.currentTarget.setPointerCapture(event.pointerId);
          drag.current = { panel, start: position(event), startSize: sizes[panel] };
          document.body.classList.add("resizing", vertical ? "vertical" : "horizontal");
        }}
        onPointerMove={(event) => {
          const current = drag.current;
          if (current?.panel !== panel) return;
          resize(panel, current.startSize + direction * (position(event) - current.start));
        }}
        onLostPointerCapture={() => {
          drag.current = null;
          document.body.classList.remove("resizing", "vertical", "horizontal");
        }}
      />
    );
  };

  const style = Object.fromEntries(
    panels.map((panel) => [`--${panel}-size`, `${sizes[panel]}px`]),
  ) as CSSProperties;

  return (
    <div className="workbench" ref={workbench} style={style}>
      <aside className="panel left" hidden={!visible.left}>
        {left}
      </aside>
      {sash("left")}
      <div className="center" ref={center}>
        <main className="content">
          <header className="pane-header content-header">
            <div className="toggles">
              {panels.map((panel) => {
                const { icon: Icon, title } = toggles[panel];
                return (
                  <button
                    key={panel}
                    type="button"
                    className="icon-button"
                    title={title}
                    aria-label={title}
                    aria-pressed={visible[panel]}
                    onClick={() => setVisible((shown) => ({ ...shown, [panel]: !shown[panel] }))}
                  >
                    <Icon />
                  </button>
                );
              })}
            </div>
          </header>
          <div className="content-body">{children}</div>
        </main>
        {sash("bottom")}
        <section className="panel bottom" hidden={!visible.bottom} />
      </div>
      {sash("right")}
      <aside className="panel right" hidden={!visible.right} />
    </div>
  );
}
