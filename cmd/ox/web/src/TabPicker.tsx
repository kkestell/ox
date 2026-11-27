import { createUniqueId, For, Show } from "solid-js";
import { Counts } from "./Changes";
import type { Attention } from "./ox";

// attentionLabel is the text of the mark a hidden chat tab carries.
const attentionLabel = (attention: Attention) => (attention.kind === "waiting" ? "Waiting on you" : attention.text);

// AttentionMark is the dot on a chat tab whose hidden chat needs the user.
export function AttentionMark(props: { attention: Attention }) {
  return (
    <span
      class={["status", "attention", props.attention.kind]}
      role="img"
      title={attentionLabel(props.attention)}
      aria-label={attentionLabel(props.attention)}
    />
  );
}

export type PickerTab = {
  key: string;
  label: string;
  title?: string;
  detail?: string;
  icon: string;
  counts?: { added: number; removed: number };
  active: boolean;
  closable: boolean;
  attention?: Attention;
};

// TabPicker shows the active tab's label and, while more than one tab is open,
// opens a menu of every tab, the narrow layout's tab strip. Choosing a tab
// shows it; a closable tab has a button that closes it.
export function TabPicker(props: { tabs: PickerTab[]; onPick(key: string): void; onClose(key: string): void }) {
  const id = createUniqueId();
  let menu!: HTMLDivElement;
  const current = () => props.tabs.find((tab) => tab.active);
  // The button carries the mark of a tab that needs the user and is not shown.
  const hidden = () => props.tabs.find((tab) => tab.attention && !tab.active)?.attention;
  const shown = () => (
    <Show when={current()}>
      {(tab) => (
        <>
          <span class={["icon", tab().icon]} />
          <span class="label">{tab().label}</span>
        </>
      )}
    </Show>
  );
  return (
    <Show
      when={props.tabs.length > 1}
      fallback={
        <div class="tab-picker" title={current()?.title}>
          {shown()}
        </div>
      }
    >
      <button type="button" class="tab-picker" popovertarget={id} aria-haspopup="menu" title={current()?.title}>
        {shown()}
        <Show when={hidden()}>{(attention) => <AttentionMark attention={attention()} />}</Show>
        <span class="icon chevrons-up-down" />
      </button>
      <div id={id} ref={menu} popover="auto" role="menu" aria-label="Tabs" class="menu tab-menu">
        <For each={props.tabs} keyed={(tab) => tab.key}>
          {(tab) => (
            <div class="item" role="none">
              <button
                type="button"
                role="menuitemradio"
                aria-checked={tab().active ? "true" : "false"}
                title={tab().title}
                onClick={() => {
                  menu.hidePopover();
                  props.onPick(tab().key);
                }}
              >
                <span class={["icon", tab().icon]} />
                <span class="label">{tab().label}</span>
                <Show when={tab().detail}>{(detail) => <span class="detail">{detail()}</span>}</Show>
                <Show when={tab().attention}>{(attention) => <AttentionMark attention={attention()} />}</Show>
                <Show when={tab().counts}>{(each) => <Counts added={each().added} removed={each().removed} />}</Show>
              </button>
              <Show when={tab().closable}>
                <button
                  type="button"
                  class="icon x"
                  aria-label={`Close ${tab().label}`}
                  title={`Close ${tab().label}`}
                  onClick={() => props.onClose(tab().key)}
                />
              </Show>
            </div>
          )}
        </For>
      </div>
    </Show>
  );
}
