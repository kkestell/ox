import MarkdownIt from "markdown-it";

// renderer renders Markdown with raw HTML escaped and images disabled, so the
// rendered HTML holds only the tags markdown-it emits and loads nothing. With
// breaks, a single line break shows as one, as it was typed.
function renderer(breaks: boolean) {
  const md = new MarkdownIt({ html: false, linkify: true, breaks }).disable("image");
  // Links open in a new tab so following one leaves the page, and its unsent
  // text, in place.
  md.renderer.rules.link_open = (tokens, i, options, _env, self) => {
    tokens[i].attrSet("target", "_blank");
    tokens[i].attrSet("rel", "noopener noreferrer");
    return self.renderToken(tokens, i, options);
  };
  return md;
}

const chat = renderer(true);
const file = renderer(false);

// Markdown renders transcript text with its line breaks kept, and a file's text
// with each paragraph's lines joined, as Markdown renders a document.
export function Markdown(props: { text: string; file?: boolean; class?: string }) {
  return <div class={["markdown", props.class]} innerHTML={(props.file ? file : chat).render(props.text)} />;
}
