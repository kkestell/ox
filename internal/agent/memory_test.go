package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kkestell/ox/internal/acp"
	"github.com/kkestell/ox/internal/settings"
)

const (
	testMemorySession = "0123456789abcdef0123456789abcdef"
	testMemoryOther   = "fedcba9876543210fedcba9876543210"
)

func TestMemoryStoreLifecycleAndRetrieval(t *testing.T) {
	directory := t.TempDir()
	workspace := memoryWorkspace(t)
	store, err := newMemoryStore(directory)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }

	first, err := store.write(workspace, testMemorySession, "preference", "Use compact Go code", "")
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	second, err := store.write(workspace, testMemoryOther, "decision", "Use JSON storage for workspace facts", "")
	if err != nil {
		t.Fatal(err)
	}
	results, err := store.search(workspace, "USE facts")
	if err != nil || len(results) != 1 || results[0].ID != second.ID {
		t.Fatalf("search = %#v, error = %v", results, err)
	}
	results, err = store.search(workspace, "")
	if err != nil || len(results) != 2 || results[0].ID != second.ID || results[1].ID != first.ID {
		t.Fatalf("list = %#v, error = %v", results, err)
	}

	now = first.ExpiresAt.Add(-time.Second)
	if results, err = store.search(workspace, "compact"); err != nil || len(results) != 1 {
		t.Fatalf("pre-expiry search = %#v, error = %v", results, err)
	}
	now = first.ExpiresAt.Add(time.Second)
	if results, err = store.search(workspace, "compact"); err != nil || len(results) != 0 {
		t.Fatalf("post-read expiry = %#v, error = %v", results, err)
	}

	now = second.CreatedAt.Add(time.Hour)
	replacement, err := store.write(
		workspace, testMemorySession, "decision", "Use one versioned JSON document", second.ID,
	)
	if err != nil || replacement.Supersedes != second.ID {
		t.Fatalf("replacement = %#v, error = %v", replacement, err)
	}
	if _, err := store.write(workspace, testMemorySession, "finding", "missing", second.ID); err == nil {
		t.Fatal("superseded fact remained active")
	}
	reopened, err := newMemoryStore(directory)
	if err != nil {
		t.Fatal(err)
	}
	reopened.now = store.now
	results, err = reopened.search(workspace, "versioned")
	if err != nil || len(results) != 1 || results[0].ID != replacement.ID {
		t.Fatalf("restarted search = %#v, error = %v", results, err)
	}
	if err := reopened.delete(workspace, replacement.ID); err != nil {
		t.Fatal(err)
	}
	if results, err = reopened.search(workspace, ""); err != nil || len(results) != 0 {
		t.Fatalf("after delete = %#v, error = %v", results, err)
	}
}

func TestMemoryStoreBoundsAndStableOrder(t *testing.T) {
	store, err := newMemoryStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	workspace := memoryWorkspace(t)
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }

	if _, err := store.write(
		workspace, testMemorySession, "finding", strings.Repeat("界", 682)+"ab", "",
	); err != nil {
		t.Fatalf("exact byte limit: %v", err)
	}
	if _, err := store.write(
		workspace, testMemorySession, "finding", strings.Repeat("界", 682)+"abx", "",
	); err == nil {
		t.Fatal("oversized UTF-8 fact succeeded")
	}
	for index := 1; index < 20; index++ {
		if _, err := store.write(
			workspace, testMemorySession, "finding", strings.Repeat("x", 700)+string(rune('a'+index)), "",
		); err != nil {
			t.Fatal(err)
		}
	}
	results, err := store.search(workspace, "")
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(results)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) > 10 || len(data) > maxMemoryResultBytes {
		t.Fatalf("retrieval bounds = %d facts, %d bytes", len(results), len(data))
	}
	for index := 1; index < len(results); index++ {
		if results[index-1].CreatedAt.Equal(results[index].CreatedAt) &&
			results[index-1].ID > results[index].ID {
			t.Fatalf("unstable ID order: %#v", results)
		}
	}

	document, err := store.load(workspace, memoryKey(workspace))
	if err != nil {
		t.Fatal(err)
	}
	document.Facts = document.Facts[:1]
	for len(document.Facts) < maxMemoryFacts {
		id := strings.Repeat("0", 24) + fmt.Sprintf("%08x", len(document.Facts))
		document.Facts = append(document.Facts, MemoryFact{
			ID: id, Type: "finding", Content: "capacity", SourceSession: testMemorySession,
			CreatedAt: now, ExpiresAt: now.Add(memoryRetention),
		})
	}
	if err := store.save(memoryKey(workspace), document); err != nil {
		t.Fatal(err)
	}
	if _, err := store.write(workspace, testMemorySession, "finding", "one too many", ""); err == nil {
		t.Fatal("capacity overflow succeeded")
	}
}

func TestMemoryStoreSourceOwnershipAndConcurrentWriters(t *testing.T) {
	directory := t.TempDir()
	workspace := memoryWorkspace(t)
	otherWorkspace := memoryWorkspace(t)
	first, err := newMemoryStore(directory)
	if err != nil {
		t.Fatal(err)
	}
	second, err := newMemoryStore(directory)
	if err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	for index := 0; index < 20; index++ {
		current := first
		if index%2 == 1 {
			current = second
		}
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			if _, err := current.write(
				workspace, testMemorySession, "finding", fmt.Sprintf("fact %d", index), "",
			); err != nil {
				t.Errorf("write %d: %v", index, err)
			}
		}(index)
	}
	wait.Wait()
	document, err := first.load(workspace, memoryKey(workspace))
	if err != nil || len(document.Facts) != 20 {
		t.Fatalf("concurrent document = %d facts, error = %v", len(document.Facts), err)
	}
	results, err := first.search(workspace, "fact")
	if err != nil || len(results) != 10 {
		t.Fatalf("concurrent search = %d facts, error = %v", len(results), err)
	}
	if results, err = first.search(otherWorkspace, ""); err != nil || len(results) != 0 {
		t.Fatalf("separate workspace = %#v, error = %v", results, err)
	}
	if _, err := first.write(otherWorkspace, testMemoryOther, "finding", "other", ""); err != nil {
		t.Fatal(err)
	}
	if err := first.deleteSource(workspace, testMemorySession); err != nil {
		t.Fatal(err)
	}
	if results, err = first.search(workspace, ""); err != nil || len(results) != 0 {
		t.Fatalf("source cleanup = %#v, error = %v", results, err)
	}
	if results, err = first.search(otherWorkspace, ""); err != nil || len(results) != 1 {
		t.Fatalf("other workspace changed = %#v, error = %v", results, err)
	}
}

func TestMemoryStoreRejectsBadDataAndPreservesAtomicState(t *testing.T) {
	directory := t.TempDir()
	workspace := memoryWorkspace(t)
	store, err := newMemoryStore(directory)
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.write(workspace, testMemorySession, "finding", "preserved", "")
	if err != nil {
		t.Fatal(err)
	}
	store.beforeRename = func() error { return errors.New("injected replacement failure") }
	if _, err := store.write(workspace, testMemorySession, "finding", "rejected", ""); err == nil {
		t.Fatal("injected replacement succeeded")
	}
	store.beforeRename = nil
	results, err := store.search(workspace, "")
	if err != nil || len(results) != 1 || results[0].ID != first.ID {
		t.Fatalf("state after failed replacement = %#v, error = %v", results, err)
	}

	path := filepath.Join(directory, memoryKey(workspace)+".json")
	for _, data := range []string{
		`{"version":2,"workspace":"` + workspace + `","facts":[]}`,
		`{"version":1,"workspace":"/wrong","facts":[]}`,
		`{"version":1,"workspace":"` + workspace + `","facts":[],"extra":true}`,
		`{bad}`,
	} {
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := store.search(workspace, ""); err == nil {
			t.Fatalf("bad document succeeded: %s", data)
		}
	}
}

func TestDeleteSessionRemovesOnlyItsWorkspaceMemories(t *testing.T) {
	sessionDirectory := t.TempDir()
	memoryDirectory := t.TempDir()
	workspace := memoryWorkspace(t)
	files, err := newFileStore(sessionDirectory)
	if err != nil {
		t.Fatal(err)
	}
	memories, err := newMemoryStore(memoryDirectory)
	if err != nil {
		t.Fatal(err)
	}
	record, err := newRecord(1, recordSessionCreated, sessionCreated{
		SessionID: testMemorySession,
		CWD:       workspace,
		Configuration: requestConfiguration{
			Settings: settings.Resolved{Model: "test/model"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	log, err := files.create(testMemorySession, record)
	if err != nil {
		t.Fatal(err)
	}
	log.close()
	if _, err := memories.write(
		workspace, testMemorySession, "finding", "remove with source", "",
	); err != nil {
		t.Fatal(err)
	}
	remaining, err := memories.write(
		workspace, testMemoryOther, "finding", "keep other source", "",
	)
	if err != nil {
		t.Fatal(err)
	}
	instance := &Agent{
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		store:    files,
		memory:   memories,
		sessions: make(map[string]*session),
	}
	if _, err := instance.DeleteSession(context.Background(), acp.DeleteSessionRequest{
		SessionID: testMemorySession,
	}); err != nil {
		t.Fatal(err)
	}
	results, err := memories.search(workspace, "")
	if err != nil || len(results) != 1 || results[0].ID != remaining.ID {
		t.Fatalf("remaining memories = %#v, error = %v", results, err)
	}
}

func memoryWorkspace(t *testing.T) string {
	t.Helper()
	workspace, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return workspace
}
