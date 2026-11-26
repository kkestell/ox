import { createUniqueId, For, Show } from "solid-js";
import { Counts } from "./Changes";

export type PickerTab = {
  key: string;
  label: string;
  icon: string;
  counts?: { added: number; removed: number };
  active: boolean;
  closable: boolean;
};

// TabPicker shows the active tab's label and opens a menu of every tab, the
// narrow layout's tab strip. Choosing a tab shows it; a closable tab has a
// button that closes it.
export function TabPicker(props: { tabs: PickerTab[]; onPick(key: string): void; onClose(key: string): void }) {
  const id = createUniqueId();
  let menu!: HTMLDivElement;
  const current = () => props.tabs.find((tab) => tab.active);
  return (
    <>
      <button type="button" class="tab-picker" popovertarget={id} aria-haspopup="menu">
        <Show when={current()}>
          {(tab) => (
            <>
              <span class={["icon", tab().icon]} />
              <span class="label">{tab().label}</span>
            </>
          )}
        </Show>
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
                onClick={() => {
                  menu.hidePopover();
                  props.onPick(tab().key);
                }}
              >
                <span class={["icon", tab().icon]} />
                <span class="label">{tab().label}</span>
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
    </>
  );
}
