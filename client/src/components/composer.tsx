import { PaperclipIcon, SendIcon, SquareIcon, XIcon } from "lucide-react";
import { useEffect, useRef, useState } from "react";

import {
  maximumAttachmentBytes,
  maximumPromptText,
  type PromptCapabilities,
  type PromptContentBlock,
  type SessionTranscript,
} from "../protocol.ts";

import { SessionInformation } from "./session-information.tsx";

import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";

// The composer holds the draft for one session. Mounting it under the session
// key keeps a draft from following the selection to another session. It also
// owns the row of session controls, because the attachment control sits at the
// start of that row and the chosen files belong below the whole row.
export function Composer({
  busy,
  capabilities,
  onCancel,
  onConfigOption,
  onError,
  onSubmit,
  transcript,
}: {
  busy: boolean;
  capabilities: PromptCapabilities;
  onCancel: () => void;
  onConfigOption: (configId: string, value: string) => void;
  onError: (message: string) => void;
  onSubmit: (prompt: PromptContentBlock[]) => void;
  transcript: SessionTranscript;
}) {
  const [text, setText] = useState("");
  const [attachments, setAttachments] = useState<File[]>([]);
  const files = useRef<HTMLInputElement>(null);
  const acceptsAttachments = capabilities.audio || capabilities.embeddedContext || capabilities.image;

  useEffect(() => {
    if (!busy) return;
    const cancel = (event: KeyboardEvent) => {
      if (event.key !== "Escape" || overlayOpen()) return;
      event.preventDefault();
      onCancel();
    };
    window.addEventListener("keydown", cancel);
    return () => window.removeEventListener("keydown", cancel);
  }, [busy, onCancel]);

  async function submit(): Promise<void> {
    let prompt: PromptContentBlock[];
    try {
      prompt = await promptBlocks(text, attachments, capabilities);
    } catch (error) {
      onError(error instanceof Error ? error.message : "Could not read attachment");
      return;
    }
    if (prompt.length === 0) {
      onError("Enter a prompt or choose an attachment");
      return;
    }
    onSubmit(prompt);
    setText("");
    setAttachments([]);
    clearPicker();
  }

  function removeAttachment(index: number): void {
    setAttachments(attachments.filter((_, position) => position !== index));
    // The picker keeps its last selection, so clearing it lets the same file be
    // chosen again after it was removed.
    clearPicker();
  }

  function clearPicker(): void {
    if (files.current) {
      files.current.value = "";
    }
  }

  return (
    <section aria-label="Prompt">
      <form
        className="overflow-hidden rounded-xl border bg-card shadow-sm focus-within:border-ring focus-within:ring-[3px] focus-within:ring-ring/20"
        onSubmit={(event) => {
          event.preventDefault();
          void submit();
        }}
      >
        <Textarea
          aria-label="Message"
          className="max-h-[calc(8lh+1.5rem)] min-h-11 resize-none rounded-none border-0 bg-transparent px-3 py-3 leading-6 shadow-none focus-visible:ring-0"
          disabled={busy}
          onChange={(event) => setText(event.target.value)}
          onKeyDown={(event) => {
            if (event.key !== "Enter" || event.shiftKey || event.nativeEvent.isComposing) return;
            event.preventDefault();
            void submit();
          }}
          placeholder="Send a message"
          rows={1}
          value={text}
        />
        <div className="flex min-w-0 flex-nowrap items-center gap-1 px-2 py-2 sm:flex-wrap sm:items-end sm:gap-2">
          {acceptsAttachments ? (
            <Button asChild size="icon-sm" title="Add attachment" variant="ghost">
              <label>
                <PaperclipIcon />
                <input
                  aria-label="Add attachment"
                  className="sr-only"
                  multiple
                  onChange={(event) => setAttachments([...attachments, ...Array.from(event.target.files ?? [])])}
                  ref={files}
                  type="file"
                />
              </label>
            </Button>
          ) : null}
          <SessionInformation onConfigOption={onConfigOption} transcript={transcript} />
          {busy ? (
            <Button
              aria-label="Stop response"
              className="ml-auto sm:ml-0 sm:w-auto sm:px-3"
              onClick={onCancel}
              size="icon-sm"
              type="button"
              variant="outline"
            >
              <SquareIcon className="fill-current" />
              <span className="hidden sm:inline">Stop</span>
            </Button>
          ) : (
            <Button aria-label="Send prompt" className="ml-auto sm:ml-0" size="icon-sm" type="submit">
              <SendIcon />
            </Button>
          )}
        </div>
        {attachments.length > 0 ? (
          <ul aria-label="Attachments" className="space-y-1 px-2 py-2">
            {attachments.map((file, index) => (
              <li
                className="flex items-center gap-2 rounded-md border py-0.5 pl-3 text-sm"
                key={`${file.name}-${index}`}
              >
                <span className="min-w-0 flex-1 truncate">{file.name}</span>
                <Button
                  aria-label={`Remove ${file.name}`}
                  className="hover:bg-destructive/10 hover:text-destructive"
                  onClick={() => removeAttachment(index)}
                  size="icon-sm"
                  title={`Remove ${file.name}`}
                  type="button"
                  variant="ghost"
                >
                  <XIcon />
                </Button>
              </li>
            ))}
          </ul>
        ) : null}
      </form>
    </section>
  );
}

// Escape closes whichever layer is on top. A dialog, drawer, menu, popover, or
// open select owns that key before the running turn does.
function overlayOpen(): boolean {
  return document.querySelector(
    '[data-slot="dialog-content"], [data-slot="sheet-content"], [data-slot="dropdown-menu-content"], [data-slot="popover-content"], [data-slot="select-content"]',
  ) !== null;
}

// A browser file becomes the most faithful block the agent advertised support
// for, so an unsupported attachment is refused here rather than at the host.
async function promptBlocks(
  text: string,
  attachments: File[],
  capabilities: PromptCapabilities,
): Promise<PromptContentBlock[]> {
  const prompt: PromptContentBlock[] = text ? [{ text, type: "text" }] : [];
  for (const file of attachments) {
    if (file.size > maximumAttachmentBytes) {
      throw new Error(`${file.name} is too large to attach`);
    }
    const uri = `attachment://local/${encodeURIComponent(file.name)}`;
    const mimeType = file.type ? { mimeType: file.type } : {};
    if (capabilities.image && file.type.startsWith("image/")) {
      prompt.push({ data: await fileData(file), mimeType: file.type, type: "image", uri });
    } else if (capabilities.audio && file.type.startsWith("audio/")) {
      prompt.push({ data: await fileData(file), mimeType: file.type, type: "audio" });
    } else if (!capabilities.embeddedContext) {
      throw new Error(`${file.name} is not supported by Ox`);
    } else if (isEmbeddableText(file)) {
      prompt.push({ resource: { ...mimeType, text: await file.text(), uri }, type: "resource" });
    } else {
      prompt.push({ resource: { blob: await fileData(file), ...mimeType, uri }, type: "resource" });
    }
  }
  return prompt;
}

function isEmbeddableText(file: File): boolean {
  const textual = file.type.startsWith("text/") || /(?:json|javascript|xml|yaml|toml)$/.test(file.type);
  return textual && file.size <= maximumPromptText;
}

async function fileData(file: File): Promise<string> {
  const bytes = new Uint8Array(await file.arrayBuffer());
  let data = "";
  for (let index = 0; index < bytes.length; index += 0x8000) {
    data += String.fromCharCode(...bytes.subarray(index, index + 0x8000));
  }
  return btoa(data);
}
