package agent

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"runtime"
	"strings"
	"time"
)

//go:embed prompt.md
var basePrompt string

const sharedToolProse = "File tools resolve relative paths against the workspace root and refuse paths " +
	"outside it. When a search result spills, use read_file with the reported spill " +
	"path and offset/limit to read it back. Read an existing file before overwriting it. " +
	"For edit_file, old_string must be exact and unique unless replace_all is set; after " +
	"a refused match, read the file again instead of guessing. Some tool calls require " +
	"the user's approval. Shell commands run through a fresh `sh -c` from the workspace " +
	"root with the user's full authority; `cd`, exports, and other state do not persist " +
	"between calls. Chain dependent commands with `&&` in one call and use parallel tool " +
	"calls for independent work. Prefer read_file, grep, and glob over shell cat, grep, " +
	"and find. Shell stdin is closed, so interactive commands fail rather than hang. Set " +
	"timeout for legitimately long work. Shell output combines stdout and stderr; when " +
	"it spills, use read_file on the reported path. A rejected call returns an error, " +
	"so do not simply retry it."

// composePrompt keeps instruction blocks in their canonical order. The result
// is frozen for the session activation; later instruction sources append after
// the environment block rather than interleaving with it.
func composePrompt(cwd string, now time.Time) string {
	return promptPrefix(strings.TrimSpace(basePrompt), cwd, now) + "\n\n" +
		sharedToolProse + "\n\n" +
		"Use task to delegate self-contained work when it helps. Give each subagent a " +
		"complete standalone prompt, do not duplicate its work, and partition file work " +
		"so concurrent subagents never touch the same file."
}

func composeSubagentPrompt(cwd string, now time.Time) string {
	const prose = "You are a subagent working on one self-contained task. You cannot see the " +
		"user or the delegating conversation, so rely only on the prompt you receive. Your " +
		"final message is the entire answer returned to the caller; make it complete and " +
		"self-contained. You cannot delegate further. Some calls still require the user's " +
		"approval; if one is rejected, do not simply retry it. Sibling subagents may be " +
		"running, so confine file work to the files your prompt names."
	return promptPrefix(prose, cwd, now) + "\n\n" + sharedToolProse
}

func promptPrefix(prose, cwd string, now time.Time) string {
	return prose + "\n\n<environment>\n" +
		"<workspace-root>" + cwd + "</workspace-root>\n" +
		"<platform>" + runtime.GOOS + "</platform>\n" +
		"<current-date>" + now.Format("2006-01-02") + "</current-date>\n" +
		"</environment>"
}

func promptDigest(prompt string) string {
	sum := sha256.Sum256([]byte(prompt))
	return hex.EncodeToString(sum[:6])
}
