import { CircleIcon, CircleAlertIcon, CircleCheckIcon, LoaderCircleIcon } from "lucide-react";

import {
  type SessionTranscript,
  type ToolTranscriptContent,
  type TranscriptContent,
} from "../protocol.ts";

import { Disclosure } from "./disclosure.tsx";
import { toolLabel } from "./tool-label.ts";

import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";

export function Transcript({ transcript }: { transcript: SessionTranscript }) {
  return (
    <section aria-label="Transcript" className="flex min-w-0 flex-col gap-5">
      <ol aria-label="Session transcript" className="flex min-w-0 flex-col">
        {transcript.entries.map((entry, index) => (
          <li
            className={`${compactWithPrevious(transcript.entries, index) ? "mt-1.5" : "[&:not(:first-child)]:mt-5"} min-w-0`}
            data-transcript-kind={entry.kind}
            key={entry.id}
          >
            {entry.kind === "tool" ? (
              <article aria-label={`Tool ${toolName(entry)}`}>
                <Disclosure
                  className="min-w-0"
                  contentClassName="min-w-0"
                  icon={entry.status === "completed" ? undefined : <ToolIcon status={entry.status} />}
                  label={
                    <span className="min-w-0 flex-1 text-left">
                      <span className="break-words font-medium text-foreground [overflow-wrap:anywhere]">
                        {toolName(entry)}
                      </span>
                      <span className="sr-only">{` ${toolStatusLabel(entry.status)}`}</span>
                    </span>
                  }
                >
                  {entry.locations.length > 0 ? (
                    <div className="min-w-0">
                      <p className="text-xs font-medium text-muted-foreground">Locations</p>
                      <ul aria-label="Tool locations" className="mt-1 min-w-0 space-y-1 font-mono text-xs">
                        {entry.locations.map((location) => (
                          <li className="break-words [overflow-wrap:anywhere]" key={`${location.path}:${location.line ?? ""}`}>
                            {location.path}
                            {location.line === undefined ? "" : `:${location.line}`}
                          </li>
                        ))}
                      </ul>
                    </div>
                  ) : null}
                  {entry.content.length > 0 ? (
                    <div className="min-w-0 space-y-2 pt-1">
                      <p className="text-xs font-medium text-muted-foreground">Output</p>
                      {entry.content.map((content, contentIndex) => <ToolOutput content={content} key={contentIndex} />)}
                    </div>
                  ) : null}
                </Disclosure>
              </article>
            ) : entry.kind === "unknown" ? (
              <Disclosure label="Unsupported transcript item"><p>{entry.label}</p></Disclosure>
            ) : entry.kind === "thought" ? (
              <Disclosure className="rounded-lg py-1.5" label="Reasoning">
                {entry.content.map((content, contentIndex) => <Content content={content} key={contentIndex} />)}
              </Disclosure>
            ) : entry.kind === "user" ? (
              <article
                aria-label="user message"
                className="ml-auto w-fit min-w-0 max-w-[36rem] space-y-2 rounded-2xl bg-primary px-4 py-3 text-sm leading-6 text-primary-foreground shadow-sm"
              >
                <h3 className="sr-only">You</h3>
                {entry.content.map((content, contentIndex) => <Content content={content} key={contentIndex} />)}
              </article>
            ) : (
              <article aria-label={`${entry.kind} message`} className="w-full min-w-0 space-y-2 rounded-2xl border bg-card px-4 py-3 text-sm leading-6 shadow-xs">
                <h3 className="mb-1 text-xs font-semibold text-muted-foreground">Ox</h3>
                {entry.content.map((content, contentIndex) => <Content content={content} key={contentIndex} />)}
              </article>
            )}
          </li>
        ))}
      </ol>
      {transcript.plan.length > 0 ? (
        <Card asChild className="gap-3 border-dashed bg-muted/20 py-4 shadow-none">
          <article aria-label="Current plan">
            <CardHeader className="flex-row items-center gap-2 px-4">
              <CardTitle asChild className="flex-1">
                <h3 className="text-sm">Plan</h3>
              </CardTitle>
              <span className="text-xs text-muted-foreground">
                {`${transcript.plan.filter((entry) => entry.status === "completed").length} of ${transcript.plan.length} complete`}
              </span>
            </CardHeader>
            <CardContent className="px-4">
              <ol className="space-y-1 text-sm text-muted-foreground">
                {transcript.plan.map((entry, index) => (
                  <li className="flex gap-2" key={index}>
                    {entry.status === "completed" ? (
                      <CircleCheckIcon aria-hidden className="mt-0.5 size-4 shrink-0" />
                    ) : (
                      <CircleIcon aria-hidden className="mt-0.5 size-4 shrink-0" />
                    )}
                    <span
                      className={`min-w-0 break-words [overflow-wrap:anywhere] ${entry.status === "completed" ? "text-muted-foreground line-through" : "text-foreground"}`}
                    >
                      {entry.content}
                    </span>
                  </li>
                ))}
              </ol>
            </CardContent>
          </article>
        </Card>
      ) : null}
    </section>
  );
}

// Tool activity is a continuous work log, so consecutive calls sit closer than
// the boundary between a message and the work it prompted.
function compactWithPrevious(entries: SessionTranscript["entries"], index: number): boolean {
  const previous = entries[index - 1];
  return previous !== undefined && previous.kind === "tool" && entries[index]?.kind === "tool";
}

function toolName(entry: Extract<SessionTranscript["entries"][number], { kind: "tool" }>): string {
  return toolLabel(entry);
}

function ToolIcon({ status }: { status?: string }) {
  if (status === "completed") return null;
  return status === "failed"
    ? <CircleAlertIcon aria-hidden className="mt-0.5 size-4 shrink-0 text-destructive" />
    : <LoaderCircleIcon aria-hidden className="mt-0.5 size-4 shrink-0 animate-spin text-muted-foreground" />;
}

function toolStatusLabel(status?: string): string {
  return status === undefined ? "In progress" : status.replaceAll("_", " ");
}

function ToolOutput({ content }: { content: ToolTranscriptContent }) {
  switch (content.type) {
    case "content":
      return <Content content={content.content} />;
    case "diff":
      return <Pre>{`${content.path}\n${content.oldText ?? ""}\n${content.newText}`}</Pre>;
    case "terminal":
      return <p className="break-words [overflow-wrap:anywhere]">{`Terminal ${content.terminalId}`}</p>;
    case "unknown":
      return <p className="break-words [overflow-wrap:anywhere]">{content.label}</p>;
  }
}

function Content({ content }: { content: TranscriptContent }) {
  switch (content.type) {
    case "text":
      return <p className="break-words whitespace-pre-wrap [overflow-wrap:anywhere]">{content.text}</p>;
    case "image":
      return (
        <img
          alt={content.uri ?? "Image content"}
          className="max-w-full rounded-lg border"
          src={`data:${content.mimeType};base64,${content.data}`}
        />
      );
    case "audio":
      return <audio className="w-full" controls src={`data:${content.mimeType};base64,${content.data}`} />;
    case "resource_link": {
      const href = safeResourceLink(content.uri);
      const label = content.title ?? content.name;
      return (
        <p className="break-words [overflow-wrap:anywhere]">
          {href ? (
            <a className="font-medium underline underline-offset-4" href={href}>{label}</a>
          ) : (
            <span>{label}</span>
          )}
          {content.description ? `: ${content.description}` : ""}
        </p>
      );
    }
    case "resource":
      return content.text === undefined ? (
        <a
          className="break-words font-medium underline underline-offset-4 [overflow-wrap:anywhere]"
          download
          href={`data:${content.mimeType ?? "application/octet-stream"};base64,${content.blob}`}
        >
          {content.uri}
        </a>
      ) : (
        <Pre>{content.text}</Pre>
      );
    case "unknown":
      return <p className="break-words [overflow-wrap:anywhere]">{content.label}</p>;
  }
}

function Pre({ children }: { children: string }) {
  return (
    <pre className="max-h-72 min-w-0 max-w-full overflow-y-auto overflow-x-hidden break-words whitespace-pre-wrap rounded-lg border bg-muted/60 p-3 font-mono text-xs leading-5 [overflow-wrap:anywhere]">
      {children}
    </pre>
  );
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
