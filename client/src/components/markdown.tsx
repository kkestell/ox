import ReactMarkdown, { type Components } from "react-markdown";
import remarkBreaks from "remark-breaks";
import remarkGfm from "remark-gfm";

const components: Components = {
  a: ({ children, href }) => href ? (
    <a className="font-medium underline underline-offset-4" href={href}>{children}</a>
  ) : (
    <span>{children}</span>
  ),
  img: ({ alt, src }) => <span>{alt === "" || alt === undefined ? src : alt}</span>,
  pre: ({ children }) => (
    <pre className="max-h-72 min-w-0 max-w-full overflow-auto rounded-lg border border-current/15 bg-current/5 p-3 font-mono text-xs leading-5">
      {children}
    </pre>
  ),
  table: ({ children }) => (
    <div className="max-w-full overflow-x-auto">
      <table className="w-full border-collapse text-left">{children}</table>
    </div>
  ),
};

// Model output is untrusted. ReactMarkdown leaves raw HTML as text, and this
// transform keeps its links within the same HTTP(S) policy as ACP resources.
function safeURL(url: string): string | undefined {
  try {
    const parsed = new URL(url);
    return parsed.protocol === "http:" || parsed.protocol === "https:" ? parsed.href : undefined;
  } catch {
    return undefined;
  }
}

export function Markdown({ text }: { text: string }) {
  return (
    <div className="min-w-0 space-y-3 break-words [overflow-wrap:anywhere] [&_blockquote]:border-l-2 [&_blockquote]:border-current/20 [&_blockquote]:pl-4 [&_blockquote]:text-current/70 [&_code]:rounded [&_code]:bg-current/10 [&_code]:px-1 [&_code]:font-mono [&_h1]:text-2xl [&_h1]:font-semibold [&_h2]:text-xl [&_h2]:font-semibold [&_h3]:text-lg [&_h3]:font-semibold [&_li]:ml-5 [&_ol]:list-decimal [&_ul]:list-disc [&_td]:border [&_td]:border-current/20 [&_td]:px-2 [&_td]:py-1 [&_th]:border [&_th]:border-current/20 [&_th]:px-2 [&_th]:py-1 [&_th]:font-semibold">
      <ReactMarkdown components={components} remarkPlugins={[remarkGfm, remarkBreaks]} urlTransform={safeURL}>
        {text}
      </ReactMarkdown>
    </div>
  );
}
