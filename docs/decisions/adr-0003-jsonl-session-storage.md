# ADR-0003: JSONL for Session Storage

- **Status:** accepted
- **Date:** 2026-03-28

## Context and Problem Statement

Ur needs to persist conversation sessions (user messages, assistant responses, tool calls, tool results) so they can be resumed later. Sessions are scoped to a workspace and are the primary unit of state in Ur. The storage format must be simple, reliable, and require no external dependencies.

Relevant docs: [Ur Architecture](../index.md)

## Decision Drivers

- Simplicity — no database dependencies, no server processes, no schema migrations
- Reliability — sessions must not be corrupted by crashes or power loss
- Human-readability — developers should be able to inspect session files directly
- AoT compatibility — no ORM or database driver reflection concerns
- Performance — sessions grow linearly with conversation length; reads and writes must stay fast

## Considered Options

### Option 1: SQLite

**Description:** Sessions stored in a SQLite database per workspace.

**Pros:**

- Structured queries (search across sessions, filter by date, etc.)
- Transactions and crash recovery built in
- Well-established .NET libraries (Microsoft.Data.Sqlite)

**Cons:**

- Database file is binary — not human-readable
- Adds a native dependency (SQLite native library), complicating AoT
- Schema migrations needed as the session format evolves
- Overkill for append-only sequential data

**When this is the right choice:** When sessions need to be queried, searched, or cross-referenced.

### Option 2: JSONL (JSON Lines)

**Description:** Each session is a `.jsonl` file — one JSON object per line, appended sequentially.

**Pros:**

- Append-only — writes are a single file append, crash-safe with `fsync`
- Human-readable — `cat`, `grep`, `jq` all work
- No dependencies — just file I/O and JSON serialization
- Trivially parseable — read line by line, parse each as JSON
- AoT compatible — `System.Text.Json` source generators work with AoT

**Cons:**

- No indexing — resuming a session requires reading the whole file
- No cross-session queries without reading all session files
- No schema enforcement beyond what the serializer provides
- Files grow without bound (until compaction)

**When this is the right choice:** When data is sequential, append-only, and primarily read in full (like a conversation log).

### Option 3: Single JSON file per session

**Description:** Each session is a JSON file containing an array of messages.

**Pros:**

- Human-readable
- Standard JSON — any tool can parse it

**Cons:**

- Every write must rewrite the entire file (can't append to a JSON array without reading/rewriting)
- Crash during rewrite can corrupt the file
- Scales poorly with session length

**When this is the right choice:** When sessions are short and crash safety is not a concern.

## Decision

We chose **JSONL** because sessions are inherently sequential and append-only — a conversation is a log. JSONL maps perfectly to this shape: each message is a line, writes are appends, and the format is human-readable with zero dependencies. SQLite is more powerful but the power is not needed (we don't query across sessions), and it adds native dependency complexity. Single-file JSON has write-amplification and crash-safety problems that JSONL avoids.

## Consequences

### Positive

- Session writes are cheap (append a line)
- Sessions are inspectable with standard Unix tools
- No native dependencies, no schema migrations
- `System.Text.Json` source generators provide AoT-compatible serialization

### Negative

- Resuming a long session requires reading the entire file — acceptable for conversations (thousands of lines, not millions)
- Cross-session search (e.g. "find the session where I discussed X") requires reading all session files — may need an index later
- Session compaction (summarizing old messages) must rewrite the file — but this is infrequent

### Neutral

- The JSONL format is the session's wire format and storage format — no translation layer needed
- Session files can be version-controlled (though they will be large)

## Confirmation

- Session resume time stays under 100ms for sessions up to 10,000 messages
- No data loss from crashes during writes — validate with crash-recovery testing
- Developers actually inspect session files with `cat`/`jq` — if nobody does, the human-readability benefit is theoretical
