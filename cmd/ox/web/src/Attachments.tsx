import { createSignal, For, onCleanup, Show, untrack } from "solid-js";
import { imageTypes, type SessionControl } from "./sessionControl";

// filesOf is the files a drag or paste carries.
const filesOf = (data: DataTransfer | null) => [...(data?.files ?? [])];

const carriesFiles = (event: DragEvent) => event.dataTransfer?.types.includes("Files") === true;

// createImageDrop makes the transcript and composer take dropped files as
// attached images while the composer shows, and reports while files are
// dragged over either. A file dropped while the composer is hidden is
// discarded rather than opened by the browser.
export function createImageDrop(control: SessionControl, composing: () => boolean) {
  const [dragging, setDragging] = createSignal(false);
  const handlers = {
    onDragOver(event: DragEvent) {
      if (!carriesFiles(event)) return;
      event.preventDefault();
      setDragging(composing());
    },
    onDragLeave(event: DragEvent & { currentTarget: HTMLElement }) {
      const to = event.relatedTarget;
      if (!(to instanceof Node) || !event.currentTarget.contains(to)) setDragging(false);
    },
    onDrop(event: DragEvent) {
      if (!carriesFiles(event)) return;
      event.preventDefault();
      setDragging(false);
      if (composing()) control.attach(filesOf(event.dataTransfer));
    },
  };
  return { dragging, handlers };
}

// pasteImages attaches the images pasted into the prompt.
export function pasteImages(control: SessionControl) {
  return (event: ClipboardEvent) => {
    const files = filesOf(event.clipboardData);
    if (!files.length) return;
    event.preventDefault();
    control.attach(files);
  };
}

// AttachButton opens the file picker for images.
export function AttachButton(props: { control: SessionControl }) {
  let picker!: HTMLInputElement;
  return (
    <>
      <button
        type="button"
        class="icon paperclip"
        aria-label="Attach Image"
        title="Attach Image"
        onClick={() => picker.click()}
      />
      <input
        ref={picker}
        type="file"
        accept={imageTypes.join(",")}
        multiple
        hidden
        onChange={(event) => {
          const files = [...(event.currentTarget.files ?? [])];
          event.currentTarget.value = "";
          props.control.attach(files);
        }}
      />
    </>
  );
}

// Thumbnails shows the composer's attached images, each with its remove
// button.
export function Thumbnails(props: { control: SessionControl }) {
  return (
    <Show when={props.control.images().length}>
      <div class="thumbnails">
        <For each={props.control.images()}>
          {(file) => <Thumbnail file={file} onRemove={() => props.control.removeImage(file)} />}
        </For>
      </div>
    </Show>
  );
}

function Thumbnail(props: { file: File; onRemove(): void }) {
  const file = untrack(() => props.file);
  const url = URL.createObjectURL(file);
  onCleanup(() => URL.revokeObjectURL(url));
  return (
    <div class="thumbnail">
      <img src={url} alt={file.name} />
      <button type="button" class="icon x" aria-label="Remove Image" title="Remove Image" onClick={props.onRemove} />
    </div>
  );
}
