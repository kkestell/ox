import { useEffect, useLayoutEffect, useRef, useState } from "react";

import {
  maximumAttachmentBytes,
  maximumPromptText,
  type PromptCapabilities,
  type PromptContentBlock,
  type SessionTranscript,
} from "../protocol.ts";

import { SessionInformation } from "./session-information.tsx";

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
  const message = useRef<HTMLTextAreaElement>(null);
  const acceptsAttachments = capabilities.audio || capabilities.embeddedContext || capabilities.image;

  useEffect(() => {
    if (!busy) return;
    const cancel = (event: KeyboardEvent) => {
      if (
        event.key !== "Escape"
        || document.querySelector('dialog[open], .app[data-navigation="open"]')
      ) return;
      event.preventDefault();
      onCancel();
    };
    window.addEventListener("keydown", cancel);
    return () => window.removeEventListener("keydown", cancel);
  }, [busy, onCancel]);

  useLayoutEffect(() => {
    const element = message.current;
    if (!element) return;
    const maximumHeight = Number.parseFloat(getComputedStyle(element).maxBlockSize);
    element.style.blockSize = "auto";
    element.style.blockSize = `${Math.min(element.scrollHeight, maximumHeight)}px`;
    element.style.overflowY = element.scrollHeight > maximumHeight ? "auto" : "hidden";
  }, [text]);

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
    <section aria-label="Prompt" className="composer">
      <form
        onSubmit={(event) => {
          event.preventDefault();
          void submit();
        }}
      >
        <textarea
          aria-label="Message"
          disabled={busy}
          onChange={(event) => setText(event.target.value)}
          onKeyDown={(event) => {
            if (event.key !== "Enter" || event.shiftKey || event.nativeEvent.isComposing) return;
            event.preventDefault();
            void submit();
          }}
          ref={message}
          rows={1}
          value={text}
        />
        <div className="session-controls">
          {acceptsAttachments ? (
            <label className="icon">
              <PaperclipIcon />
              <input
                aria-label="Add attachment"
                multiple
                onChange={(event) => setAttachments([...attachments, ...Array.from(event.target.files ?? [])])}
                ref={files}
                type="file"
              />
            </label>
          ) : null}
          <SessionInformation onConfigOption={onConfigOption} transcript={transcript} />
        </div>
        {attachments.length > 0 ? (
          <ul aria-label="Attachments" className="attachments">
            {attachments.map((file, index) => (
              <li key={`${file.name}-${index}`}>
                <span className="truncate">{file.name}</span>
                <button
                  aria-label={`Remove ${file.name}`}
                  className="danger icon"
                  onClick={() => removeAttachment(index)}
                  title={`Remove ${file.name}`}
                  type="button"
                >
                  <CloseIcon />
                </button>
              </li>
            ))}
          </ul>
        ) : null}
      </form>
    </section>
  );
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

function PaperclipIcon() {
  return (
    <svg aria-hidden="true" fill="none" stroke="currentColor" strokeLinecap="round" strokeWidth="2" viewBox="0 0 24 24">
      <path d="m21.44 11.05-9.19 9.19a6 6 0 0 1-8.49-8.49l8.57-8.57A4 4 0 1 1 18 8.84l-8.59 8.57a2 2 0 0 1-2.83-2.83l8.49-8.48" />
    </svg>
  );
}

function CloseIcon() {
  return (
    <svg aria-hidden="true" fill="none" stroke="currentColor" strokeLinecap="round" strokeWidth="2" viewBox="0 0 24 24">
      <path d="M18 6 6 18M6 6l12 12" />
    </svg>
  );
}
