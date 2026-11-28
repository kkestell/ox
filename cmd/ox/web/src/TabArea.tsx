import type { JSX } from "@solidjs/web";
import { createEffect, createMemo, createSignal, For, Match, Show, Switch, untrack } from "solid-js";
import { ChangesView, Counts, type Listing } from "./Changes";
import { fileDrag, tabDrag } from "./drag";
import { FileView } from "./FileView";
import { languageFor } from "./highlight";
import { type Panels, PanelToggle, Sash, SessionsToggle } from "./Panels";
import type { FileRoot } from "./protocol";
import type { Attention, SessionControl } from "./sessionControl";
import { AttentionMark, type PickerTab, TabPicker } from "./TabPicker";
import {
  activate,
  type Branch,
  closeTab,
  columns,
  dropZone,
  type FileMode,
  findTab,
  fits,
  focus,
  focusedGroup,
  focusedTab,
  type Group,
  groups,
  type Layout,
  leftmostTop,
  openTab,
  placeTab,
  rightmostTop,
  setMode,
  setSizes,
  type Tab,
  type Target,
  tabKey,
} from "./tabs";

// What the chat tab shows: its label, the directory as its tooltip, and the
// session's or draft's view, and what the chat holds that needs the user.
export type Chat = {
  label: () => string;
  title: () => string | undefined;
  attention: () => Attention | undefined;
  view: () => JSX.Element;
};

const baseName = (path: string) => path.slice(path.lastIndexOf("/") + 1);

// mode is how a file tab shows its file: a Markdown file rendered unless its
// tab chose source, any other file as source.
const modeOf = (tab: Extract<Tab, { kind: "file" }>): FileMode =>
  tab.mode ?? (languageFor(tab.path) === "markdown" ? "preview" : "source");

const icons = { chat: "message-square", changes: "file-diff", file: "file" } as const;

// TabArea shows the session's layout: groups of tabs, split by sashes. The
// narrow layout shows only the focused group's active tab under a bar with the
// tab picker. Dragging a tab, or a file from the file tree, onto a group moves
// it there; the edge a drag is over shows where the group will split.
type Props = {
  control: SessionControl;
  root: FileRoot;
  listing: Listing;
  panels: Panels;
  wrap: boolean;
  onWrap(wrap: boolean): void;
  chat: Chat;
};

export function TabArea(props: Props) {
  const layout = () => props.control.layout();
  const edit = props.control.edit;
  // over is where the drag in progress would drop, the page's own fact.
  const [over, setOver] = createSignal<{ group: number; target: Target }>();
  const area: Area = { props, layout, edit, over, setOver, dragging: {} };
  const focused = () => focusedGroup(layout());
  return (
    <Show when={!props.panels.narrow()} fallback={<GroupView area={area} group={focused()} phone />}>
      <div class="tab-area">
        <NodeView area={area} node={layout().root} path={[]} />
      </div>
    </Show>
  );
}

type Area = {
  props: Props;
  layout: () => Layout;
  edit: SessionControl["edit"];
  over: () => { group: number; target: Target } | undefined;
  setOver(over: { group: number; target: Target } | undefined): void;
  // The key of the tab being dragged; a drag from the file tree has none.
  dragging: { key?: string };
};

const nodeKey = (node: Branch): string =>
  node.kind === "group" ? `g${node.group.id}` : `s${nodeKey(node.children[0])}`;

function NodeView(props: { area: Area; node: Branch; path: number[] }) {
  return (
    <Switch>
      <Match when={props.node.kind === "group" && props.node.group}>
        {(group) => <GroupView area={props.area} group={group()} />}
      </Match>
      <Match when={props.node.kind === "split" && props.node}>
        {(split) => (
          <div class={["split", split().direction]}>
            <For each={split().children} keyed={nodeKey}>
              {(child, i) => (
                <>
                  <Show when={i() > 0}>
                    <Sash
                      label="Resize groups"
                      horizontal={split().direction === "column"}
                      drag={(before, after) => {
                        const at = i();
                        const base = untrack(() => split().sizes);
                        const pair = base[at - 1] + base[at];
                        return (travel) => {
                          const sizes = [...base];
                          sizes[at - 1] = (pair * (before + travel)) / (before + after);
                          sizes[at] = pair - sizes[at - 1];
                          props.area.edit((l) => setSizes(l, props.path, sizes));
                        };
                      }}
                    />
                  </Show>
                  <div class="cell" style={{ "flex-grow": split().sizes[i()] }}>
                    <NodeView area={props.area} node={child()} path={[...props.path, i()]} />
                  </div>
                </>
              )}
            </For>
          </div>
        )}
      </Match>
    </Switch>
  );
}

// GroupView shows one group: its tab strip, or on a phone the bar with the tab
// picker, over its active tab. The chat tab stays mounted while another tab of
// its group shows, so the transcript keeps its place.
function GroupView(props: { area: Area; group: Group; phone?: boolean }) {
  const { area } = props;
  const active = () => props.group.tabs[props.group.active];
  const hasChat = () => props.group.tabs.some((tab) => tab.kind === "chat");
  const shownPath = () => {
    const tab = active();
    return tab.kind === "file" ? tab.path : undefined;
  };
  const reveal = () => {
    const tab = active();
    return tab.kind === "changes" ? tab.reveal : undefined;
  };
  const mode = () => {
    const tab = active();
    return tab.kind === "file" ? modeOf(tab) : "source";
  };
  let body!: HTMLDivElement;
  let strip: HTMLDivElement | undefined;
  const accepts = (event: DragEvent) =>
    event.dataTransfer?.types.some((type) => type === tabDrag || type === fileDrag) ?? false;
  // targetAt is where a drop at the pointer lands: among the strip's tabs, or
  // on the body's zone.
  const targetAt = (event: DragEvent): Target => {
    const id = props.group.id;
    if (strip?.contains(event.target as globalThis.Node)) {
      const tab = (event.target as Element).closest<HTMLElement>("[data-index]");
      if (!tab) return { kind: "tabs", group: id };
      const box = tab.getBoundingClientRect();
      const at = Number(tab.dataset.index);
      return { kind: "tabs", group: id, index: event.clientX < box.left + box.width / 2 ? at : at + 1 };
    }
    return { kind: "zone", group: id, zone: dropZone(body.getBoundingClientRect(), event.clientX, event.clientY) };
  };
  const dropped = (event: DragEvent): Tab | undefined => {
    const key = event.dataTransfer?.getData(tabDrag);
    if (key) return findTab(area.layout(), key);
    const path = event.dataTransfer?.getData(fileDrag);
    return path ? { kind: "file", path } : undefined;
  };
  // allowed is whether a drop at target keeps every group at its minimum width:
  // a split beside a group needs room, any other drop does not. The minimum is
  // the tab area's own, over the columns its layout has now.
  const allowed = (target: Target, tab: Tab | undefined) => {
    if (target.kind !== "zone" || (target.zone !== "left" && target.zone !== "right")) return true;
    const root = body.closest<HTMLElement>(".tab-area");
    if (!root) return true;
    const layout = area.layout();
    const min = Number.parseFloat(getComputedStyle(root).minWidth) / columns(layout.root);
    const moving = tab ?? { kind: "file" as const, path: "" };
    return fits(layout, moving, target, root.clientWidth, min);
  };
  const preview = () => {
    const current = area.over();
    return current?.group === props.group.id && current.target.kind === "zone" ? current.target.zone : undefined;
  };
  const dropping = () => {
    const current = area.over();
    return current?.group === props.group.id && current.target.kind === "tabs";
  };
  return (
    <section
      class={["group", { focused: !props.phone && area.layout().focused === props.group.id }]}
      aria-label="Tab group"
      onPointerDown={() => area.edit((l) => focus(l, props.group.id))}
      onFocusIn={() => area.edit((l) => focus(l, props.group.id))}
      onDragOver={(event) => {
        if (props.phone || !accepts(event)) return;
        const target = targetAt(event);
        const key = area.dragging.key;
        if (!allowed(target, key ? findTab(area.layout(), key) : undefined)) {
          area.setOver(undefined);
          return;
        }
        event.preventDefault();
        area.setOver({ group: props.group.id, target });
      }}
      onDragLeave={(event) => {
        if (!event.currentTarget.contains(event.relatedTarget as globalThis.Node | null)) area.setOver(undefined);
      }}
      onDrop={(event) => {
        if (props.phone || !accepts(event)) return;
        event.preventDefault();
        const tab = dropped(event);
        const target = targetAt(event);
        area.setOver(undefined);
        if (tab && allowed(target, tab)) area.edit((l) => placeTab(l, tab, target));
      }}
    >
      <Show
        when={props.phone}
        fallback={<Strip area={area} group={props.group} ref={(el) => (strip = el)} dropping={dropping()} />}
      >
        <PhoneBar area={area} />
      </Show>
      <div ref={body} class="group-body">
        <Show when={hasChat()}>
          <main hidden={active().kind !== "chat"}>{area.props.chat.view()}</main>
        </Show>
        <Switch>
          <Match when={active().kind === "changes"}>
            <ChangesView
              listing={area.props.listing}
              wrap={area.props.wrap}
              reveal={reveal()}
              onOpen={(path) => area.edit((l) => openTab(l, { kind: "file", path }))}
            />
          </Match>
          <Match when={shownPath()} keyed>
            {(path) => <FileView root={area.props.root} path={path} mode={mode()} wrap={area.props.wrap} />}
          </Match>
        </Switch>
        <Show when={preview()}>{(zone) => <div class={["drop-preview", zone()]} />}</Show>
      </div>
    </section>
  );
}

const parentOf = (path: string) => {
  const at = path.lastIndexOf("/");
  return at > 0 ? path.slice(0, at) : undefined;
};

// label and title are a tab's text and its tooltip.
function labelOf(chat: Chat, tab: Tab) {
  return tab.kind === "chat" ? chat.label() : tab.kind === "changes" ? "Changes" : baseName(tab.path);
}

function titleOf(chat: Chat, tab: Tab) {
  return tab.kind === "chat" ? chat.title() : tab.kind === "file" ? tab.path : undefined;
}

// Actions are the active tab's view actions: Source and Preview for a Markdown
// file, and word wrap for a file's source and the Changes view.
function Actions(props: { area: Area; tab: Tab }) {
  const { area } = props;
  const file = () => (props.tab.kind === "file" ? props.tab : undefined);
  const markdown = () => {
    const tab = file();
    return tab !== undefined && languageFor(tab.path) === "markdown";
  };
  const wraps = () => {
    const tab = file();
    return props.tab.kind === "changes" || (tab !== undefined && modeOf(tab) === "source");
  };
  const modeButton = (tab: Extract<Tab, { kind: "file" }>, mode: FileMode, label: string, icon: string) => (
    <button
      type="button"
      class={["icon", icon]}
      aria-label={label}
      title={label}
      aria-pressed={modeOf(tab) === mode ? "true" : "false"}
      onClick={() => area.edit((l) => setMode(l, tab.path, mode))}
    />
  );
  return (
    <>
      <Show when={markdown() && file()}>
        {(tab) => (
          <fieldset aria-label="Show as" class="toggle-group">
            {modeButton(tab(), "source", "Source", "code")}
            {modeButton(tab(), "preview", "Preview", "eye")}
          </fieldset>
        )}
      </Show>
      <Show when={wraps()}>
        <button
          type="button"
          class="icon wrap-text"
          aria-label="Toggle word wrap"
          title="Toggle word wrap"
          aria-pressed={area.props.wrap ? "true" : "false"}
          onClick={() => area.props.onWrap(!area.props.wrap)}
        />
      </Show>
    </>
  );
}

// Strip is a group's tab strip. The session list's toggle starts the leftmost
// top strip while the list is closed, and the panel's toggle ends the
// rightmost top one while the panel is closed.
function Strip(props: { area: Area; group: Group; ref: (el: HTMLDivElement) => void; dropping: boolean }) {
  const { area } = props;
  const id = () => props.group.id;
  let tabs!: HTMLDivElement;
  // Whatever activates a tab brings it into the strip: the tab's own strip
  // scrolls, and no ancestor does.
  createEffect(
    () => tabKey(props.group.tabs[props.group.active]),
    () => {
      const tab = tabs.querySelector<HTMLElement>('[role="tab"][aria-selected="true"]')?.parentElement;
      if (!tab) return;
      const box = tab.getBoundingClientRect();
      const strip = tabs.getBoundingClientRect();
      if (box.left < strip.left) tabs.scrollLeft += box.left - strip.left;
      else if (box.right > strip.right) tabs.scrollLeft += box.right - strip.right;
    },
  );
  return (
    <div ref={props.ref} class={["strip", { dropping: props.dropping }]}>
      <Show when={leftmostTop(area.layout()) === id() && !area.props.panels.sessions()}>
        <SessionsToggle panels={area.props.panels} />
      </Show>
      <div ref={tabs} role="tablist" aria-label="Tabs" class="tabs">
        <For each={props.group.tabs} keyed={tabKey}>
          {(tab, i) => {
            const label = () => labelOf(area.props.chat, tab());
            return (
              // biome-ignore lint/a11y/noStaticElementInteractions: a drag moves the tab; Move to a group by keyboard is not offered, and the tab's button takes focus.
              <div
                class={["tab", { active: props.group.active === i() }]}
                data-index={i()}
                draggable="true"
                onDragStart={(event) => {
                  area.dragging.key = tabKey(tab());
                  event.dataTransfer?.setData(tabDrag, tabKey(tab()));
                  if (event.dataTransfer) event.dataTransfer.effectAllowed = "move";
                }}
                onDragEnd={() => {
                  area.dragging.key = undefined;
                  area.setOver(undefined);
                }}
              >
                <button
                  type="button"
                  role="tab"
                  aria-selected={props.group.active === i() ? "true" : "false"}
                  title={titleOf(area.props.chat, tab())}
                  onClick={() => area.edit((l) => activate(l, tabKey(tab())))}
                >
                  <span class={["icon", icons[tab().kind]]} />
                  <span class="label">{label()}</span>
                  <Show
                    when={tab().kind === "chat" && props.group.active !== i() ? area.props.chat.attention() : undefined}
                  >
                    {(attention) => <AttentionMark attention={attention()} />}
                  </Show>
                  <Show when={tab().kind === "changes"}>
                    <Counts added={area.props.listing.total().added} removed={area.props.listing.total().removed} />
                  </Show>
                </button>
                <Show when={tab().kind !== "chat"}>
                  <button
                    type="button"
                    class="icon x"
                    aria-label={`Close ${label()}`}
                    title={`Close ${label()}`}
                    onClick={() => area.edit((l) => closeTab(l, tabKey(tab())))}
                  />
                </Show>
              </div>
            );
          }}
        </For>
      </div>
      <div class="editor-actions">
        <Actions area={area} tab={props.group.tabs[props.group.active]} />
        <Show when={rightmostTop(area.layout()) === id() && !area.props.panels.right()}>
          <PanelToggle panels={area.props.panels} />
        </Show>
      </div>
    </div>
  );
}

// PhoneBar is the narrow layout's header: the session list's toggle, the tab
// picker over every tab of the layout, the active tab's actions, and the
// panel's toggle.
function PhoneBar(props: { area: Area }) {
  const { area } = props;
  const tabs = createMemo<PickerTab[]>(() => {
    const layout = area.layout();
    const shown = layout.focused;
    return groups(layout.root).flatMap((group) =>
      group.tabs.map((tab, i) => ({
        key: tabKey(tab),
        label: labelOf(area.props.chat, tab),
        title: titleOf(area.props.chat, tab),
        detail: tab.kind === "file" ? parentOf(tab.path) : undefined,
        icon: icons[tab.kind],
        counts: tab.kind === "changes" ? area.props.listing.total() : undefined,
        active: group.id === shown && group.active === i,
        closable: tab.kind !== "chat",
        attention: tab.kind === "chat" ? area.props.chat.attention() : undefined,
      })),
    );
  });
  const focused = () => focusedTab(area.layout());
  return (
    <div class="strip phone" role="toolbar" aria-label="Tabs">
      <SessionsToggle panels={area.props.panels} />
      <TabPicker
        tabs={tabs()}
        onPick={(key) => area.edit((l) => activate(l, key))}
        onClose={(key) => area.edit((l) => closeTab(l, key))}
      />
      <div class="editor-actions">
        <Actions area={area} tab={focused()} />
        <PanelToggle panels={area.props.panels} />
      </div>
    </div>
  );
}
