import {
  type SessionTranscript,
  type ToolTranscriptContent,
  type TranscriptContent,
} from "../protocol.ts";

import { Disclosure } from "./disclosure.tsx";

export function Transcript({ transcript }: { transcript: SessionTranscript }) {
  return (
    <section aria-label="Transcript" className="transcript">
      <ol aria-label="Session transcript" className="transcript-entries">
        {transcript.entries.map((entry) => (
          <li className={`transcript-entry transcript-${entry.kind}`} data-transcript-kind={entry.kind} key={entry.id}>
            {entry.kind === "tool" ? (
              <article aria-label={`Tool ${toolName(entry)}`} className="transcript-tool">
                <Disclosure className="tool-disclosure" label={<><code>{toolName(entry)}</code><ToolStatus status={entry.status} /></>}>
                  {entry.name ? <p>{entry.name}</p> : null}
                  {entry.toolKind ? <p>{entry.toolKind}</p> : null}
                  {entry.locations.length > 0 ? <ul aria-label="Tool locations">{entry.locations.map((location) => <li key={`${location.path}:${location.line ?? ""}`}>{location.path}{location.line === undefined ? "" : `:${location.line}`}</li>)}</ul> : null}
                  {entry.content.map((content, index) => <ToolOutput content={content} key={index} />)}
                </Disclosure>
              </article>
            ) : entry.kind === "unknown" ? (
              <Disclosure className="unknown-disclosure" label="Unsupported transcript item"><p>{entry.label}</p></Disclosure>
            ) : entry.kind === "thought" ? (
              <Disclosure className="thought-disclosure" label="Thought">
                {entry.content.map((content, index) => <Content content={content} key={index} />)}
              </Disclosure>
            ) : (
              <article aria-label={`${entry.kind} message`} className="transcript-message">
                <h3>{entry.kind === "agent" ? "Ox" : "You"}</h3>
                {entry.content.map((content, index) => <Content content={content} key={index} />)}
              </article>
            )}
          </li>
        ))}
      </ol>
      {transcript.plan.length > 0 ? (
        <article aria-label="Current plan" className="plan-card">
          <header><h3>Plan</h3><span>{`${transcript.plan.filter((entry) => entry.status === "completed").length} of ${transcript.plan.length} complete`}</span></header>
          <ol>{transcript.plan.map((entry, index) => <li className={entry.status === "completed" ? "completed" : undefined} key={index}>{entry.content}</li>)}</ol>
        </article>
      ) : null}
      {transcript.usage?.cost ? <Disclosure className="usage-disclosure" label="Cost"><p>{`${transcript.usage.cost.amount} ${transcript.usage.cost.currency}`}</p></Disclosure> : null}
    </section>
  );
}

function toolName(entry: Extract<SessionTranscript["entries"][number], { kind: "tool" }>): string {
  return entry.name ?? entry.title;
}

function ToolStatus({ status }: { status?: string }) {
  const label = status === undefined ? "In progress" : status.replaceAll("_", " ");
  const symbol = status === "completed" ? "✓" : status === "failed" ? "!" : "●";
  return <span aria-label={label} className={`tool-status ${status === "in_progress" || status === undefined ? "progress" : ""}`} title={label}>{symbol}</span>;
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
