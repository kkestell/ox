#!/usr/bin/env python3
"""Run opt-in smoke checks against Claude Code through oxd and oxctl."""

import argparse
import json
import os
import select
import shutil
import signal
import subprocess
import tempfile
import time
import traceback
from pathlib import Path

REPO = Path(__file__).resolve().parents[1]


def check(condition, message):
    if not condition:
        raise RuntimeError(message)


def command(args, *, cwd=REPO, env=None, timeout=30):
    result = subprocess.run(
        args,
        cwd=cwd,
        env=env,
        capture_output=True,
        text=True,
        timeout=timeout,
        check=False,
    )
    check(result.returncode == 0, f"{args[0]} failed: {result.stderr.strip()}")
    return result.stdout.strip()


def stop(process):
    if process.poll() is None:
        process.kill()
    process.wait(timeout=10)


def running_after(pids, timeout):
    """Waits up to `timeout` seconds for each process to exit and returns the
    ones still running. Exits arrive as kqueue events on macOS and as readable
    pidfds on Linux."""
    deadline = time.monotonic() + timeout
    if hasattr(select, "kqueue"):
        queue = select.kqueue()
        running = set()
        for pid in pids:
            event = select.kevent(
                pid,
                select.KQ_FILTER_PROC,
                select.KQ_EV_ADD | select.KQ_EV_ONESHOT,
                select.KQ_NOTE_EXIT,
            )
            try:
                queue.control([event], 0)
                running.add(pid)
            except ProcessLookupError:
                pass
        while running and (remaining := deadline - time.monotonic()) > 0:
            for event in queue.control(None, len(running), remaining):
                running.discard(event.ident)
        queue.close()
        return running
    descriptors = {}
    for pid in pids:
        try:
            descriptors[os.pidfd_open(pid)] = pid
        except ProcessLookupError:
            pass
    poll = select.poll()
    for descriptor in descriptors:
        poll.register(descriptor, select.POLLIN)
    while descriptors and (remaining := deadline - time.monotonic()) > 0:
        for descriptor, _ in poll.poll(remaining * 1000):
            poll.unregister(descriptor)
            os.close(descriptor)
            del descriptors[descriptor]
    for descriptor in descriptors:
        os.close(descriptor)
    return set(descriptors.values())


def kind(event):
    return event["kind"]["type"]


def update(event):
    return event["kind"]["data"] if kind(event) == "update" else {}


def message(event):
    return update(event).get("sessionUpdate") in (
        "agent_message_chunk",
        "agent_message",
    )


def blocks_text(blocks):
    return "".join(b.get("text", "") for b in blocks or [])


def message_text(data):
    """The text of a message update: a chunk's one block, or a whole
    message's list of blocks."""
    if "content" in data and isinstance(data["content"], dict):
        return data["content"].get("text", "")
    return blocks_text(data.get("content"))


def text(events):
    return "".join(message_text(update(e)) for e in events if message(e))


def user_text(events):
    return "".join(
        message_text(update(e))
        for e in events
        if update(e).get("sessionUpdate") in ("user_message", "user_message_chunk")
    )


def turn_result(stop_reason):
    """The data of a `turn_ended` event whose turn ended with `stop_reason`."""
    return {"stop_reason": {"stopReason": stop_reason}, "error": None}


class Attachment:
    def __init__(self, args, env, stderr, log):
        self.process = subprocess.Popen(
            args, env=env, stdout=subprocess.PIPE, stderr=stderr, bufsize=0
        )
        self.buffer = b""
        self.log = log

    def read(self, deadline):
        while b"\n" not in self.buffer:
            remaining = deadline - time.monotonic()
            check(remaining > 0, "Timed out waiting for an agent event")
            ready, _, _ = select.select([self.process.stdout], [], [], remaining)
            check(ready, "Timed out waiting for an agent event")
            data = os.read(self.process.stdout.fileno(), 65536)
            check(data, "Attachment exited; see attach.stderr.log")
            self.buffer += data
        line, self.buffer = self.buffer.split(b"\n", 1)
        event = json.loads(line)
        self.log.write(json.dumps(event) + "\n")
        self.log.flush()
        return event

    def close(self):
        stop(self.process)
        self.process.stdout.close()


class SmokeRun:
    def __init__(self, args, directory, binaries, events, stderr):
        self.args = args
        self.directory = directory
        self.project = directory / "project"
        self.project.mkdir()
        self.socket = directory / "oxd.sock"
        self.cli = [str(binaries / "oxctl"), "--socket", str(self.socket)]
        self.binaries = binaries
        self.events = events
        self.stderr = stderr
        self.env = os.environ.copy()
        self.agent = [str(binaries / "claude-code-acp")]
        self.env.update(
            CLAUDE_ACP_STORE_DIR=str(directory / "bridge-store"),
        )
        self.daemon = None
        self.workspace = None
        self.session = None
        self.attachment = None
        self.agent_pids = set()
        self.seq = 0
        self.results = []

    def report(self, name, **details):
        result = {"check": name, **details}
        self.results.append(result)
        (self.directory / "results.json").write_text(
            json.dumps(self.results, indent=2) + "\n"
        )
        print(json.dumps(result), flush=True)

    def ctl(self, *args):
        return command(self.cli + list(args), env=self.env, timeout=self.args.timeout)

    def attach(self, after):
        if self.attachment is not None:
            self.attachment.close()
        self.attachment = Attachment(
            self.cli + ["attach", self.session, "--after", str(after), "--json"],
            self.env,
            self.stderr,
            self.events,
        )

    def disconnect(self):
        self.attachment.close()
        self.attachment = None

    def until(self, predicate):
        deadline = time.monotonic() + self.args.timeout
        events = []
        while True:
            event = self.attachment.read(deadline)
            check(
                event["session_id"] == self.session, "Event belongs to another session"
            )
            check(event["seq"] == self.seq + 1, f"Sequence gap or duplicate: {event}")
            self.seq = event["seq"]
            events.append(event)
            if predicate(event):
                return events
            check(
                kind(event) not in ("turn_ended", "agent_exited"),
                f"Turn stopped before the expected event: {event}; text: {text(events)}",
            )

    def finish(self, stop_reason="end_turn"):
        events = self.until(lambda e: kind(e) == "turn_ended")
        check(
            events[-1]["kind"]["data"] == turn_result(stop_reason),
            f"Unexpected turn result: {events[-1]}",
        )
        check("Error:" not in text(events), f"Agent reported an error: {text(events)}")
        return events

    def permission(self):
        events = self.until(lambda e: kind(e) == "permission_requested")
        return events[-1]

    def remember_agents(self):
        # ACP agents own separate process groups; retain their PIDs for cleanup
        # if session startup or a later command fails.
        rows = command(["ps", "-axo", "pid=,ppid="]).splitlines()
        self.agent_pids.update(
            int(pid)
            for pid, parent in (row.split() for row in rows)
            if int(parent) == self.daemon.pid
        )

    def start_daemon(self):
        self.daemon = subprocess.Popen(
            [str(self.binaries / "oxd"), "--socket", str(self.socket)],
            stdout=subprocess.PIPE,
            stderr=self.daemon_log,
        )
        # oxd prints `ready` once it is listening and nothing else on stdout.
        with self.daemon.stdout:
            ready, _, _ = select.select([self.daemon.stdout], [], [], self.args.timeout)
            check(ready, "Daemon did not become ready")
            check(
                self.daemon.stdout.readline() == b"ready\n",
                "Daemon exited during startup; see oxd.log",
            )

    def run(self, daemon_log):
        self.daemon_log = daemon_log
        self.start_daemon()
        self.workspace = self.ctl("workspace", "add", str(self.project))
        self.session = self.ctl("new", "--workspace", self.workspace, "--", *self.agent)
        self.ctl("config", self.session, "model", self.args.model)
        self.remember_agents()
        check(self.agent_pids, "Could not find the daemon's ACP agent process")
        check("\tidle\t" in self.ctl("ls"), "New session is not idle")
        self.report(
            "real_session_created",
            session_id=self.session,
            requested_model=self.args.model,
        )

        self.check_reconnect()
        self.check_effort()
        self.check_permission()
        self.check_background_wake()
        self.check_cancellation()
        self.verify_model()
        self.check_load_after_restart()
        self.report("all_event_sequences_contiguous", event_count=self.seq)

        self.disconnect()
        self.ctl("delete", self.session)
        self.session = None
        check(self.ctl("ls") == "", "Deleted session is still listed")
        self.ctl("workspace", "rm", self.workspace)
        check(self.ctl("workspace", "ls") == "", "Removed workspace is still listed")
        self.workspace = None
        check(self.daemon.wait(timeout=15) == 0, "Daemon did not exit successfully")
        check(not self.socket.exists(), "Daemon left its socket behind")
        check(not running_after(self.agent_pids, 10), "ACP agent did not exit")
        self.report("delete_and_idle_exit", agent_exited=True, socket_removed=True)

    def check_reconnect(self):
        token = self.directory.name
        self.attach(0)
        self.ctl(
            "prompt",
            self.session,
            f"Remember this token for my next message: {token}. Do not use tools. "
            "Write a numbered list of 100 short factual sentences about arithmetic.",
        )
        first = self.until(message)
        cursor = self.seq
        self.disconnect()
        time.sleep(6)  # Longer than oxd's default five-second idle exit.
        listing = self.ctl("ls")
        check(self.session in listing, "Session disappeared without a client")
        self.attach(cursor)
        rest = self.finish()
        self.report(
            "mid_turn_client_killed_and_reattached",
            detached_after_seq=cursor,
            replay_events=len(rest),
            text_characters=len(text(first + rest)),
            survived_without_clients_seconds=6,
        )
        self.ctl(
            "prompt",
            self.session,
            "What exact token did I ask you to remember? Reply with only that token. "
            "Do not use tools.",
        )
        response = text(self.finish()).strip()
        check(token in response, f"Conversation token was not retained: {response}")
        self.report("followup_retains_conversation", response=response)

    def check_effort(self):
        """Sets the effort of the session's running Claude."""
        options = self.ctl("config", self.session, "effort", "low")
        effort = next(
            line.split("\t")
            for line in options.splitlines()
            if line.startswith("effort\t")
        )
        check(effort[1] == "low", f"Effort was not set: {effort}")
        self.report("effort_set_on_running_claude", effort=effort[1])

    def session_row(self):
        """The fields of the session's row in `oxctl ls`: id, workspace id,
        state, directory, command, title, and agent session id."""
        row = next(r for r in self.ctl("ls").splitlines() if r.startswith(self.session))
        return row.split("\t")

    def check_load_after_restart(self):
        token = self.directory.name
        title = self.session_row()[5]
        check(title != "-", "The session has no title")
        self.disconnect()
        self.remember_agents()
        self.daemon.send_signal(signal.SIGTERM)
        check(self.daemon.wait(timeout=15) == 0, "Daemon did not stop on SIGTERM")
        check(not running_after(self.agent_pids, 10), "ACP agent outlived the daemon")

        self.start_daemon()
        check(
            self.ctl("workspace", "ls") == f"{self.workspace}\t{self.project}",
            "The workspace was not saved",
        )
        row = self.session_row()
        check(row[2] == "exited", f"Saved session is not exited: {row}")
        check(row[1] == self.workspace, f"Saved session lost its workspace: {row}")
        check(row[5] == title, f"Saved session lost its title: {row}")
        self.report("saved_session_listed_after_daemon_restart", title=title)

        check(
            self.ctl("load", self.session) == self.session,
            "Load started another session",
        )
        self.remember_agents()
        self.seq = 0
        self.attach(0)
        replay = self.until(lambda e: kind(e) == "agent_started")
        check(
            f"Remember this token for my next message: {token}" in user_text(replay),
            f"Replay lacks the first prompt: {user_text(replay)[:300]}",
        )
        check(
            token in text(replay),
            f"Replay lacks the agent's answer: {text(replay)[:300]}",
        )
        self.report(
            "load_replays_after_daemon_restart",
            session_id=self.session,
            replay_events=len(replay),
        )
        self.ctl(
            "prompt",
            self.session,
            "What exact token did I ask you to remember at the start of this "
            "conversation? Reply with only that token. Do not use tools.",
        )
        response = text(self.finish()).strip()
        check(token in response, f"Loaded session lost the conversation: {response}")
        self.report("loaded_session_retains_conversation", response=response)

    def check_permission(self):
        self.ctl(
            "prompt",
            self.session,
            "Use your file write or patch tool to create approved.txt in the current "
            "working directory containing exactly permission-ok followed by a newline. "
            "Do not use shell commands. Then say done.",
        )
        requested = self.permission()
        permission = requested["kind"]["data"]
        self.disconnect()
        time.sleep(2)
        check(
            "\tawaiting_permission\t" in self.ctl("ls"),
            "Permission did not survive disconnect",
        )
        check(not (self.project / "approved.txt").exists(), "Write ran before approval")
        self.attach(self.seq - 1)
        replay = self.attachment.read(time.monotonic() + self.args.timeout)
        check(replay == requested, "Replayed permission differs from the original")
        option = next(
            o["optionId"] for o in permission["options"] if o["kind"] == "allow_once"
        )
        self.ctl("permit", self.session, permission["request_id"], option)
        events = self.finish()
        check(
            any(kind(e) == "permission_resolved" for e in events),
            "Approval was not recorded",
        )
        check(
            (self.project / "approved.txt").read_text() == "permission-ok\n",
            "Approved write did not produce the expected file",
        )
        self.report(
            "permission_survives_disconnect_and_allow_once",
            request_id=permission["request_id"],
        )

    def check_background_wake(self):
        token = f"woke-{self.directory.name}"
        self.ctl(
            "prompt",
            self.session,
            "Use the Bash tool with run_in_background set to true to run "
            "`sleep 8; echo finished`. Then reply with only the word started and "
            "end your turn without waiting for the command. When you are later "
            f"told that the background command finished, reply with only {token}.",
        )
        while True:
            events = self.until(
                lambda e: kind(e) in ("permission_requested", "turn_ended")
            )
            if kind(events[-1]) == "turn_ended":
                break
            permission = events[-1]["kind"]["data"]
            option = next(
                o["optionId"]
                for o in permission["options"]
                if o["kind"] == "allow_once"
            )
            self.ctl("permit", self.session, permission["request_id"], option)
        check(
            events[-1]["kind"]["data"] == turn_result("end_turn"),
            f"Unexpected turn result: {events[-1]}",
        )
        output = [
            block["content"]["text"]
            for e in events
            if kind(e) == "update"
            and e["kind"]["data"]["sessionUpdate"] == "tool_call_update"
            and e["kind"]["data"].get("status") == "completed"
            for block in e["kind"]["data"].get("content") or []
            if block.get("content", {}).get("type") == "text"
        ]
        check(output, "The Bash tool call reported no output")
        woke = self.until(lambda e: kind(e) == "turn_ended")
        check(
            any(kind(e) == "turn_started" for e in woke),
            f"Claude's own turn did not start with turn_started: {woke}",
        )
        check(
            woke[-1]["kind"]["data"] == turn_result("end_turn"),
            f"Unexpected end of Claude's own turn: {woke[-1]}",
        )
        check(token in text(woke), f"Claude did not answer: {text(woke)}")
        check("\tidle\t" in self.ctl("ls"), "Session is not idle after the turn")
        self.report(
            "background_task_wakes_claude",
            tool_output=output[-1].strip(),
            response=text(woke).strip(),
        )

    def check_cancellation(self):
        self.ctl(
            "prompt",
            self.session,
            "Use your file write or patch tool to create cancelled.txt containing "
            "cancelled. Do not use shell commands.",
        )
        self.permission()
        self.ctl("cancel", self.session)
        events = self.finish("cancelled")
        check(
            any(
                kind(e) == "permission_resolved"
                and e["kind"]["data"]["outcome"]["outcome"] == "cancelled"
                for e in events
            ),
            "Pending permission was not cancelled",
        )
        check(
            not (self.project / "cancelled.txt").exists(),
            "Cancelled write created a file",
        )
        self.report("cancel_pending_permission", file_created=False)
        self.ctl(
            "prompt",
            self.session,
            "Do not use tools. Write a numbered list of 200 short factual sentences "
            "about geometry.",
        )
        self.until(message)
        self.ctl("cancel", self.session)
        self.finish("cancelled")
        self.report("cancel_running_generation", stop_reason="cancelled")

    def verify_model(self):
        store = json.loads(
            (self.directory / "bridge-store/acp-sessions.json").read_text()
        )
        session = next(iter(store.values()))
        claude_dir = Path(
            self.env.get("CLAUDE_CONFIG_DIR", str(Path.home() / ".claude"))
        )
        transcript = next(
            (claude_dir / "projects").glob(f"*/{session['claudeSessionId']}.jsonl")
        )
        models = set()
        for line in transcript.read_text().splitlines():
            model = json.loads(line).get("message", {}).get("model")
            if model and model != "<synthetic>":
                models.add(model)
        check(
            models == {self.args.model},
            f"Unexpected transcript models: {sorted(models)}",
        )
        self.report("model_verified", models=sorted(models))

    def cleanup(self):
        try:
            if self.attachment is not None:
                self.disconnect()
            if self.daemon is not None and self.daemon.poll() is None:
                self.remember_agents()
                if self.session is not None:
                    self.ctl("delete", self.session)
        finally:
            try:
                if self.daemon is not None:
                    self.daemon.wait(timeout=8)
            except subprocess.TimeoutExpired:
                # A failed delete must not leave a live model request behind.
                for pid in self.agent_pids:
                    try:
                        os.killpg(pid, signal.SIGKILL)
                    except ProcessLookupError:
                        pass
            finally:
                if self.daemon is not None:
                    stop(self.daemon)


def positive_seconds(value):
    seconds = float(value)
    if not 0 < seconds < float("inf"):
        raise argparse.ArgumentTypeError("must be a positive, finite number")
    return seconds


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--model", required=True, help="Model ID; no fallback")
    parser.add_argument(
        "--timeout",
        type=positive_seconds,
        default=120,
        help="Seconds per command or event phase (default: 120)",
    )
    args = parser.parse_args()
    check(shutil.which("claude"), "claude is not installed or is not on PATH")
    packages = ["oxd", "oxctl", "claude-code-acp"]
    subprocess.run(
        ["cargo", "build", *[flag for p in packages for flag in ("-p", p)]],
        cwd=REPO,
        check=True,
    )
    metadata = json.loads(
        command(["cargo", "metadata", "--no-deps", "--format-version", "1"])
    )
    binaries = Path(metadata["target_directory"]) / "debug"
    directory = Path(tempfile.mkdtemp(prefix="ox-acp-", dir="/tmp")).resolve()
    print(f"Artifacts: {directory}", flush=True)
    with (
        (directory / "events.jsonl").open("w") as events,
        (directory / "attach.stderr.log").open("w") as stderr,
        (directory / "oxd.log").open("w") as daemon_log,
    ):
        run = SmokeRun(args, directory, binaries, events, stderr)
        try:
            try:
                run.run(daemon_log)
            finally:
                run.cleanup()
        except (Exception, KeyboardInterrupt) as error:  # noqa: BLE001 - every failure is reported
            run.report("failed", error=str(error) or "Interrupted")
            traceback.print_exc()
            return 1
        run.report("passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
