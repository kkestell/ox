#!/usr/bin/env python3
# Exports a session from ox.db to a JSON file.
# Entry data and tool call arguments are decoded from their stored JSON text.
#   scripts/export_session.py 0792a899 -o session.json
#   jq '.transcript[] | select(.kind == "turn_start") | .data.model' session.json

import argparse
import json
import os
import sqlite3
from pathlib import Path


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("session", metavar="SESSION_ID_PREFIX")
    parser.add_argument("-o", "--output", type=Path, metavar="PATH")
    args = parser.parse_args()

    database = sqlite3.connect(f"file:{data_dir() / 'ox.db'}?mode=ro", uri=True)
    database.row_factory = sqlite3.Row
    ids = [
        row["id"]
        for row in database.execute(
            "SELECT id FROM sessions WHERE id LIKE ? || '%'", (args.session,)
        )
    ]
    if len(ids) != 1:
        parser.error(f"{len(ids)} sessions match {args.session!r}")

    output = args.output or Path(f"session-{ids[0]}.json")
    output.write_text(json.dumps(session(database, ids[0]), indent=2, ensure_ascii=False))
    print(output)


def data_dir():
    if os.environ.get("OX_DATA_DIR"):
        return Path(os.environ["OX_DATA_DIR"])
    if os.environ.get("XDG_DATA_HOME"):
        return Path(os.environ["XDG_DATA_HOME"]) / "ox"
    return Path.home() / ".local/share/ox"


def session(database, id):
    row = database.execute("SELECT * FROM sessions WHERE id = ?", (id,)).fetchone()
    transcript = [
        {"id": entry["id"], "kind": entry["kind"], "data": decode(json.loads(entry["data"]))}
        for entry in database.execute(
            "SELECT id, kind, data FROM transcript_entries WHERE session_id = ? ORDER BY id",
            (id,),
        )
    ]
    return dict(row) | {"transcript": transcript}


def decode(value):
    if isinstance(value, list):
        return [decode(item) for item in value]
    if not isinstance(value, dict):
        return value
    decoded = {key: decode(item) for key, item in value.items()}
    if "call_id" in decoded and isinstance(decoded.get("arguments"), str):
        try:
            decoded["arguments"] = json.loads(decoded["arguments"])
        except json.JSONDecodeError:
            pass
    return decoded


raise SystemExit(main())
