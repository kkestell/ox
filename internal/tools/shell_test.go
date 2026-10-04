package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"ox/internal/shellproc"
	"ox/internal/transcript"
)

func (t *Toolbox) shellCall(command string) transcript.ToolOutcome {
	return t.call(Shell, object{"command": command})
}

// waitFor waits until path exists.
func waitFor(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never appeared", path)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// assertGone waits until the process whose PID is in file no longer runs. A
// zombie counts as gone, since Ox does not reap descendants.
func assertGone(t *testing.T, file string) {
	t.Helper()
	pid := strings.TrimSpace(readText(t, file))
	deadline := time.Now().Add(3 * time.Second)
	for {
		output, _ := exec.Command("ps", "-o", "stat=", "-p", pid).Output()
		state := strings.TrimSpace(string(output))
		if state == "" || strings.HasPrefix(state, "Z") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("process %s from %s is still running (%s)", pid, file, state)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestShellArgumentsAreValidatedBeforeAnythingRuns(t *testing.T) {
	tools := toolbox(t)
	for _, arguments := range []string{
		"{", "{}", `{"command":1}`,
		`{"command":"touch wrong","extra":1}`,
		`{"command":"touch wrong","timeout_seconds":0}`,
		`{"command":"touch wrong","timeout_seconds":601}`,
		`{"command":"touch wrong","timeout_seconds":-1}`,
		`{"command":"touch wrong","timeout_seconds":1.5}`,
		`{"command":"touch wrong","timeout_seconds":"1"}`,
		`{"command":"touch wrong","timeout_seconds":null}`,
		`{"command":"touch wrong","background":true,"timeout_seconds":null}`,
		`{"command":"touch wrong","background":"yes"}`,
		`{"command":"touch wrong","background":true,"timeout_seconds":120}`,
	} {
		if outcome := tools.call(Shell, arguments); outcome.Status != transcript.ToolFailed {
			t.Errorf("%s: %+v", arguments, outcome)
		}
	}
	if _, err := os.Stat(filepath.Join(tools.Workspace, "wrong")); err == nil {
		t.Error("an invalid call ran")
	}
	for _, seconds := range []int{1, 600} {
		if outcome := tools.call(Shell, object{"command": "", "timeout_seconds": seconds}); outcome.Status != transcript.ToolCompleted {
			t.Errorf("timeout %d: %+v", seconds, outcome)
		}
	}
}

func TestShellReportsStatusAndBothStreams(t *testing.T) {
	tools := toolbox(t)
	outcome := tools.shellCall("pwd; printf hello; printf problem >&2; read value; test $? -ne 0; test ! -t 0")
	canonical, _ := filepath.EvalSymlinks(tools.Workspace)
	if outcome.Status != transcript.ToolCompleted ||
		outcome.Text != "Exit code: 0\n\nstdout:\n"+canonical+"\nhello\n\nstderr:\nproblem" ||
		len(outcome.Content) != 3 || outcome.Content[1].Text != canonical+"\nhello" {
		t.Errorf("outcome = %+v", outcome)
	}
	if outcome := tools.shellCall("printf out; exit 7"); outcome.Status != transcript.ToolFailed ||
		outcome.Text != "Exit code: 7\n\nstdout:\nout\n\nstderr:\n(empty)" {
		t.Errorf("failure = %+v", outcome)
	}
	if outcome := tools.shellCall("kill -TERM $$"); !strings.HasPrefix(outcome.Text, "Terminated by signal: 15") {
		t.Errorf("signal = %q", outcome.Text)
	}
	t.Setenv("OPENROUTER_API_KEY", "secret")
	if outcome := tools.shellCall(`printf "${OPENROUTER_API_KEY:-unset}"`); !strings.Contains(outcome.Text, "stdout:\nunset") {
		t.Errorf("the API key reached the command: %q", outcome.Text)
	}
}

func TestLargeOutputKeepsItsStartAndEnd(t *testing.T) {
	tools := toolbox(t)
	outcome := tools.shellCall("i=0; while [ $i -lt 5000 ]; do printf 'line %05d\\n' $i; i=$((i+1)); done; printf END")
	if len(outcome.Text) > outputLimit || !strings.Contains(outcome.Text, "stdout: (start and end of 55003 bytes; the middle is omitted)") ||
		!strings.Contains(outcome.Text, "line 00000") || !strings.HasSuffix(strings.TrimSuffix(outcome.Text, "\n\nstderr:\n(empty)"), "END") ||
		!strings.Contains(outcome.Text, "\n[...]\n") {
		t.Errorf("text = %.300q", outcome.Text)
	}
	floods := tools.shellCall("head -c 30000 /dev/zero | tr '\\0' o; head -c 30000 /dev/zero | tr '\\0' e >&2")
	if len(floods.Text) > outputLimit || strings.Count(floods.Text, "the middle is omitted") != 2 {
		t.Errorf("two floods: %d bytes", len(floods.Text))
	}
}

func TestTimeoutCancellationAndExitStopTheWholeGroup(t *testing.T) {
	tools := toolbox(t)
	group := "sleep 60 & echo $! > child; echo $$ > shell; "
	outcome := tools.call(Shell, object{"command": group + "sleep 60", "timeout_seconds": 1})
	if outcome.Status != transcript.ToolFailed || !strings.HasPrefix(outcome.Text, "Timed out after 1 seconds") {
		t.Errorf("timeout = %+v", outcome)
	}
	assertGone(t, filepath.Join(tools.Workspace, "child"))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan transcript.ToolOutcome)
	go func() { done <- tools.Execute(ctx, call(Shell, object{"command": group + "touch started; sleep 60"})) }()
	waitFor(t, filepath.Join(tools.Workspace, "started"))
	cancel()
	if outcome := <-done; outcome.Status != transcript.ToolCancelled || !strings.HasPrefix(outcome.Text, "Cancelled during execution") {
		t.Errorf("cancelled = %+v", outcome)
	}
	assertGone(t, filepath.Join(tools.Workspace, "child"))

	// A background child that keeps the output pipes open does not hold the
	// call past the drain deadline.
	started := time.Now()
	outcome = tools.shellCall("sleep 60 & echo $! > child; printf done")
	if outcome.Status != transcript.ToolCompleted || time.Since(started) > 3*time.Second {
		t.Errorf("exit with a background child: %+v after %v", outcome, time.Since(started))
	}
	assertGone(t, filepath.Join(tools.Workspace, "child"))
	if outcome := tools.Execute(ctx, call(Shell, object{"command": "touch never"})); outcome.Text != "Cancelled before this tool was started." {
		t.Errorf("already cancelled = %+v", outcome)
	}
}

var processID = regexp.MustCompile(`Started shell process (\S+)\.`)

func start(t *testing.T, tools *Toolbox, command string) string {
	t.Helper()
	outcome := tools.call(Shell, object{"command": command, "background": true})
	match := processID.FindStringSubmatch(outcome.Text)
	if outcome.Status != transcript.ToolCompleted || match == nil {
		t.Fatalf("start: %+v", outcome)
	}
	return match[1]
}

func TestBackgroundProcessesKeepRunningAndAcceptInput(t *testing.T) {
	tools := toolbox(t)
	id := start(t, tools, "printf ready; cat")
	if outcome := tools.call(ShellProcess, object{"action": "list"}); outcome.Text != id+" running: printf ready; cat" {
		t.Errorf("list = %q", outcome.Text)
	}
	write := tools.call(ShellProcess, object{"action": "write", "process_id": id, "text": "one\ntwo", "close_stdin": true})
	if write.Text != "Wrote 7 bytes to shell process "+id+"; stdin is closed." {
		t.Errorf("write = %q", write.Text)
	}
	read := tools.call(ShellProcess, object{"action": "read", "process_id": id, "wait_seconds": 5})
	if read.Status != transcript.ToolCompleted || !strings.HasSuffix(read.Text, "State: exited\nExit code: 0\n\nstdout:\nreadyone\ntwo\n\nstderr:\n(empty)") {
		t.Errorf("read = %+v", read)
	}
	late := tools.call(ShellProcess, object{"action": "write", "process_id": id, "text": "late"})
	if late.Status != transcript.ToolFailed || !strings.Contains(late.Text, "the command has finished") {
		t.Errorf("late write = %q", late.Text)
	}
	if outcome := tools.call(ShellProcess, object{"action": "read", "process_id": "missing"}); !strings.HasPrefix(outcome.Text, "No shell process missing") {
		t.Errorf("missing = %q", outcome.Text)
	}

	stopped := start(t, tools, "trap 'printf bye; exit 3' TERM; printf up; while true; do sleep 0.1; done")
	waitForOutput(t, tools, stopped, "up")
	stop := tools.call(ShellProcess, object{"action": "stop", "process_id": stopped})
	if stop.Status != transcript.ToolCompleted || !strings.Contains(stop.Text, "State: stopped\nExit code: 3") || !strings.Contains(stop.Text, "upbye") {
		t.Errorf("stop = %+v", stop)
	}
}

func waitForOutput(t *testing.T, tools *Toolbox, id, text string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(captured(tools.Processes.Get(id).Output().Stdout), text) {
		if time.Now().After(deadline) {
			t.Fatalf("%s never printed %q", id, text)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestProcessActionsValidateArgumentsAndClassifyPermission(t *testing.T) {
	tools := toolbox(t)
	for _, arguments := range []string{
		`{}`, `{"action":"restart"}`, `{"action":"list","process_id":"p"}`, `{"action":"read"}`,
		`{"action":"read","process_id":"p","wait_seconds":31}`, `{"action":"write","process_id":"p"}`,
		`{"action":"write","process_id":"p","text":""}`, `{"action":"stop","process_id":"p","text":"x"}`,
		`{"action":"write","process_id":"p","text":"` + strings.Repeat("x", maxInputBytes+1) + `"}`,
	} {
		if _, err := parseProcessAction(arguments); err == nil {
			t.Errorf("%.80s parsed", arguments)
		}
		if permission := tools.Permission(call(ShellProcess, arguments)); permission.Kind != PermissionNone {
			t.Errorf("%.80s needs permission", arguments)
		}
	}
	if action, err := parseProcessAction(`{"action":"write","process_id":"p","text":"","close_stdin":true}`); err != nil || !action.CloseStdin {
		t.Errorf("closing without text: %+v %v", action, err)
	}
	id := start(t, tools, "cat")
	permission := tools.Permission(call(ShellProcess, object{"action": "write", "process_id": id, "text": "y\n"}))
	if permission != (Permission{Kind: PermissionInput, ProcessID: id, Command: "cat", Text: "y\n"}) {
		t.Errorf("write permission = %+v", permission)
	}
	if tools.Permission(call(Shell, "not json")).Kind != PermissionCommand {
		t.Error("an invalid shell call ran without permission")
	}
	for _, action := range []string{"list", "read", "stop"} {
		if tools.Permission(call(ShellProcess, object{"action": action, "process_id": id})).Kind != PermissionNone && action != "list" {
			t.Errorf("%s needs permission", action)
		}
	}
}

func captured(capture *shellproc.Capture) string {
	head, tail := capture.Text()
	return head + tail
}
