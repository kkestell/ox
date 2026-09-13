package eval

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/kkestell/ox/internal/workspace"
)

func TestVersionedTaskCorpus(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "tasks", "v1"))
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	var found []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		task, err := LoadTask(filepath.Join(root, entry.Name()))
		if err != nil {
			t.Errorf("%s: %v", entry.Name(), err)
			continue
		}
		if task.ID != entry.Name() {
			t.Errorf("directory %s has task id %q", entry.Name(), task.ID)
		}
		found = append(found, task.ID)
	}
	sort.Strings(found)
	want := []string{
		"cancellation", "edit", "long-context", "multi-file", "navigation",
		"noisy-validator", "permission-denial", "restart",
	}
	if len(found) != len(want) {
		t.Fatalf("tasks = %v, want %v", found, want)
	}
	for index := range want {
		if found[index] != want[index] {
			t.Fatalf("tasks = %v, want %v", found, want)
		}
	}
	noisy, err := LoadTask(filepath.Join(root, "noisy-validator"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := noisy.Success.Files["status.txt"]; !ok {
		t.Fatal("noisy-validator task does not require a status report")
	}
	assertNoisyFailingValidator(t, filepath.Join(root, "noisy-validator", "workspace"))
	payload, err := os.Stat(filepath.Join(root, "long-context", "workspace", "archive", "payload.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if payload.Size() < 32*1024 {
		t.Fatalf("long-context payload is only %d bytes", payload.Size())
	}
	denial, err := LoadTask(filepath.Join(root, "permission-denial"))
	if err != nil {
		t.Fatal(err)
	}
	if denial.Success.MinimumPermissionRejections < 1 {
		t.Fatal("permission-denial task does not require a rejected permission request")
	}
}

// assertNoisyFailingValidator proves the fixture discriminates: the validator
// must fail and print more than the inline preview keeps, so a model that wants
// less output has to truncate it and lose the validator's own status.
func assertNoisyFailingValidator(t *testing.T, workspaceDirectory string) {
	t.Helper()
	command := exec.Command("sh", "check.sh")
	command.Dir = workspaceDirectory
	output, err := command.CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() == 0 {
		t.Fatalf("validator error = %v, output = %q", err, output)
	}
	if lines := strings.Count(string(output), "\n"); lines <= workspace.InlineMaxLines {
		t.Fatalf("validator printed %d lines, want more than %d", lines, workspace.InlineMaxLines)
	}
}
