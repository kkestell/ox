package eval

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
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
	want := []string{"cancellation", "edit", "long-context", "multi-file", "navigation", "permission-denial", "restart"}
	if len(found) != len(want) {
		t.Fatalf("tasks = %v, want %v", found, want)
	}
	for index := range want {
		if found[index] != want[index] {
			t.Fatalf("tasks = %v, want %v", found, want)
		}
	}
	payload, err := os.Stat(filepath.Join(root, "long-context", "workspace", "archive", "payload.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if payload.Size() < 32*1024 {
		t.Fatalf("long-context payload is only %d bytes", payload.Size())
	}
}
