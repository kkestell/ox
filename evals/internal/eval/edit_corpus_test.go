package eval

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEditComparisonCorpus(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "..", "tasks", "edit-v1"))
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) < 30 {
		t.Fatalf("edit comparison tasks = %d, want at least 30", len(entries))
	}

	categoryMinimums := map[string]int{
		"repeated": 4,
		"stale":    4,
		"unicode":  4,
		"newline":  6,
		"insert":   3,
		"delete":   2,
		"disjoint": 2,
		"multi":    4,
		"failure":  3,
	}
	categories := make(map[string]int)
	seen := make(map[string]bool)
	wantBudget := Budget{TimeoutMS: 300000, ProviderRequests: 20}
	for _, entry := range entries {
		if !entry.IsDir() {
			t.Errorf("unexpected corpus file %s", entry.Name())
			continue
		}
		taskRoot := filepath.Join(root, entry.Name())
		task, err := LoadTask(taskRoot)
		if err != nil {
			t.Errorf("%s: %v", entry.Name(), err)
			continue
		}
		if task.ID != entry.Name() {
			t.Errorf("directory %s has task id %q", entry.Name(), task.ID)
		}
		if seen[task.ID] {
			t.Errorf("duplicate task id %q", task.ID)
		}
		seen[task.ID] = true
		if task.Budget != wantBudget {
			t.Errorf("%s budget = %+v, want %+v", task.ID, task.Budget, wantBudget)
		}
		for category := range categoryMinimums {
			if strings.HasPrefix(task.ID, category+"-") {
				categories[category]++
			}
		}

		promptCount := 0
		mutationCount := 0
		for index, phase := range task.Phases {
			switch phase.Action {
			case "prompt":
				promptCount++
				lower := strings.ToLower(phase.Prompt)
				for _, toolName := range []string{"edit_file", "read_file", "apply_patch"} {
					if strings.Contains(lower, toolName) {
						t.Errorf("%s phase %d prompt names tool %q", task.ID, index+1, toolName)
					}
				}
			case "mutate":
				mutationCount++
				if index == 0 || index == len(task.Phases)-1 {
					t.Errorf("%s mutation phase must be between prompts", task.ID)
				}
				if phase.Overlay == "" {
					t.Errorf("%s mutation phase has no overlay", task.ID)
					continue
				}
				mutationRoot := filepath.Join(taskRoot, phase.Overlay)
				info, err := os.Stat(mutationRoot)
				if err != nil || !info.IsDir() {
					t.Errorf("%s mutation overlay %q is not a directory", task.ID, phase.Overlay)
				}
				if strings.HasPrefix(filepath.Clean(mutationRoot), filepath.Join(taskRoot, "workspace")+string(filepath.Separator)) {
					t.Errorf("%s mutation overlay is inside the agent workspace fixture", task.ID)
				}
			}
		}
		if promptCount == 0 {
			t.Errorf("%s has no prompt phase", task.ID)
		}
		if strings.HasPrefix(task.ID, "stale-") && mutationCount == 0 {
			t.Errorf("%s does not exercise fixture mutation", task.ID)
		}
		if len(task.Success.Files) == 0 && len(task.Success.Command) == 0 {
			t.Errorf("%s has no objective verifier", task.ID)
		}
	}

	for category, minimum := range categoryMinimums {
		if categories[category] < minimum {
			t.Errorf("edit comparison corpus %s tasks = %d, want at least %d", category, categories[category], minimum)
		}
	}
}

func TestEditComparisonCorpusVerifiers(t *testing.T) {
	root := filepath.Join("..", "..", "tasks", "edit-v1")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		t.Run(entry.Name(), func(t *testing.T) {
			task, err := LoadTask(filepath.Join(root, entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			workspace := t.TempDir()
			for path, content := range task.Success.Files {
				target := filepath.Join(workspace, filepath.FromSlash(path))
				if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(target, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := verifyTask(context.Background(), task, workspace); err != nil {
				t.Fatalf("objective verifier rejected expected result: %v", err)
			}
		})
	}
}

func TestEditComparisonCorpusByteSensitiveExpectations(t *testing.T) {
	root := filepath.Join("..", "..", "tasks", "edit-v1")
	tests := []struct {
		taskID string
		path   string
		input  func(string) bool
		output func(string) bool
		label  string
	}{
		{"newline-lf-13", "unix.conf", func(value string) bool { return strings.Contains(value, "\n") && !strings.Contains(value, "\r\n") }, func(value string) bool { return strings.Contains(value, "\n") && !strings.Contains(value, "\r\n") }, "LF endings"},
		{"newline-crlf-14", "windows.ini", func(value string) bool {
			return strings.Contains(value, "\r\n") && !strings.Contains(strings.ReplaceAll(value, "\r\n", ""), "\n")
		}, func(value string) bool {
			return strings.Contains(value, "\r\n") && !strings.Contains(strings.ReplaceAll(value, "\r\n", ""), "\n")
		}, "CRLF endings"},
		{"newline-bom-15", "bom.txt", func(value string) bool { return strings.HasPrefix(value, "\ufeff") }, func(value string) bool { return strings.HasPrefix(value, "\ufeff") }, "UTF-8 BOM"},
		{"newline-trailing-preserve-16", "present.txt", func(value string) bool { return strings.HasSuffix(value, "\n") }, func(value string) bool { return strings.HasSuffix(value, "\n") }, "trailing newline"},
		{"newline-trailing-add-17", "absent.txt", func(value string) bool { return !strings.HasSuffix(value, "\n") }, func(value string) bool { return strings.HasSuffix(value, "\n") }, "added trailing newline"},
		{"newline-trailing-remove-18", "remove.txt", func(value string) bool { return strings.HasSuffix(value, "\n") }, func(value string) bool { return !strings.HasSuffix(value, "\n") }, "removed trailing newline"},
	}
	for _, test := range tests {
		t.Run(test.taskID, func(t *testing.T) {
			task, err := LoadTask(filepath.Join(root, test.taskID))
			if err != nil {
				t.Fatal(err)
			}
			input, err := os.ReadFile(filepath.Join(root, test.taskID, "workspace", test.path))
			if err != nil || !test.input(string(input)) {
				t.Fatalf("%s input does not require %s: %v", test.path, test.label, err)
			}
			value, ok := task.Success.Files[test.path]
			if !ok || !test.output(value) {
				t.Fatalf("%s expected output does not require %s", test.path, test.label)
			}
		})
	}
}
