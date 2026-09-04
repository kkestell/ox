package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kkestell/ox/internal/acp"
	"github.com/kkestell/ox/internal/settings"
)

func TestFileStorePersistsLocksAndRepairsTornTail(t *testing.T) {
	store, err := newFileStore(t.TempDir())
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

func TestFileStoreRepairsTornCheckpointAndContinuesSequence(t *testing.T) {
	store, err := newFileStore(t.TempDir())
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
	state := mustFold(t, []sessionRecord{created, user, finished})
	checkpoint, err := newCheckpointRecord(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := log.append(user, finished, checkpoint); err != nil {
		t.Fatal(err)
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
	changed := cloneConfiguration(configuration)
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
	store, err := newFileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.path("../session"); err == nil {
		t.Fatal("traversal session ID was accepted")
	}
	path := filepath.Join(store.root, "0123456789abcdef0123456789abcdef.jsonl")
	if err := os.WriteFile(path, []byte("{bad}\n{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.list(); err == nil {
		t.Fatal("interior corruption was ignored")
	}
}

func TestFileStoreDeletesOnlyInactiveSessions(t *testing.T) {
	store, err := newFileStore(t.TempDir())
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
	if err := store.delete(id); err != errSessionLocked {
		t.Fatalf("delete active session error = %v", err)
	}
	log.close()
	if err := store.delete(id); err != nil {
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
	store, err := newFileStore(t.TempDir())
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
	if err := store.delete(id); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(spill); !os.IsNotExist(err) {
		t.Fatalf("orphaned spill remains: %v", err)
	}
	if err := store.delete(id); err == nil {
		t.Fatal("unknown session deletion became unconditionally idempotent")
	}
}
