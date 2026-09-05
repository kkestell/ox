# Public Web Fetch and MCP Search

## Sources

- `docs/spec.md#web-access` and `docs/spec.md#session-configuration` — fetch,
  source handling, search ownership, permission, and plan-mode contract
- `eng/roadmap.md#public-web-fetch-and-mcp-search` — slice scope and gates
- `eng/architecture.md#extension-boundaries` and
  `eng/architecture.md#workspace-boundary` — tool ownership, MCP search
  boundary, untrusted data, permissions, and ordinary spill handling
- `internal/tools/{tools,read}.go`, `internal/workspace/spill.go`,
  `internal/agent/{prompt,tool}.go`, and `internal/e2e/mcp_test.go` — built-in
  registration, strict argument, spill, prompt, permission, and MCP fixture
  patterns
- `~/src/references/repos/personal/eta/internal/agent/tools/{web_fetch,web_fetch_test}.go`
  — HTML conversion, bounded rendering, and tool shape to adapt

## Goal

Add an approval-gated `web_fetch` tool that safely retrieves public HTTP(S) text
and identifies its sources. Complete the slice with a real-process MCP
search-to-fetch fixture and a short guide explaining that search servers come
from the ACP client.

## Implementation

- `internal/tools/web_fetch.go` and `internal/tools/tools.go` — add a strict
  one-URL tool for parent and child turns in code and plan modes. Give ACP
  permission updates a useful URL title, keep the tool parallel-safe, and use
  `workspace.RenderText` plus `ReportSpill` for output above 64 KiB.
- Build an isolated HTTP client with no proxy, cookie jar, authorization, or
  provider headers. Reject non-HTTP(S), relative, or credential-bearing URLs;
  cap redirects at five; and apply the same validation to every redirect.
  Resolve at dial time, reject the complete response when any candidate address
  is loopback, private, link-local, multicast, unspecified, or another IANA
  special-use range, and dial only an address that was validated. Normalize
  IPv4-mapped addresses before policy checks so DNS rebinding cannot separate
  validation from connection.
- Enforce one 30-second call deadline and read at most 2 MiB plus one byte from
  the decoded response stream so compressed growth fails explicitly. Accept
  HTML/XHTML, UTF-8 plain text, and JSON only; reject malformed media types,
  invalid text, unsupported content encodings, and binary responses. Use Eta's
  focused HTML-to-Markdown dependency for deterministic extraction, and
  deterministically indent JSON without changing its values.
- Render requested URL, final URL, media type, and an explicit untrusted-source
  delimiter around extracted content. Ensure errors never include response
  bodies or ambient/configured credentials.
- `internal/agent/prompt.go` — tell both parent and child models that fetched
  and search content is untrusted data, that factual answers based on it link
  the fetched source and distinguish inference, and that search is unavailable
  without a client-supplied MCP search tool. Do not treat snippets as fetched
  evidence.
- `docs/web.md` — document shipped fetch limits and approval behavior, and show
  that an ACP client must supply a search MCP server in `mcpServers`; Ox has no
  search credential or settings field.

## Tests

- Tool tests use injected resolution and dialing to cover strict arguments, URL
  credentials, direct and DNS-resolved special-use addresses, mixed DNS answers,
  rebinding-safe dialing, redirect revalidation and limits, status failures,
  total timeout, cancellation, and absence of cookies, proxy, and authorization
  headers.
- Response fixtures cover HTML extraction, plain text, JSON formatting,
  requested versus redirected source URLs, unsupported binary/media types,
  invalid UTF-8, gzip expansion past 2 MiB, exact-limit success, explicit
  oversize failure, inline output, and readable session spills.
- A fake-provider process test supplies an MCP search tool, has the model
  search, fetch the returned URL after a separate permission request, and answer
  with the final source link. Use an e2e-only resolver/dial seam for the local
  HTTP fixture so the production binary retains the public-address policy. Also
  prove fetch remains present without MCP search and that cancellation leaves an
  unrelated session responsive.

## Decisions

- Keep retrieval and extraction in `internal/tools`; the bounded single-request
  surface does not justify another production package or a search-provider
  abstraction.
- Port Eta's dedicated HTML conversion library instead of maintaining a partial
  HTML renderer in Ox. Search remains entirely in the existing MCP catalog.
