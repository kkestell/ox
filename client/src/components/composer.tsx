import { useRef, useState } from "react";

import {
  maximumAttachmentBytes,
  maximumPromptText,
  type PromptCapabilities,
  type PromptContentBlock,
} from "../protocol.ts";

import { Disclosure } from "./disclosure.tsx";

// The composer holds the draft for one session. Mounting it under the session
// key keeps a draft from following the selection to another session.
export function Composer({
  busy,
  capabilities,
  onCancel,
  onError,
  onSubmit,
}: {
  busy: boolean;
  capabilities: PromptCapabilities;
  onCancel: () => void;
  onError: (message: string) => void;
  onSubmit: (prompt: PromptContentBlock[]) => void;
}) {
  const [text, setText] = useState("");
  const [attachments, setAttachments] = useState<File[]>([]);
  const [resourceLinkName, setResourceLinkName] = useState("");
  const [resourceLinkURI, setResourceLinkURI] = useState("");
  const files = useRef<HTMLInputElement>(null);
  const acceptsAttachments = capabilities.audio || capabilities.embeddedContext || capabilities.image;

  async function submit(): Promise<void> {
    let prompt: PromptContentBlock[];
    try {
      prompt = await promptBlocks(text, attachments, resourceLinkName, resourceLinkURI, capabilities);
    } catch (error) {
      onError(error instanceof Error ? error.message : "Could not read attachment");
      return;
    }
    if (prompt.length === 0) {
      onError("Enter a prompt, choose an attachment, or add a resource link");
      return;
    }
    onSubmit(prompt);
    setText("");
    setAttachments([]);
    setResourceLinkName("");
    setResourceLinkURI("");
    if (files.current) {
      files.current.value = "";
    }
  }

  return (
    <section aria-labelledby="composer-heading">
      <h2 id="composer-heading">Prompt</h2>
      <form
        onSubmit={(event) => {
          event.preventDefault();
          void submit();
        }}
      >
        <label>
          Message
          <textarea disabled={busy} onChange={(event) => setText(event.target.value)} value={text} />
        </label>
        <Disclosure label="Add context">
          {acceptsAttachments ? (
            <label>
              Attachments
              <input disabled={busy} multiple onChange={(event) => setAttachments(Array.from(event.target.files ?? []))} ref={files} type="file" />
            </label>
          ) : null}
          {attachments.length > 0 ? <p>{attachments.map((file) => file.name).join(", ")}</p> : null}
          <fieldset disabled={busy}>
            <legend>Resource link</legend>
            <label>Name<input onChange={(event) => setResourceLinkName(event.target.value)} value={resourceLinkName} /></label>
            <label>URI<input onChange={(event) => setResourceLinkURI(event.target.value)} type="url" value={resourceLinkURI} /></label>
          </fieldset>
        </Disclosure>
        <button disabled={busy} type="submit">
          Send prompt
        </button>
        {busy ? (
          <button onClick={onCancel} type="button">
            Cancel prompt
          </button>
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
  resourceLinkName: string,
  resourceLinkURI: string,
  capabilities: PromptCapabilities,
): Promise<PromptContentBlock[]> {
  const prompt: PromptContentBlock[] = text ? [{ text, type: "text" }] : [];
  if (resourceLinkName || resourceLinkURI) {
    if (!resourceLinkName || !resourceLinkURI) {
      throw new Error("A resource link needs both a name and URI");
    }
    prompt.push({ name: resourceLinkName, type: "resource_link", uri: resourceLinkURI });
  }
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
