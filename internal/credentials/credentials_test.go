package credentials

import (
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/zalando/go-keyring"
)

// Every test installs go-keyring's in-memory provider before touching the
// keyring, and that provider stays installed for the rest of the test binary,
// so no test in this package reaches the developer's real keyring.

func TestStoreResolutionOrderAndCache(t *testing.T) {
	keyring.MockInit()
	if err := keyring.Set(service, account, "  keyring-key  "); err != nil {
		t.Fatal(err)
	}

	store := NewStore(" \t", false, discardLogger())
	assertCredential(t, store, "keyring-key", SourceKeyring)

	if err := keyring.Set(service, account, "changed"); err != nil {
		t.Fatal(err)
	}
	assertCredential(t, store, "keyring-key", SourceKeyring)
	store.Refresh()
	assertCredential(t, store, "changed", SourceKeyring)

	environmentStore := NewStore("  environment-key  ", false, discardLogger())
	assertCredential(t, environmentStore, "environment-key", SourceEnvironment)
}

func TestStoreResolvesNoCredential(t *testing.T) {
	keyring.MockInit()

	store := NewStore("", false, discardLogger())
	assertCredential(t, store, "", SourceNone)

	if err := keyring.Set(service, account, " \t"); err != nil {
		t.Fatal(err)
	}
	store.Refresh()
	assertCredential(t, store, "", SourceNone)
}

func TestStoreSetAndClear(t *testing.T) {
	keyring.MockInit()
	store := NewStore("", false, discardLogger())

	if err := store.Set("  stored-key  "); err != nil {
		t.Fatal(err)
	}
	assertCredential(t, store, "stored-key", SourceKeyring)
	stored, err := keyring.Get(service, account)
	if err != nil || stored != "stored-key" {
		t.Fatalf("stored key = %q, %v", stored, err)
	}

	if err := store.Clear(); err != nil {
		t.Fatal(err)
	}
	assertCredential(t, store, "", SourceNone)
	if err := store.Clear(); err != nil {
		t.Fatalf("clear missing key: %v", err)
	}

	if err := store.Set("out-of-band"); err != nil {
		t.Fatal(err)
	}
	if err := keyring.Delete(service, account); err != nil {
		t.Fatal(err)
	}
	if err := store.Clear(); err != nil {
		t.Fatalf("clear key deleted out of band: %v", err)
	}
	assertCredential(t, store, "", SourceNone)
}

func TestStoreRejectsEmptySet(t *testing.T) {
	keyring.MockInit()
	store := NewStore("", false, discardLogger())

	for _, key := range []string{"", "   "} {
		if err := store.Set(key); err == nil || !strings.Contains(err.Error(), "empty") {
			t.Fatalf("Set(%q) error = %v", key, err)
		}
	}
	if _, err := keyring.Get(service, account); !errors.Is(err, keyring.ErrNotFound) {
		t.Fatalf("empty key was stored: %v", err)
	}
}

func TestStoreEnvironmentWinsAfterSetAndPreventsClear(t *testing.T) {
	keyring.MockInit()
	store := NewStore(" environment-key ", false, discardLogger())

	if err := store.Set("keyring-key"); err != nil {
		t.Fatal(err)
	}
	assertCredential(t, store, "environment-key", SourceEnvironment)
	stored, err := keyring.Get(service, account)
	if err != nil || stored != "keyring-key" {
		t.Fatalf("stored key = %q, %v", stored, err)
	}

	if err := store.Clear(); err == nil || !strings.Contains(err.Error(), apiKeyVariable) {
		t.Fatalf("environment Clear error = %v", err)
	}
	stored, err = keyring.Get(service, account)
	if err != nil || stored != "keyring-key" {
		t.Fatalf("keyring changed after failed Clear: %q, %v", stored, err)
	}
	assertCredential(t, store, "environment-key", SourceEnvironment)
}

func TestStoreWithKeyringDisabled(t *testing.T) {
	keyring.MockInitWithError(errors.New("keyring must not be touched"))

	store := NewStore("", true, discardLogger())
	assertCredential(t, store, "", SourceNone)
	if err := store.Set("secret"); err == nil || !strings.Contains(err.Error(), keyringDisabledVariable) {
		t.Fatalf("disabled Set error = %v", err)
	}
	if err := store.Clear(); err != nil {
		t.Fatalf("disabled Clear error = %v", err)
	}

	environmentStore := NewStore(" environment-key ", true, discardLogger())
	assertCredential(t, environmentStore, "environment-key", SourceEnvironment)
}

func TestStoreKeyringErrorsDegradeReadsAndFailWritesWithoutLeakingKey(t *testing.T) {
	keyringError := errors.New("keyring unavailable")
	keyring.MockInitWithError(keyringError)
	var output lockedBuffer
	logger := slog.New(slog.NewTextHandler(&output, nil))

	store := NewStore("", false, logger)
	assertCredential(t, store, "", SourceNone)
	if err := store.Set("never-log-this"); !errors.Is(err, keyringError) {
		t.Fatalf("Set error = %v", err)
	}
	if err := store.Clear(); !errors.Is(err, keyringError) {
		t.Fatalf("Clear error = %v", err)
	}
	if !strings.Contains(output.String(), keyringError.Error()) {
		t.Fatalf("log did not report the keyring failure: %s", output.String())
	}
	if strings.Contains(output.String(), "never-log-this") {
		t.Fatalf("log leaked key: %s", output.String())
	}
}

func TestStoreRefreshDiscardsCachedKeyAfterKeyringError(t *testing.T) {
	keyring.MockInit()
	if err := keyring.Set(service, account, "cached"); err != nil {
		t.Fatal(err)
	}
	store := NewStore("", false, discardLogger())
	assertCredential(t, store, "cached", SourceKeyring)

	keyring.MockInitWithError(errors.New("keyring unavailable"))
	store.Refresh()
	assertCredential(t, store, "", SourceNone)
}

func TestStoreConcurrentReadsAndWrites(t *testing.T) {
	keyring.MockInit()
	store := NewStore("", false, discardLogger())

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

func assertCredential(t *testing.T, store *Store, wantKey string, wantSource Source) {
	t.Helper()
	if store.Key() != wantKey || store.Source() != wantSource {
		t.Fatalf("key = %q, source = %q, want key = %q, source = %q",
			store.Key(), store.Source(), wantKey, wantSource)
	}
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
