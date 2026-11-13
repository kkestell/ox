import { join } from "node:path";
import { agentText, composer, openSession, send, startSession } from "../helpers.ts";

const text = (value: string) => `text ${JSON.stringify(value)}`;
const thought = (value: string) => `thought ${JSON.stringify(value)}`;
const reply = () => $(".conversation > .markdown");

describe("Transcript Markdown", () => {
  beforeEach(async () => {
    await openSession(await startSession());
  });

  it("renders user messages and assistant replies, including after replay", async () => {
    await send("/echo **User** and `code`");
    await expect($(".prompt .markdown strong")).toHaveText("User");
    await expect($(".prompt .markdown code")).toHaveText("code");
    await expect(reply().$("strong")).toHaveText("User");
    await expect(reply().$("code")).toHaveText("code");
    await browser.saveScreenshot(join(process.env.OX_E2E_DIR!, "markdown-messages.png"));
    await browser.refresh();
    await expect($(".prompt .markdown strong")).toHaveText("User");
    await expect(reply().$("strong")).toHaveText("User");
  });

  it("renders Markdown blocks and GitHub extensions with literal code whitespace", async () => {
    await send(
      text(
        "# Heading\n\nFirst *emphasis* and ~~removed~~.\nsoft break\n\nhard  \nbreak\n\n" +
          "> A quote\n\n1. First\n2. Second\n\n- [ ] Todo\n- [x] Done\n\n" +
          "```rust\nfn main() {\n    **literal**();\n}\n```\n\n" +
          "| Name | Value |\n| --- | --- |\n| one | two |\n\nhttps://example.com",
      ),
    );
    await expect(reply().$("h1")).toHaveText("Heading");
    await expect(reply().$("em")).toHaveText("emphasis");
    await expect(reply().$("del")).toHaveText("removed");
    await expect(reply().$("blockquote")).toHaveText("A quote");
    await expect(reply().$$("ol li")).toBeElementsArrayOfSize(2);
    await expect(reply().$$('input[type="checkbox"]')).toBeElementsArrayOfSize(2);
    await expect(reply().$('input[type="checkbox"]:checked')).toBeDisabled();
    await expect(reply().$("pre code")).toHaveText("fn main() {\n    **literal**();\n}\n", {
      trim: false,
    });
    await expect(reply().$("td")).toHaveText("one");
    await expect(reply().$("a")).toHaveAttribute("href", "https://example.com");
    // CommonMark's soft break is ordinary whitespace; a hard break adds br.
    await expect(reply().$$("br")).toBeElementsArrayOfSize(1);
    await browser.saveScreenshot(join(process.env.OX_E2E_DIR!, "markdown-blocks.png"));
  });

  it("completes Markdown split across streamed message and thought chunks", async () => {
    await send(
      [text("**"), thought("**Reason"), "permission", text(" done**"), thought("ing**")].join("\n"),
    );
    const thinking = $(".conversation .thought");
    await expect(reply()).toHaveText("**");
    await expect(thinking).not.toHaveAttribute("open");
    await thinking.$("summary").click();
    await expect(thinking.$(".markdown")).toHaveText("**Reason");
    await $(".composer .request-panel").$("button=Allow").click();
    await expect(reply().$("strong")).toHaveText("allowed done");
    await expect(thinking.$("strong")).toHaveText("Reasoning");
    await expect(composer()).toBeEnabled();
    await browser.saveScreenshot(join(process.env.OX_E2E_DIR!, "markdown-thinking.png"));
    await browser.refresh();
    await expect(thinking).not.toHaveAttribute("open");
    await thinking.$("summary").click();
    await expect(thinking.$("strong")).toHaveText("Reasoning");
    await expect(reply().$("strong")).toHaveText("allowed done");
  });

  it("escapes HTML and keeps unsupported or unsafe links inert", async () => {
    await send(
      text(
        "<script>window.markdownExecuted = true</script>\n\n" +
          "[unsafe](javascript:alert%281%29) [file](file:///tmp/a) [relative](src/main.rs) " +
          "[web](https://example.com) [email](mailto:a@example.com)",
      ),
    );
    await expect(reply().$("script")).not.toBeExisting();
    await expect(reply()).toHaveText(expect.stringContaining("<script>"));
    await expect(reply().$$("a")).toBeElementsArrayOfSize(2);
    await expect(reply().$("a=web")).toHaveAttribute("href", "https://example.com");
    await expect(reply().$("a=email")).toHaveAttribute("href", "mailto:a@example.com");
  });

  it("opens web links through the native opener without navigating the transcript", async () => {
    await send(
      text(
        "[Documentation](https://example.com/docs) " +
          "[Mixed case](HtTpS://EXAMPLE.COM/mixed-case)",
      ),
    );
    await expect(reply().$("a")).toHaveText("Documentation");
    await expect(reply().$("a=Mixed case")).toHaveText("Mixed case");
    // Tauri's invoke function is read-only in a live app. Intercept only the
    // opener's IPC fetch, so this click does not launch a real browser.
    const calls = await browser.execute(() => {
      const original = window.fetch;
      const calls: unknown[] = [];
      window.fetch = (input, options) => {
        if (!decodeURIComponent(String(input)).includes("plugin:opener|open_url")) {
          return original.call(window, input, options);
        }
        calls.push(JSON.parse(String(options?.body)));
        return Promise.resolve(
          new Response("null", {
            headers: { "Content-Type": "application/json", "Tauri-Response": "ok" },
          }),
        );
      };
      try {
        const links = document.querySelectorAll<HTMLAnchorElement>(".conversation > .markdown a");
        for (const link of links) {
          link.click();
        }
        return calls;
      } finally {
        window.fetch = original;
      }
    });
    expect(calls).toEqual([
      { url: "https://example.com/docs" },
      { url: "https://example.com/mixed-case" },
    ]);
    await expect(reply().$("a")).toHaveText("Documentation");
    await expect(composer()).toBeEnabled();
  });

  it("keeps wide code and tables inside their messages and tool data literal", async () => {
    const columns = Array.from({ length: 60 }, (_, index) => `Column ${index}`);
    const table = [columns.join(" | "), columns.map(() => "---").join(" | "), columns.join(" | ")]
      .map((row) => `| ${row} |`)
      .join("\n");
    await send(
      [
        text(`\`\`\`\n${"long_code_".repeat(100)}\n\`\`\`\n\n${table}`),
        thought("**Thinking** and `code`"),
        "tool **literal** `code`",
      ].join("\n"),
    );
    await expect(reply().$("pre")).toBeExisting();
    const layout = await browser.execute(() => {
      const conversation = document.querySelector<HTMLElement>(".conversation")!;
      const code = conversation.querySelector<HTMLElement>(".markdown pre")!;
      const table = conversation.querySelector<HTMLElement>(".markdown-table")!;
      const header = table.querySelector<HTMLElement>("th")!;
      const style = getComputedStyle(header);
      const headerTextHeight =
        header.clientHeight -
        Number.parseFloat(style.paddingTop) -
        Number.parseFloat(style.paddingBottom);
      return {
        conversationFits: conversation.scrollWidth <= conversation.clientWidth,
        codeScrolls: code.scrollWidth > code.clientWidth,
        tableScrolls: table.scrollWidth > table.clientWidth,
        tableHeightFits: table.scrollHeight <= table.clientHeight,
        tableWordsFit: headerTextHeight <= Number.parseFloat(style.lineHeight) * 2,
      };
    });
    expect(layout).toEqual({
      conversationFits: true,
      codeScrolls: true,
      tableScrolls: true,
      tableHeightFits: true,
      tableWordsFit: true,
    });
    const tool = $(".conversation .tool-call");
    await tool.$("summary").click();
    await expect(tool.$("section:last-child pre")).toHaveText("**literal** `code`");
    await expect(tool.$("section:last-child strong")).not.toBeExisting();
    await $(".thought summary").click();
    await expect($(".thought strong")).toHaveText("Thinking");
    await browser.saveScreenshot(join(process.env.OX_E2E_DIR!, "markdown-overflow.png"));
    await expect(agentText()).toBeElementsArrayOfSize(1);
  });
});
