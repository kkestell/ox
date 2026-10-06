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
)

// directory holds the built ox and ox-server.
var directory string

func TestMain(m *testing.M) { os.Exit(runTests(m)) }

// runTests runs the tests, first building ox and ox-server into a temporary
// directory when OX_E2E is set.
func runTests(m *testing.M) int {
	if os.Getenv("OX_E2E") == "" {
		return m.Run()
	}
	var err error
	directory, err = os.MkdirTemp("", "ox-e2e-bin-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(directory)
	build := exec.Command("go", "build", "-o", directory+string(filepath.Separator), "ox/cmd/ox", "ox/cmd/ox-server")
	if output, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "building the Ox binaries: %v\n%s", err, output)
		return 1
	}
	return m.Run()
}

// binary returns the path of the built ox or ox-server.
func binary(name string) string {
	return filepath.Join(directory, name)
}

// environment writes global settings naming openroutertest.DefaultModel under
// root and returns the variables that give ox-server root as its home and data
// directory and point it at endpoint.
func environment(t *testing.T, root, endpoint string) []string {
	t.Helper()
	data, _ := json.Marshal(map[string]string{"model": openroutertest.DefaultModel})
	write(t, filepath.Join(root, ".config/ox/settings.json"), string(data))
	return []string{
		"HOME=" + root,
		"OX_DATA_DIR=" + filepath.Join(root, "data"),
		"OPENROUTER_API_KEY=test-key",
		"OX_OPENROUTER_ENDPOINT=" + endpoint,
	}
}

func TestServerCommandsRunTheBundledServer(t *testing.T) {
	if os.Getenv("OX_E2E") == "" {
		t.Skip("set OX_E2E to run the tests of the built binaries; run make e2e")
	}
	output, err := exec.Command(binary("ox"), "run", "-h").CombinedOutput()
	if err != nil || !strings.Contains(string(output), "ox-server run") {
		t.Errorf("%v: %s", err, output)
	}
}

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
func startTmux(t *testing.T, replies ...openroutertest.Reply) *tmux {
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
	openrouter := openroutertest.Start(t, append([]openroutertest.Reply{openroutertest.Status(200, openroutertest.Catalog)}, replies...)...)
	variables := environment(t, root, openrouter.URL)
	// The comparison leaves out the first line, the window size, and pendin,
	// which the kernel sets when canonical input returns and clears at the
	// next read.
	script := filepath.Join(root, "shell.sh")
	write(t, script, fmt.Sprintf("settings() { stty -a | sed -e 1d -e 's/-*pendin//'; }\nbefore=$(settings)\n%s --dir %s\nresult=$?\n"+
		"[ \"$(settings)\" = \"$before\" ] && echo TERMINAL_RESTORED\necho EXIT_$result\nexec /bin/sh\n",
		quote(binary("ox")), quote(x.workspace)))
	command := []string{"env", "XDG_STATE_HOME=" + quote(filepath.Join(root, "state"))}
	for _, variable := range variables {
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

func TestTerminalResumePickerShowsASavedSessionAndReplaysOnEnter(t *testing.T) {
	x := startTmux(t, openroutertest.Echo(), openroutertest.Echo())
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

func TestTerminalKeysSendInterruptApproveScrollAndRestoreTheShell(t *testing.T) {
	x := startTmux(t,
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

func TestTerminalPermissionSurvivesDisconnectAndServerFailureRestoresTheShell(t *testing.T) {
	x := startTmux(t,
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
	x := startTmux(t,
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
