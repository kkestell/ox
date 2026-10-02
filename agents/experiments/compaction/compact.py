#!/usr/bin/env python3
# Sends saved Ox sessions to a model with a compaction prompt and writes one
# result file per session: the request's usage, the prompt, the summary, and
# the session text the model saw.
#   agents/experiments/compaction/compact.py
#   agents/experiments/compaction/compact.py 0792a899 245e79da --label shorter
# The key comes from OPENROUTER_API_KEY, else from OPENROUTER_API_KEY= in .env.

import argparse
import concurrent.futures
import json
import os
import re
import sqlite3
import time
import urllib.error
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
HERE = Path(__file__).resolve().parent
ENDPOINT = "https://openrouter.ai/api/v1/chat/completions"

PROMPT = """\
The text above is an earlier part of a session between a user and Ox, a coding
agent working in the user's workspace. That earlier part will be replaced by
your summary. A model that sees only your summary and the messages that follow
it must be able to continue the work without asking the user to repeat
anything.

Write the summary. Include:

- The user's requests and instructions, quoted where the exact words matter.
- What has been done: files read or changed, commands run, and their results.
- Decisions made and the reasons for them.
- The current state of the work and what remains to be done.
- Facts the work depends on: file paths, names, errors, and values.

Leave out anything the rest of the work does not need. Write plain text, not a
reply to the user."""


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("sessions", nargs="*", metavar="SESSION_ID_PREFIX")
    parser.add_argument("--count", type=int, default=4, help="largest sessions to use when none are named")
    parser.add_argument("--model", default="deepseek/deepseek-v4.1-flash")
    parser.add_argument("--provider", default="deepseek", help="the only OpenRouter provider allowed")
    parser.add_argument("--label", default="baseline")
    args = parser.parse_args()
    if not re.fullmatch(r"[A-Za-z0-9._-]+", args.label):
        parser.error("--label may contain only letters, digits, '.', '_', and '-'")

    database = sqlite3.connect(f"file:{data_dir() / 'ox.db'}?mode=ro", uri=True)
    database.row_factory = sqlite3.Row
    if args.sessions:
        ids = [session_id(database, parser, prefix) for prefix in args.sessions]
    else:
        ids = [row["id"] for row in database.execute("SELECT id FROM sessions")]
    sessions = [(row(database, id), render(database, id)) for id in ids]
    if not args.sessions:
        sessions = sorted(sessions, key=lambda session: len(session[1]), reverse=True)[: args.count]

    output = HERE / "results" / args.label
    output.mkdir(parents=True, exist_ok=True)
    key = api_key()

    def run(session):
        row, text = session
        path = output / f"{row['id']}.txt"
        path.write_text(result(row, args.model, text, compact(key, args.model, args.provider, text)))
        return path

    with concurrent.futures.ThreadPoolExecutor() as pool:
        for path in pool.map(run, sessions):
            print(path.relative_to(ROOT))


def data_dir():
    if os.environ.get("OX_DATA_DIR"):
        return Path(os.environ["OX_DATA_DIR"])
    if os.environ.get("XDG_DATA_HOME"):
        return Path(os.environ["XDG_DATA_HOME"]) / "ox"
    return Path.home() / ".local/share/ox"


def session_id(database, parser, prefix):
    ids = [row["id"] for row in database.execute("SELECT id FROM sessions WHERE id LIKE ? || '%'", (prefix,))]
    if len(ids) != 1:
        parser.error(f"{len(ids)} sessions match {prefix!r}")
    return ids[0]


def row(database, id):
    return dict(database.execute("SELECT * FROM sessions WHERE id = ?", (id,)).fetchone())


def api_key():
    if os.environ.get("OPENROUTER_API_KEY"):
        return os.environ["OPENROUTER_API_KEY"]
    return next(
        line.split("=", 1)[1]
        for line in (ROOT / ".env").read_text().splitlines()
        if line.startswith("OPENROUTER_API_KEY=")
    )


def render(database, id):
    """The session as the text a model sees: user messages, skill invocations,
    assistant reasoning and text, tool calls, and tool results. Like Ox, it
    leaves out failed turns and provider continuation metadata. It also leaves
    out compaction checkpoints from sessions saved before compaction was
    removed, because the full history they summarized is still saved."""
    blocks = []
    for entry in database.execute(
        "SELECT kind, data FROM transcript_entries WHERE session_id = ? ORDER BY id", (id,)
    ):
        data = json.loads(entry["data"])
        if entry["kind"] == "turn_start":
            blocks.append(turn_start(data["input"]))
        elif entry["kind"] == "assistant_batch":
            message = data["message"]
            if message.get("reasoning"):
                blocks.append(f"[assistant reasoning]\n{message['reasoning']}")
            if message.get("text"):
                blocks.append(f"[assistant]\n{message['text']}")
            for call in message.get("tool_calls", []):
                blocks.append(f"[tool call {call['call_id']}] {call['name']}\n{call['arguments']}")
            for call, outcome in zip(message.get("tool_calls", []), data["outcomes"]):
                blocks.append(f"[tool result {call['call_id']}]\n{outcome['text']}")
        elif entry["kind"] == "subagent_messages":
            for message in data:
                blocks.append(f"[subagent {message['subagent_id']}]\n{message['content'].get('text', '')}")
    return "\n\n".join(blocks)


def turn_start(input):
    content = input["content"]
    if input["type"] == "skill_invocation":
        images = "\n\n[image]" * len(content.get("images", []))
        return (
            f"[user]\nSkill /{content['name']} invoked.\n\nInstructions:\n{content['instructions']}"
            f"\n\nArguments:\n{content['arguments']}{images}"
        )
    parts = [part["content"] if part["type"] == "text" else "[image]" for part in content["parts"]]
    return "[user]\n" + "\n\n".join(parts)


def compact(key, model, provider, text):
    body = {
        "model": model,
        "provider": {"order": [provider], "allow_fallbacks": False},
        "messages": [{"role": "user", "content": f"<session>\n{text}\n</session>\n\n{PROMPT}"}],
        "usage": {"include": True},
    }
    request = urllib.request.Request(
        ENDPOINT,
        data=json.dumps(body).encode(),
        headers={"Authorization": f"Bearer {key}", "Content-Type": "application/json"},
    )
    started = time.monotonic()
    try:
        with urllib.request.urlopen(request, timeout=900) as response:
            reply = json.load(response)
    except urllib.error.HTTPError as error:
        reply = {"error": {"status": error.code, "body": error.read().decode(errors="replace")}}
    return reply | {"seconds": round(time.monotonic() - started, 1)}


def result(session, model, text, reply):
    usage = reply.get("usage") or {}
    if "error" in reply:
        summary = f"Request failed: {json.dumps(reply['error'], indent=2)}"
    else:
        summary = reply["choices"][0]["message"]["content"] or ""
    header = [
        f"Session: {session['id']}",
        f"Title: {session['title']}",
        f"Workspace: {session['workspace_path']}",
        f"Model: {model} (provider {reply.get('provider')})",
        f"Session characters: {len(text)}",
        f"Input tokens: {usage.get('prompt_tokens')}",
        f"Output tokens: {usage.get('completion_tokens')}"
        f" (reasoning {(usage.get('completion_tokens_details') or {}).get('reasoning_tokens')})",
        f"Cost: {usage.get('cost')}",
        f"Seconds: {reply['seconds']}",
        f"Summary characters: {len(summary)}",
    ]
    return "\n".join(
        ["\n".join(header), "=== Prompt ===", PROMPT, "=== Summary ===", summary, "=== Session ===", text]
    ) + "\n"


raise SystemExit(main())
