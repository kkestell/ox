package credentials

import (
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/zalando/go-keyring"
)

func TestStoreResolutionOrderAndCache(t *testing.T) {
	keyring.MockInit()
	if err := keyring.Set(service, account, "keyring-key"); err != nil {
		t.Fatal(err)
	}

	store := NewStore(discardLogger(), "", false)
	if store.Key() != "keyring-key" || store.Source() != SourceKeyring {
		t.Fatalf("key = %q, source = %q", store.Key(), store.Source())
	}
	if err := keyring.Set(service, account, "changed"); err != nil {
		t.Fatal(err)
	}
	if store.Key() != "keyring-key" {
		t.Fatalf("uncached key = %q", store.Key())
	}
	store.Refresh()
	if store.Key() != "changed" {
		t.Fatalf("refreshed key = %q", store.Key())
	}

	fileStore := NewStore(discardLogger(), " file-key ", false)
	if fileStore.Key() != "file-key" || fileStore.Source() != SourceCredentialFile {
		t.Fatalf("file key = %q, source = %q", fileStore.Key(), fileStore.Source())
	}
}

func TestStoreSetAndClear(t *testing.T) {
	keyring.MockInit()
	store := NewStore(discardLogger(), "", false)

	if err := store.Set("  stored-key  "); err != nil {
		t.Fatal(err)
	}
	if store.Key() != "stored-key" || store.Source() != SourceKeyring {
		t.Fatalf("key = %q, source = %q", store.Key(), store.Source())
	}
	if err := store.Clear(); err != nil {
		t.Fatal(err)
	}
	if store.Key() != "" || store.Source() != SourceNone {
		t.Fatalf("key = %q, source = %q", store.Key(), store.Source())
	}
	if err := store.Clear(); err != nil {
		t.Fatalf("clear missing key: %v", err)
	}
}

func TestStoreRejectsUnavailableMutations(t *testing.T) {
	keyring.MockInit()
	store := NewStore(discardLogger(), "", false)
	for _, key := range []string{"", "   "} {
		if err := store.Set(key); !errors.Is(err, ErrEmptyKey) {
			t.Fatalf("Set(%q) error = %v", key, err)
		}
	}

	disabled := NewStore(discardLogger(), "", true)
	if err := disabled.Set("secret"); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("disabled Set error = %v", err)
	}
	if err := disabled.Clear(); err != nil {
		t.Fatalf("disabled Clear error = %v", err)
	}

	fromFile := NewStore(discardLogger(), "file-key", false)
	if err := fromFile.Set("replacement"); !errors.Is(err, ErrCredentialFileImmutable) {
		t.Fatalf("file Set error = %v", err)
	}
	if err := fromFile.Clear(); !errors.Is(err, ErrCredentialFileImmutable) {
		t.Fatalf("file Clear error = %v", err)
	}
	if fromFile.Key() != "file-key" {
		t.Fatalf("file key changed to %q", fromFile.Key())
	}
}

func TestStoreKeyringErrorsDegradeReadsAndFailWritesWithoutLeakingKey(t *testing.T) {
	keyringError := errors.New("keyring unavailable")
	keyring.MockInitWithError(keyringError)
	var output lockedBuffer
	logger := slog.New(slog.NewTextHandler(&output, nil))

	store := NewStore(logger, "", false)
	if store.Key() != "" || store.Source() != SourceNone {
		t.Fatalf("key = %q, source = %q", store.Key(), store.Source())
	}
	if err := store.Set("never-log-this"); !errors.Is(err, keyringError) {
		t.Fatalf("Set error = %v", err)
	}
	if strings.Contains(output.String(), "never-log-this") {
		t.Fatalf("log leaked key: %s", output.String())
	}
}

func TestLoadFile(t *testing.T) {
	directory := t.TempDir()
	valid := filepath.Join(directory, "credential")
	if err := os.WriteFile(valid, []byte("  secret-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	key, err := LoadFile(valid)
	if err != nil || key != "secret-key" {
		t.Fatalf("LoadFile = %q, %v", key, err)
	}

	bad := []struct {
		name string
		path string
		want string
	}{
		{name: "missing", path: filepath.Join(directory, "missing"), want: "inspect"},
		{name: "directory", path: directory, want: "regular file"},
	}
	for _, test := range bad {
		t.Run(test.name, func(t *testing.T) {
			if _, err := LoadFile(test.path); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("LoadFile error = %v", err)
			}
		})
	}

	for name, body := range map[string]string{
		"blank":      " \n",
		"multi-line": "first\nsecond\n",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(directory, name)
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadFile(path); err == nil {
				t.Fatal("invalid credential file was accepted")
			}
		})
	}

	permissive := filepath.Join(directory, "permissive")
	if err := os.WriteFile(permissive, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFile(permissive); err == nil || !strings.Contains(err.Error(), "group or other") {
		t.Fatalf("permissive error = %v", err)
	}
	unreadable := filepath.Join(directory, "unreadable")
	if err := os.WriteFile(unreadable, []byte("secret"), 0o200); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFile(unreadable); err == nil || !strings.Contains(err.Error(), "readable") {
		t.Fatalf("unreadable error = %v", err)
	}
	link := filepath.Join(directory, "link")
	if err := os.Symlink(valid, link); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFile(link); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("symlink error = %v", err)
	}
}

func TestStoreConcurrentReadsAndWrites(t *testing.T) {
	keyring.MockInit()
	store := NewStore(discardLogger(), "", false)

	var wait sync.WaitGroup
	for range 20 {
		wait.Add(2)
		go func() {
			defer wait.Done()
			_ = store.Key()
			_ = store.Source()
		}()
		go func() {
			defer wait.Done()
			if err := store.Set("key"); err != nil {
				t.Error(err)
			}
		}()
	}
	wait.Wait()
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

type lockedBuffer struct {
	mu     sync.Mutex
	buffer strings.Builder
}

func (b *lockedBuffer) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(data)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}
