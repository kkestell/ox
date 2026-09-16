package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kkestell/ox/internal/acp"
	"github.com/kkestell/ox/internal/settings"
)

func TestFileStorePersistsLocksAndRepairsTornTail(t *testing.T) {
	store, err := newFileStore(t.TempDir(), discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	id := "0123456789abcdef0123456789abcdef"
	created, err := newRecord(1, recordSessionCreated, sessionCreated{
		SessionID: id,
		CWD:       t.TempDir(),
		Configuration: requestConfiguration{
			Settings: settings.Resolved{Model: "test/model"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	log, err := store.create(id, created)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := store.open(id); err != errSessionLocked {
		t.Fatalf("second activation error = %v", err)
	}
	log.close()

	path, err := store.path(id)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(`{"version":1,"sequence":2`); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, records, repaired, err := store.open(id)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.close()
	if !repaired || len(records) != 1 {
		t.Fatalf("repaired = %v, records = %d", repaired, len(records))
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != int64(len(data)) || data[len(data)-1] != '\n' {
		t.Fatalf("repaired log = %q", data)
	}
}

func TestFileStoreRepairsTornConfigurationAndContinuesSequence(t *testing.T) {
	store, err := newFileStore(t.TempDir(), discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	id := "0123456789abcdef0123456789abcdef"
	configuration := requestConfiguration{
		Settings: settings.Resolved{Model: "test/model"},
	}
	created := mustRecord(t, 1, recordSessionCreated, sessionCreated{
		SessionID: id, CWD: t.TempDir(), Configuration: configuration,
	})
	log, err := store.create(id, created)
	if err != nil {
		t.Fatal(err)
	}
	user := mustRecord(t, 2, recordUserMessage, userMessageRecord{
		TurnID: "turn", MessageID: "user",
		Content: []acp.ContentBlock{{Type: "text", Text: "hello"}},
	})
	finished := mustRecord(t, 3, recordTurnFinished, turnFinishedRecord{
		TurnID: "turn", Kind: "cancelled",
	})
	changedRecord := mustRecord(t, 4, recordConfigChanged, configurationChanged{Configuration: configuration})
	for _, record := range []sessionRecord{user, finished, changedRecord} {
		if err := log.append(record); err != nil {
			t.Fatal(err)
		}
	}
	log.close()

	path, err := store.path(id)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(path, int64(len(data)-1)); err != nil {
		t.Fatal(err)
	}

	reopened, records, repaired, err := store.open(id)
	if err != nil {
		t.Fatal(err)
	}
	if !repaired || len(records) != 3 || records[2].Type != recordTurnFinished {
		t.Fatalf("repaired = %v, records = %#v", repaired, records)
	}
	restored := mustFold(t, records)
	if restored.openTurn != "" || restored.sequence != 3 {
		t.Fatalf("restored state = %#v", restored)
	}
	changed := configuration
	changed.Settings.Model = "test/next"
	next := mustRecord(t, restored.sequence+1, recordConfigChanged, configurationChanged{
		Configuration: changed,
	})
	if err := reopened.append(next); err != nil {
		t.Fatal(err)
	}
	reopened.close()

	final, records, repaired, err := store.open(id)
	if err != nil {
		t.Fatal(err)
	}
	defer final.close()
	if repaired || len(records) != 4 {
		t.Fatalf("second open repaired = %v, records = %d", repaired, len(records))
	}
	continued := mustFold(t, records)
	if continued.sequence != 4 || continued.configuration.Settings.Model != "test/next" {
		t.Fatalf("continued state = %#v", continued)
	}
}

func TestFileStoreRejectsTraversalAndInteriorCorruption(t *testing.T) {
	store, err := newFileStore(t.TempDir(), discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.path("../session"); err == nil {
		t.Fatal("traversal session ID was accepted")
	}
	// A damaged log is skipped rather than fatal: one unreadable file must not
	// hide every other session from the client.
	corrupt := filepath.Join(store.root, "0123456789abcdef0123456789abcdef.jsonl")
	if err := os.WriteFile(corrupt, []byte("{bad}\n{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	readable := writeListableSession(t, store, "fedcba9876543210fedcba9876543210", "readable")
	listed, err := store.list()
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].id != readable {
		t.Fatalf("listed = %#v, want only the readable session", listed)
	}
}

// writeListableSession creates a session with one prompt, which is what gives a
// listing its title, and closes its log so the store can read it back.
func writeListableSession(t *testing.T, store *fileStore, id, prompt string) string {
	t.Helper()
	created, err := newRecord(1, recordSessionCreated, sessionCreated{
		SessionID: id,
		CWD:       t.TempDir(),
		Configuration: requestConfiguration{
			Settings: settings.Resolved{Model: "test/model"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	log, err := store.create(id, created)
	if err != nil {
		t.Fatal(err)
	}
	message, err := newRecord(2, recordUserMessage, userMessageRecord{
		TurnID:    "turn",
		MessageID: "message",
		Content:   []acp.ContentBlock{{Type: "text", Text: prompt}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := log.append(message); err != nil {
		t.Fatal(err)
	}
	log.close()
	return id
}

func TestFileStoreDeletesOnlyInactiveSessions(t *testing.T) {
	store, err := newFileStore(t.TempDir(), discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	id := "0123456789abcdef0123456789abcdef"
	created, err := newRecord(1, recordSessionCreated, sessionCreated{
		SessionID: id,
		CWD:       t.TempDir(),
		Configuration: requestConfiguration{
			Settings: settings.Resolved{Model: "test/model"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	log, err := store.create(id, created)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.delete(id, nil); err != errSessionLocked {
		t.Fatalf("delete active session error = %v", err)
	}
	log.close()
	if err := store.delete(id, nil); err != nil {
		t.Fatal(err)
	}
	path, err := store.path(id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("deleted session stat error = %v", err)
	}
}

func TestFileStoreCanRetryOrphanedSpillDeletion(t *testing.T) {
	store, err := newFileStore(t.TempDir(), discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	id := "0123456789abcdef0123456789abcdef"
	spill := store.spillDir(id)
	if err := os.Mkdir(spill, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(spill, "result.out"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.delete(id, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(spill); !os.IsNotExist(err) {
		t.Fatalf("orphaned spill remains: %v", err)
	}
	if err := store.delete(id, nil); err == nil {
		t.Fatal("unknown session deletion became unconditionally idempotent")
	}
}

func TestFileStoreRunsCleanupBeforeDeletingSession(t *testing.T) {
	store, err := newFileStore(t.TempDir(), discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	id := "0123456789abcdef0123456789abcdef"
	workspace := t.TempDir()
	created, err := newRecord(1, recordSessionCreated, sessionCreated{
		SessionID: id,
		CWD:       workspace,
		Configuration: requestConfiguration{
			Settings: settings.Resolved{Model: "test/model"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	log, err := store.create(id, created)
	if err != nil {
		t.Fatal(err)
	}
	log.close()
	injected := errors.New("cleanup failed")
	if err := store.delete(id, func(state durableState) error {
		if state.cwd != workspace {
			t.Fatalf("cleanup cwd = %q", state.cwd)
		}
		return injected
	}); !errors.Is(err, injected) {
		t.Fatalf("delete error = %v", err)
	}
	path, err := store.path(id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("session removed after cleanup failure: %v", err)
	}
	if err := store.delete(id, func(state durableState) error { return nil }); err != nil {
		t.Fatal(err)
	}
}

// TestListSessionsPagesAtFiftyAndRejectsBadCursors covers the page boundary and
// every reason a cursor is refused, which is where a client either loses
// sessions or loops.
func TestListSessionsPagesAtFiftyAndRejectsBadCursors(t *testing.T) {
	list := func(t *testing.T, instance *Agent, request acp.ListSessionsRequest) acp.ListSessionsResponse {
		t.Helper()
		response, err := instance.ListSessions(context.Background(), request)
		if err != nil {
			t.Fatal(err)
		}
		return response
	}

	t.Run("a full page needs no cursor", func(t *testing.T) {
		instance := listTestAgent(t, 50)
		response := list(t, instance, acp.ListSessionsRequest{})
		if len(response.Sessions) != 50 || response.NextCursor != "" {
			t.Fatalf("sessions = %d, cursor = %q", len(response.Sessions), response.NextCursor)
		}
	})

	t.Run("one more session pages", func(t *testing.T) {
		instance := listTestAgent(t, 51)
		first := list(t, instance, acp.ListSessionsRequest{})
		if len(first.Sessions) != 50 || first.NextCursor == "" {
			t.Fatalf("first page = %d sessions, cursor = %q",
				len(first.Sessions), first.NextCursor)
		}
		second := list(t, instance, acp.ListSessionsRequest{Cursor: first.NextCursor})
		if len(second.Sessions) != 1 || second.NextCursor != "" {
			t.Fatalf("second page = %d sessions, cursor = %q",
				len(second.Sessions), second.NextCursor)
		}
		seen := make(map[string]struct{}, 51)
		for _, session := range append(first.Sessions, second.Sessions...) {
			if _, repeated := seen[session.SessionID]; repeated {
				t.Fatalf("session %s appeared on both pages", session.SessionID)
			}
			seen[session.SessionID] = struct{}{}
		}
		if len(seen) != 51 {
			t.Fatalf("paged over %d sessions, want 51", len(seen))
		}
	})

	t.Run("bad cursors are refused", func(t *testing.T) {
		instance := listTestAgent(t, 51)
		page := list(t, instance, acp.ListSessionsRequest{})
		stale := encodeCursor(listCursor{
			UpdatedAt: time.Unix(0, 0).UTC().Format(time.RFC3339Nano),
			SessionID: "0123456789abcdef0123456789abcdef",
		})
		for name, request := range map[string]acp.ListSessionsRequest{
			"stale":      {Cursor: stale},
			"mismatched": {Cursor: page.NextCursor, CWD: t.TempDir()},
			"malformed":  {Cursor: "not-a-cursor"},
		} {
			t.Run(name, func(t *testing.T) {
				if _, err := instance.ListSessions(
					context.Background(), request,
				); err == nil {
					t.Fatal("cursor was accepted")
				}
			})
		}
	})
}

func TestListSessionsMarksSessionsLockedByAnotherRuntime(t *testing.T) {
	directory := t.TempDir()
	owner, err := New(Config{Logger: discardLogger(), SessionDir: directory})
	if err != nil {
		t.Fatal(err)
	}
	listing, err := New(Config{Logger: discardLogger(), SessionDir: directory})
	if err != nil {
		t.Fatal(err)
	}
	id := writeListableSession(t, owner.store, "0123456789abcdef0123456789abcdef", "locked")
	log, _, _, err := owner.store.open(id)
	if err != nil {
		t.Fatal(err)
	}
	closed := false
	defer func() {
		if !closed {
			log.close()
		}
	}()

	response, err := listing.ListSessions(context.Background(), acp.ListSessionsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Sessions) != 1 || response.Sessions[0].Meta[acp.MetaSessionLocked] != true {
		t.Fatalf("listed sessions = %#v", response.Sessions)
	}

	owner.sessionsMu.Lock()
	owner.sessions[id] = &session{id: id, log: log}
	owner.sessionsMu.Unlock()
	response, err = owner.ListSessions(context.Background(), acp.ListSessionsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Sessions) != 1 || response.Sessions[0].Meta != nil {
		t.Fatalf("owner listed sessions = %#v", response.Sessions)
	}
	owner.sessionsMu.Lock()
	delete(owner.sessions, id)
	owner.sessionsMu.Unlock()
	log.close()
	closed = true
	response, err = listing.ListSessions(context.Background(), acp.ListSessionsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Sessions) != 1 || response.Sessions[0].Meta != nil {
		t.Fatalf("unlocked listed sessions = %#v", response.Sessions)
	}
}

func listTestAgent(t *testing.T, sessions int) *Agent {
	t.Helper()
	instance, err := New(Config{Logger: discardLogger(), SessionDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	for index := range sessions {
		writeListableSession(
			t,
			instance.store,
			fmt.Sprintf("%032x", index+1),
			fmt.Sprintf("prompt %d", index+1),
		)
	}
	return instance
}
