package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ox/internal/openroutertest"
	"ox/internal/servertest"
)

// tmux runs ox in a detached tmux session against a scripted OpenRouter.
type tmux struct {
	t                       *testing.T
	root, socket, workspace string
}

func quote(text string) string {
	return "'" + strings.ReplaceAll(text, "'", `'\''`) + "'"
}

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o666); err != nil {
		t.Fatal(err)
	}
}

// startTmux starts ox in an 80 by 24 pane. The pane's shell reports whether
// ox restored the terminal and how it exited, then stays usable.
func startTmux(t *testing.T, skill bool, replies ...openroutertest.Reply) *tmux {
	t.Helper()
	if os.Getenv("OX_E2E") == "" {
		t.Skip("set OX_E2E to run the tmux tests; run make e2e")
	}
	// tmux socket paths must be short, so the root is not the test's
	// directory.
	root, err := os.MkdirTemp("", "ox-e2e-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	x := &tmux{t: t, root: root, socket: filepath.Join(root, "tmux.sock"), workspace: filepath.Join(root, "workspace")}
	if err := os.Mkdir(x.workspace, 0o777); err != nil {
		t.Fatal(err)
	}
	openrouter := openroutertest.Start(t, append([]openroutertest.Reply{openroutertest.CurrentCatalog()}, replies...)...)
	environment := servertest.Environment(t, root, openrouter.URL)
	if skill {
		write(t, filepath.Join(root, ".config/ox/skills/tally/SKILL.md"), "---\nname: tally\ndescription: Count tallies.\n---\nCount them.\n")
	}
	// The comparison leaves out the first line, the window size, and pendin,
	// which the kernel sets when canonical input returns and clears at the
	// next read.
	script := filepath.Join(root, "shell.sh")
	write(t, script, fmt.Sprintf("settings() { stty -a | sed -e 1d -e 's/-*pendin//'; }\nbefore=$(settings)\n%s --dir %s\nresult=$?\n"+
		"[ \"$(settings)\" = \"$before\" ] && echo TERMINAL_RESTORED\necho EXIT_$result\nexec /bin/sh\n",
		quote(servertest.Binary("ox")), quote(x.workspace)))
	command := []string{"env", "XDG_STATE_HOME=" + quote(filepath.Join(root, "state"))}
	for _, variable := range environment {
		name, value, _ := strings.Cut(variable, "=")
		command = append(command, name+"="+quote(value))
	}
	command = append(command, "/bin/sh", quote(script))
	conf := filepath.Join(root, "tmux.conf")
	write(t, conf, "set -g focus-events on\nset -g extended-keys always\nset -g extended-keys-format csi-u\n")
	t.Cleanup(func() { x.command("kill-server").Run() })
	x.call("-f", conf, "new-session", "-d", "-s", "test", "-x", "80", "-y", "24", strings.Join(command, " "))
	x.wait("0% • $0.00")
	return x
}

func (x *tmux) command(args ...string) *exec.Cmd {
	cmd := exec.Command("tmux", append([]string{"-S", x.socket}, args...)...)
	var environment []string
	for _, variable := range os.Environ() {
		if !strings.HasPrefix(variable, "NO_COLOR=") && !strings.HasPrefix(variable, "TMUX=") && !strings.HasPrefix(variable, "HOME=") {
			environment = append(environment, variable)
		}
	}
	cmd.Env = append(environment, "HOME="+x.root)
	return cmd
}

func (x *tmux) call(args ...string) string {
	x.t.Helper()
	output, err := x.command(args...).Output()
	if err != nil {
		var stderr string
		if exit, ok := err.(*exec.ExitError); ok {
			stderr = string(exit.Stderr)
		}
		x.t.Fatalf("tmux %q: %v %s", args, err, stderr)
	}
	return string(output)
}

// screen returns the pane's current screen, one line per row.
func (x *tmux) screen() string { return x.call("capture-pane", "-p", "-t", "test:0.0") }

// styledScreen returns the pane's current screen with its ANSI attributes.
func (x *tmux) styledScreen() string { return x.call("capture-pane", "-p", "-e", "-t", "test:0.0") }

func (x *tmux) wait(text string)     { x.t.Helper(); x.waitFor(text, true) }
func (x *tmux) waitGone(text string) { x.t.Helper(); x.waitFor(text, false) }

func (x *tmux) waitFor(text string, present bool) {
	x.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		screen := x.screen()
		if strings.Contains(screen, text) == present {
			return
		}
		if time.Now().After(deadline) {
			state := "missing"
			if !present {
				state = "still showing"
			}
			x.t.Fatalf("%s %q:\n%s", state, text, screen)
		}
		time.Sleep(30 * time.Millisecond)
	}
}

func (x *tmux) waitTitle(title string) {
	x.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		current := strings.TrimRight(x.call("display-message", "-p", "-t", "test:0.0", "#{pane_title}"), "\n")
		if current == title {
			return
		}
		if time.Now().After(deadline) {
			x.t.Fatalf("title %q is not %q", current, title)
		}
		time.Sleep(30 * time.Millisecond)
	}
}

func (x *tmux) keys(keys ...string) {
	x.t.Helper()
	x.call(append([]string{"send-keys", "-t", "test:0.0"}, keys...)...)
}

func (x *tmux) typeText(text string) {
	x.t.Helper()
	x.call("send-keys", "-t", "test:0.0", "-l", text)
}

func (x *tmux) prompt(text string) {
	x.t.Helper()
	x.typeText(text)
	x.keys("Enter")
}

// attach attaches a control-mode client, which gives the panes focus.
func (x *tmux) attach() *exec.Cmd {
	x.t.Helper()
	client := x.command("-C", "attach-session", "-t", "test")
	if _, err := client.StdinPipe(); err != nil {
		x.t.Fatal(err)
	}
	if err := client.Start(); err != nil {
		x.t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for strings.TrimSpace(x.call("list-clients")) == "" {
		if time.Now().After(deadline) {
			x.t.Fatal("the tmux client did not attach")
		}
		time.Sleep(30 * time.Millisecond)
	}
	return client
}

func (x *tmux) detach(client *exec.Cmd) {
	x.t.Helper()
	x.call("detach-client", "-s", "test")
	done := make(chan error, 1)
	go func() { done <- client.Wait() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		x.t.Fatal("the tmux client did not detach")
	}
}

// killServer stops the ox-server that the pane's ox started.
func (x *tmux) killServer() {
	x.t.Helper()
	child := func(pid string) string {
		output, err := exec.Command("pgrep", "-P", pid).Output()
		if err != nil {
			x.t.Fatalf("no child process of %s", pid)
		}
		return strings.Fields(string(output))[0]
	}
	pane := strings.TrimSpace(x.call("display-message", "-p", "-t", "test:0.0", "#{pane_pid}"))
	server := child(child(pane))
	if err := exec.Command("kill", server).Run(); err != nil {
		x.t.Fatal(err)
	}
}

const approvalHeading = "Would you like to run the following command?"

func hang(text string) openroutertest.Reply {
	data, _ := json.Marshal(openroutertest.Delta(map[string]any{"role": "assistant", "content": text}, ""))
	return openroutertest.Hang("data: " + string(data) + "\n\n")
}

func streamed(parts ...string) openroutertest.Reply {
	var chunks []map[string]any
	for i, part := range parts {
		finish := ""
		if i == len(parts)-1 {
			finish = "stop"
		}
		chunks = append(chunks, openroutertest.Delta(map[string]any{"role": "assistant", "content": part}, finish))
	}
	return openroutertest.Chunks(chunks...)
}

// renderReplies think, call four tools, and answer.
func renderReplies() []openroutertest.Reply {
	call := func(index int, id, name string, arguments map[string]any) map[string]any {
		data, _ := json.Marshal(arguments)
		return map[string]any{"index": index, "id": id, "type": "function", "function": map[string]any{"name": name, "arguments": string(data)}}
	}
	calls := []any{
		call(0, "run-1", "shell", map[string]any{"command": "ls"}),
		call(1, "read-1", "read_file", map[string]any{"path": "tallies/2026/september/archive/a.tally"}),
		call(2, "run-2", "shell", map[string]any{"command": `printf 'a.tally\nb.tally\n'`, "background": true}),
		call(3, "patch-1", "apply_patch", map[string]any{"patch": "*** Begin Patch\n*** Update File: a.tally\n@@\n-one\n+two\n*** End Patch"}),
	}
	return []openroutertest.Reply{
		openroutertest.Chunks(
			openroutertest.Delta(map[string]any{"role": "assistant", "reasoning": "weighing the tallies"}, ""),
			openroutertest.Delta(map[string]any{"role": "assistant", "tool_calls": calls}, "tool_calls"),
		),
		openroutertest.Text("Two tallies were counted in the workspace:\n\na.tally and b.tally"),
	}
}

func createTallies(x *tmux) {
	write(x.t, filepath.Join(x.workspace, "a.tally"), "one\n")
	write(x.t, filepath.Join(x.workspace, "b.tally"), "two\n")
	write(x.t, filepath.Join(x.workspace, "tallies/2026/september/archive/a.tally"), "one\ntwo\n")
}

func TestTerminalResumePickerShowsASavedSessionAndReplaysOnEnter(t *testing.T) {
	x := startTmux(t, false, openroutertest.Echo(), openroutertest.Echo())
	x.prompt("original transcript")
	x.wait("you said: original transcript")
	x.prompt("/resume")
	x.wait("Search")
	x.wait("original transcript")
	x.wait(time.Now().UTC().Format(time.DateOnly))
	x.keys("Escape")
	x.waitGone("Search")
	x.wait("you said: original transcript")
	x.prompt("/resume")
	x.wait("Search")
	x.keys("Enter")
	x.waitGone("Search")
	x.wait("you said: original transcript")
	if !strings.Contains(x.screen(), " original transcript") {
		t.Error(x.screen())
	}
	x.prompt("after resume")
	x.wait("you said: after resume")
}

func TestTerminalModelPickerShowsProvidersAndPricesAndChangesTheModel(t *testing.T) {
	x := startTmux(t, false)
	x.prompt("/model")
	x.wait("Search")
	screen := x.screen()
	for _, row := range []string{
		"DeepSeek V4.1 Flash                  OpenRouter  $0.03  $0.60  1,048,576",
		"GLM 5.3 Flash                        OpenRouter  $0.04  $0.14  1,310,720",
	} {
		if !strings.Contains(screen, row) {
			t.Errorf("missing %q:\n%s", row, screen)
		}
	}
	if strings.Contains(screen, "0% • $0.00") || strings.Index(screen, "GLM 5.3 Flash") < strings.Index(screen, "DeepSeek V4.1 Flash") {
		t.Errorf("%s", screen)
	}
	x.keys("Down", "C-f")
	x.wait("1,310,720\n    DeepSeek V4.1 Flash")
	if styled := x.styledScreen(); !strings.Contains(styled, "\x1b[1m") {
		t.Errorf("the favorite is not bold: %q", styled)
	}
	var config map[string]any
	data, _ := os.ReadFile(filepath.Join(x.root, ".config/ox/settings.json"))
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	if config["model"] != openroutertest.DefaultModel || fmt.Sprint(config["favorites"]) != "[openrouter:z-ai/glm-5.3-flash]" {
		t.Errorf("%v", config)
	}
	x.keys("Enter")
	x.waitGone("Search")
	x.wait("Ask • GLM 5.3 Flash")
	x.prompt("/model")
	x.wait("Search")
	if screen := x.screen(); strings.Index(screen, "GLM 5.3 Flash") > strings.Index(screen, "DeepSeek V4.1 Flash") {
		t.Errorf("the favorite is not first:\n%s", screen)
	}
}

func TestTerminalResumeDuringAPromptWaitsForTheTurnToEnd(t *testing.T) {
	x := startTmux(t, false, hang("running; waiting for cancellation"))
	x.prompt("running")
	x.wait("running; waiting for cancellation")
	x.prompt("/resume")
	x.wait("❯ /resume")
	if strings.Contains(x.screen(), "Search") {
		t.Error(x.screen())
	}
	x.keys("Escape")
	x.waitTitle("ox: ready")
	x.keys("Enter")
	x.wait("Search")
}

func TestTerminalKeysSendInterruptApproveScrollAndRestoreTheShell(t *testing.T) {
	x := startTmux(t, false,
		streamed("stream ", "arrives ", "in order\n"),
		openroutertest.Shell("printf denied"),
		openroutertest.Text("selected deny"),
		openroutertest.Shell("printf approved"),
		openroutertest.Text("selected approve"),
		hang("running one; waiting for cancellation"),
		hang("running two; waiting for cancellation"),
		openroutertest.Echo(),
		openroutertest.Echo(),
		openroutertest.Echo(),
		hang("running three; waiting for cancellation"),
	)
	x.prompt("stream")
	x.wait("stream arrives in order")
	x.prompt("tool")
	x.wait(approvalHeading)
	screen := x.screen()
	for _, text := range []string{approvalHeading, "● Shell printf denied", "Working directory:", "Command:", "printf denied", "› 1. Yes", "2. No"} {
		if !strings.Contains(screen, text) {
			t.Errorf("missing %q:\n%s", text, screen)
		}
	}
	x.keys("Down", "Enter")
	x.wait("selected deny")
	x.waitGone(approvalHeading)
	x.prompt("tool")
	x.wait("› 1. Yes")
	x.keys("Enter")
	x.wait("selected approve")
	x.prompt("running")
	x.wait("running one; waiting for cancellation")
	x.keys("Escape")
	x.waitTitle("ox: ready")
	x.prompt("running")
	x.wait("running two; waiting for cancellation")
	x.prompt("interrupting")
	x.wait("❯ interrupting")
	x.keys("Escape")
	x.waitTitle("ox: ready")
	x.keys("Enter")
	x.wait("you said: interrupting")
	if screen := x.screen(); !strings.Contains(screen, "   interrupting\n\n\n  ● you said: interrupting") {
		t.Errorf("%s", screen)
	}
	x.typeText("first")
	x.keys("S-Enter")
	x.typeText("second")
	x.wait("  ❯ first\n    second\n")
	x.keys("Enter")
	x.wait("  ● you said: first\n    second\n")
	x.call("set-buffer", "pasted界\nthird line")
	x.call("paste-buffer", "-p", "-t", "test:0.0")
	x.wait("  ❯ pasted界\n    third line\n")
	if strings.Contains(x.screen(), "you said: pasted") {
		t.Error("a paste was sent before Enter")
	}
	x.keys("Enter")
	x.wait("  ● you said: pasted界\n    third line\n")
	x.prompt("running")
	x.wait("running three; waiting for cancellation")
	x.keys("PageUp")
	x.waitGone("running three; waiting for cancellation")
	x.keys("End")
	x.wait("running three; waiting for cancellation")
	x.keys("Escape")
	x.keys("C-d")
	x.wait("TERMINAL_RESTORED")
	x.wait("EXIT_0")
	x.prompt("echo SHELL_USABLE")
	x.wait("\nSHELL_USABLE\n")
}

func TestTerminalMouseWheelScrollsTheTranscriptOneLinePerEvent(t *testing.T) {
	x := startTmux(t, false, renderReplies()...)
	createTallies(x)
	x.keys("Tab")
	x.wait("Auto")
	x.call("resize-window", "-t", "test:0", "-x", "80", "-y", "12")
	x.prompt("render")
	x.wait("a.tally and b.tally")
	before := x.screen()
	// An SGR mouse wheel up at column 5, row 3.
	x.call("send-keys", "-t", "test:0.0", "-H", "1b", "5b", "3c", "36", "34", "3b", "35", "3b", "33", "4d")
	x.waitGone("a.tally and b.tally")
	after := x.screen()
	if strings.Split(before, "\n")[1] != strings.Split(after, "\n")[2] {
		t.Errorf("before:\n%s\nafter:\n%s", before, after)
	}
	// An SGR mouse wheel down at the same position.
	x.call("send-keys", "-t", "test:0.0", "-H", "1b", "5b", "3c", "36", "35", "3b", "35", "3b", "33", "4d")
	x.wait("a.tally and b.tally")
	if screen := x.screen(); screen != before {
		t.Errorf("before:\n%s\nafter:\n%s", before, screen)
	}
}

func TestTerminalTranscriptRendersThinkingToolsAndWrappedReplies(t *testing.T) {
	x := startTmux(t, false, renderReplies()...)
	createTallies(x)
	x.keys("Tab")
	x.wait("Auto")
	x.call("resize-window", "-t", "test:0", "-x", "40", "-y", "80")
	x.prompt("render")
	x.wait("a.tally and b.tally")
	screen := x.screen()
	position := 0
	for _, text := range []string{
		" render",
		"● Thought for 0s",
		"● Shell ls",
		"● Read tallies/2026/september/archi…",
		`● Shell printf 'a.tally\nb.tally\n'…`,
		"● Apply patch to a.tally",
		"● Two tallies were counted in the",
		"a.tally and b.tally",
	} {
		found := strings.Index(screen[position:], text)
		if found < 0 {
			t.Fatalf("missing %q:\n%s", text, screen)
		}
		position += found + len(text)
	}
	styled := x.styledScreen()
	gray := func(text string) bool { return strings.Contains(styled, "\x1b[38;2;112;112;112m"+text) }
	if !gray("● Thought for 0s") || gray("● Two tallies") {
		t.Errorf("%q", styled)
	}
	x.keys("C-o")
	x.wait("Lines 1–2 of 2")
	screen = x.screen()
	for _, text := range []string{
		"● Shell ls\n    └ Exit code: 0",
		"      a.tally\n      b.tally\n      tallies",
		"● Read tallies/2026/september/archi…\n    └ Lines 1–2 of 2",
		"● Apply patch to a.tally\n    └ Modified a.tally",
		"      @@ -1 +1 @@\n      -one\n      +two",
	} {
		if !strings.Contains(screen, text) {
			t.Errorf("missing %q:\n%s", text, screen)
		}
	}
	if styled := x.styledScreen(); !strings.Contains(styled, "\x1b[38;2;152;195;121m    +two") {
		t.Errorf("%q", styled)
	}
	x.keys("C-o", "C-o")
	x.waitGone("Lines 1–2 of 2")
}

func TestTerminalStatusLineShowsTheSessionSettingsAndUsage(t *testing.T) {
	reply := openroutertest.Chunks(
		openroutertest.Delta(map[string]any{"role": "assistant", "content": "usage recorded"}, "stop"),
		openroutertest.Usage(157_286, 1, 0.25),
	)
	x := startTmux(t, false, reply)
	x.keys("Tab", "C-e", "C-e", "C-e")
	x.wait("Auto • DeepSeek V4.1 Flash • High")
	x.prompt("usage")
	x.wait("15% • $0.25")
	lines := strings.Split(strings.TrimSuffix(x.screen(), "\n"), "\n")
	status, last := lines[len(lines)-2], lines[len(lines)-1]
	if !strings.HasPrefix(status, "  Auto • DeepSeek V4.1 Flash • High") || !strings.HasSuffix(status, "15% • $0.25") || strings.TrimSpace(last) != "" {
		t.Errorf("%q", lines)
	}
}

func TestTerminalTabAndShiftTabCycleModes(t *testing.T) {
	x := startTmux(t, false)
	x.wait("Ask")
	x.keys("Tab")
	x.wait("Auto")
	x.keys("S-Tab")
	x.wait("Ask")
}

func TestTerminalControlECyclesEffort(t *testing.T) {
	x := startTmux(t, false)
	x.wait("DeepSeek V4.1 Flash • Default")
	x.keys("C-e")
	x.wait("DeepSeek V4.1 Flash • Low")
	x.keys("C-e")
	x.wait("DeepSeek V4.1 Flash • Medium")
}

func TestTerminalTabCompletesASlashCommandFromGhostText(t *testing.T) {
	x := startTmux(t, true, openroutertest.Echo())
	x.wait("Ask")
	x.keys("/", "t", "a")
	x.wait("/tally")
	x.keys("Tab")
	x.keys("Enter")
	x.wait("you said: Skill /tally invoked.")
	if !strings.Contains(x.screen(), "Ask") {
		t.Error(x.screen())
	}
}

func TestTerminalPermissionSurvivesDisconnectAndServerFailureRestoresTheShell(t *testing.T) {
	x := startTmux(t, false,
		openroutertest.Echo(),
		openroutertest.Shell("printf pending"),
		openroutertest.Text("selected deny"),
		openroutertest.Echo(),
	)
	client := x.attach()
	x.prompt("before detach")
	x.wait("you said: before detach")
	x.prompt("run a command")
	x.wait(approvalHeading)
	x.call("split-window", "-h", "-t", "test:0.0", "/bin/sh")
	x.call("send-keys", "-t", "test:0.1", "echo ADJACENT_SHELL", "Enter")
	x.detach(client)
	if strings.TrimSpace(x.call("list-clients")) != "" {
		t.Fatal("a client is still attached")
	}
	client = x.attach()
	x.keys("Down", "Enter")
	x.wait("selected deny")
	x.prompt("after reconnect")
	x.wait("you said: after reconnect")
	if !strings.Contains(x.screen(), "you said: before detach") {
		t.Error(x.screen())
	}
	if !strings.Contains(x.call("capture-pane", "-p", "-t", "test:0.1"), "\nADJACENT_SHELL\n") {
		t.Error("the adjacent shell lost its output")
	}
	x.killServer()
	x.wait("TERMINAL_RESTORED")
	x.wait("EXIT_1")
	x.prompt("echo SHELL_AFTER_FAILURE")
	x.wait("\nSHELL_AFTER_FAILURE\n")
	x.detach(client)
}

func TestTerminalPaneTitleShowsStatusAndKeepsUnseenResultsUntilFocus(t *testing.T) {
	x := startTmux(t, false,
		hang("running"),
		openroutertest.Shell("printf denied"),
		openroutertest.Text("denied"),
		openroutertest.Status(400, "{}"),
		streamed("stream ", "arrives ", "in order"),
	)
	client := x.attach()
	x.call("split-window", "-h", "-t", "test:0.0", "/bin/sh")
	x.waitTitle("ox: ready")
	x.prompt("running")
	x.waitTitle("ox: working")
	x.keys("Escape")
	x.waitTitle("ox: finished")
	x.prompt("run a command")
	x.waitTitle("ox: needs permission")
	x.keys("Down", "Enter")
	x.waitTitle("ox: finished")
	x.prompt("fail")
	x.waitTitle("ox: turn error")
	x.call("select-pane", "-t", "test:0.0")
	x.waitTitle("ox: ready")
	x.prompt("stream")
	x.wait("stream arrives in order")
	x.waitTitle("ox: ready")
	x.keys("C-d")
	x.wait("EXIT_0")
	x.waitTitle("")
	x.detach(client)
}
