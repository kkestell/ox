import {
  type FormValue,
  type PendingInteraction,
  type SessionTranscript,
  type ToolTranscriptContent,
  type TranscriptContent,
} from "../protocol.ts";

import { Disclosure } from "./disclosure.tsx";
import { ElicitationForm, PermissionInteraction } from "./pending-interactions.tsx";

export function Transcript({ interactions, onElicitation, onPermission, transcript }: {
  interactions: PendingInteraction[];
  onElicitation: (interactionId: string, action: "accept" | "decline" | "cancel", content?: Record<string, FormValue>) => void;
  onPermission: (interactionId: string, optionId: string) => void;
  transcript: SessionTranscript;
}) {
  const inlinePermissions = (entry: Extract<SessionTranscript["entries"][number], { kind: "tool" }>) =>
    interactions.filter((interaction): interaction is Extract<PendingInteraction, { kind: "permission" }> =>
      interaction.kind === "permission" && entry.id === `tool:${interaction.tool.id}`,
    );
  const trailingInteractions = interactions.filter((interaction) =>
    interaction.kind === "form" || !transcript.entries.some((entry) =>
      entry.kind === "tool" && entry.id === `tool:${interaction.tool.id}`,
    ),
  );

  return (
    <section aria-label="Transcript">
      <ol aria-label="Session transcript">
        {transcript.entries.map((entry) => (
          <li key={entry.id}>
            {entry.kind === "tool" ? (
              <article aria-label={`Tool ${entry.title}`}>
                <h3>{entry.title}</h3>
                {entry.status ? <p>{entry.status}</p> : null}
                <Disclosure label="Details">
                  {entry.name ? <p>{entry.name}</p> : null}
                  {entry.toolKind ? <p>{entry.toolKind}</p> : null}
                  {entry.locations.length > 0 ? <ul aria-label="Tool locations">{entry.locations.map((location) => <li key={`${location.path}:${location.line ?? ""}`}>{location.path}{location.line === undefined ? "" : `:${location.line}`}</li>)}</ul> : null}
                  {entry.content.map((content, index) => <ToolOutput content={content} key={index} />)}
                </Disclosure>
                {inlinePermissions(entry).map((interaction) => <PermissionInteraction interaction={interaction} key={interaction.id} onPermission={onPermission} />)}
              </article>
            ) : entry.kind === "unknown" ? (
              <Disclosure label="Unsupported transcript item"><p>{entry.label}</p></Disclosure>
            ) : entry.kind === "thought" ? (
              <Disclosure label="Thought">
                {entry.content.map((content, index) => <Content content={content} key={index} />)}
              </Disclosure>
            ) : (
              <article aria-label={`${entry.kind} message`}>
                <h3>{entry.kind === "agent" ? "Ox" : "You"}</h3>
                {entry.content.map((content, index) => <Content content={content} key={index} />)}
              </article>
            )}
          </li>
        ))}
        {trailingInteractions.map((interaction) => (
          <li key={interaction.id}>
            {interaction.kind === "permission" ? (
              <PermissionInteraction interaction={interaction} onPermission={onPermission} />
            ) : (
              <ElicitationForm interaction={interaction} onSubmit={onElicitation} />
            )}
          </li>
        ))}
      </ol>
      {transcript.plan.length > 0 ? (
        <Disclosure label={`Plan — ${transcript.plan.filter((entry) => entry.status === "completed").length} of ${transcript.plan.length} complete`}>
          <ol>{transcript.plan.map((entry, index) => <li key={index}>{entry.content} ({entry.status})</li>)}</ol>
        </Disclosure>
      ) : null}
      {transcript.usage?.cost ? <Disclosure label="Cost"><p>{`${transcript.usage.cost.amount} ${transcript.usage.cost.currency}`}</p></Disclosure> : null}
    </section>
  );
}

function ToolOutput({ content }: { content: ToolTranscriptContent }) {
  switch (content.type) {
    case "content":
      return <Content content={content.content} />;
    case "diff":
      return <pre>{`${content.path}\n${content.oldText ?? ""}\n${content.newText}`}</pre>;
    case "terminal":
      return <p>{`Terminal ${content.terminalId}`}</p>;
    case "unknown":
      return <p>{content.label}</p>;
  }
}

function Content({ content }: { content: TranscriptContent }) {
  switch (content.type) {
    case "text":
      return <p>{content.text}</p>;
    case "image":
      return <img alt={content.uri ?? "Image content"} src={`data:${content.mimeType};base64,${content.data}`} />;
    case "audio":
      return <audio controls src={`data:${content.mimeType};base64,${content.data}`} />;
    case "resource_link": {
      const href = safeResourceLink(content.uri);
      const label = content.title ?? content.name;
      return (
        <p>
          {href ? <a href={href}>{label}</a> : <span>{label}</span>}
          {content.description ? `: ${content.description}` : ""}
        </p>
      );
    }
    case "resource":
      return content.text === undefined ? (
        <a download href={`data:${content.mimeType ?? "application/octet-stream"};base64,${content.blob}`}>
          {content.uri}
        </a>
      ) : (
        <pre>{content.text}</pre>
      );
    case "unknown":
      return <p>{content.label}</p>;
  }
}

// ACP resource links are agent-provided values, so do not let them select an
// executable browser URL scheme.
function safeResourceLink(uri: string): string | undefined {
  try {
    const url = new URL(uri);
    return url.protocol === "http:" || url.protocol === "https:" ? url.href : undefined;
  } catch {
    return undefined;
  }
}
