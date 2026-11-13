import { openUrl } from "@tauri-apps/plugin-opener";
import type { ComponentProps } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { showError } from "./showError";

/** Transcript prose, rendered from the complete text accumulated so far. */
export function Markdown({ text }: { text: string }) {
  return (
    <div className="text markdown">
      <ReactMarkdown remarkPlugins={[remarkGfm]} components={{ a: Link, table: Table }}>
        {text}
      </ReactMarkdown>
    </div>
  );
}

/** Only web and email links have a destination outside the transcript. */
function Link({ href, title, children }: ComponentProps<"a">) {
  if (!href || !/^(https?:\/\/|mailto:)/i.test(href)) return <>{children}</>;
  return (
    <a
      href={href}
      title={title}
      onClick={(event) => {
        event.preventDefault();
        openUrl(event.currentTarget.href).catch(showError);
      }}
    >
      {children}
    </a>
  );
}

/** Wide tables scroll locally instead of widening the conversation. */
function Table({ children }: ComponentProps<"table">) {
  return (
    <div className="markdown-table">
      <table>{children}</table>
    </div>
  );
}
