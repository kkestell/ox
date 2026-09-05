# Web Access

Ox can fetch a public HTTP or HTTPS page with `web_fetch`. Every fetch requires
permission unless the session already has a matching grant. The tool accepts
HTML, UTF-8 plain text, and JSON. It does not run JavaScript, read browser
cookies, or send OpenRouter credentials.

Fetches stop after 30 seconds, follow at most five redirects, and reject private
or special-use network addresses at every connection. The decoded response body
cannot exceed 2 MiB. Results identify both the requested and final URL. Content
over 64 KiB is stored in the session spill directory and can be read with
`read_file`.

## Search

Web search is an MCP tool supplied by the ACP client. Configure a search MCP
server in the client so it includes that server in the `mcpServers` field when
creating or loading an Ox session. The server's discovered tools then appear in
the session with Ox's usual `mcp__<server>__<tool>` names and permission checks.

Ox has no search provider, search API key, or search setting of its own. Without
a client-supplied search server, `web_fetch` can still open a URL you already
have, but search is unavailable.
