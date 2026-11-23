import MarkdownIt from "markdown-it";

// The text is model output: raw HTML is escaped and images are disabled, so
// the rendered HTML holds only the tags markdown-it emits and loads nothing.
const md = new MarkdownIt({ html: false, linkify: true }).disable("image");

// Links open in a new tab so following one leaves the page, and its unsent
// text, in place.
md.renderer.rules.link_open = (tokens, i, options, _env, self) => {
  tokens[i].attrSet("target", "_blank");
  tokens[i].attrSet("rel", "noopener noreferrer");
  return self.renderToken(tokens, i, options);
};

export function Markdown(props: { text: string; class?: string }) {
  return <div class={["markdown", props.class]} innerHTML={md.render(props.text)} />;
}
