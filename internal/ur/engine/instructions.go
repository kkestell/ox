package engine

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/kkestell/ox/internal/agent"
)

// instructions is the system message of a request sent at now: Ur's role,
// how to reach the other sessions when Ur is Ox session oxSession, Ox's
// style, the environment, and the contents of workspace's AGENTS.md when it
// has one. An empty oxSession means Ur has no Ox tools.
func instructions(workspace, oxSession string, now time.Time) (string, error) {
	var b strings.Builder
	b.WriteString("You are Ur, a coding agent running inside Ox. The user talks to you through Ox. " +
		"You work in the user's workspace through the `shell` tool: " +
		"read, search, edit, build, and test with shell commands, and check your work by running it.\n\n")
	if oxSession != "" {
		b.WriteString("You are Ox session `" + oxSession + "`. " +
			"\"Another session\" or \"the other agent\" means another Ox session. " +
			"Find them with `list_sessions` and message them with `send_message`. " +
			"To hand work to another agent, start a child session with `create_session`.\n\n" +
			agent.Sessions + "\n\n")
	}
	b.WriteString(agent.Style + "\n\n")
	b.WriteString("Workspace: " + workspace + "\n")
	b.WriteString("OS: " + runtime.GOOS + "/" + runtime.GOARCH + "\n")
	b.WriteString("Shell: bash\n")
	b.WriteString("Date: " + now.Format("2006-01-02, Monday") + "\n\n")
	b.WriteString("A directory may hold an `AGENTS.md` with instructions for the files beneath it; " +
		"read it before changing files there.")
	agents, err := os.ReadFile(filepath.Join(workspace, "AGENTS.md"))
	if errors.Is(err, fs.ErrNotExist) {
		return b.String(), nil
	}
	if err != nil {
		return "", err
	}
	b.WriteString("\n\nInstructions from the workspace's `AGENTS.md`:\n\n")
	b.Write(agents)
	return b.String(), nil
}
