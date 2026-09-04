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

func TestStoreResolutionOrderAndCache(t *testing.T) {
	keyring.MockInit()
	t.Setenv(disabledEnvName, "")
	t.Setenv("OPENROUTER_API_KEY", "")
	if err := keyring.Set(service, account, "keyring-key"); err != nil {
		t.Fatal(err)
	}

	store := NewStore(discardLogger())
	if store.Key() != "keyring-key" || store.Source() != SourceKeyring {
		t.Fatalf("key = %q, source = %q", store.Key(), store.Source())
	}
	if err := keyring.Set(service, account, "changed"); err != nil {
		t.Fatal(err)
	}
	if store.Key() != "keyring-key" {
		t.Fatalf("uncached key = %q", store.Key())
	}

	t.Setenv("OPENROUTER_API_KEY", " environment-key ")
	store.Refresh()
	if store.Key() != "environment-key" || store.Source() != SourceEnvironment {
		t.Fatalf("key = %q, source = %q", store.Key(), store.Source())
	}
}

func TestStoreSetAndClear(t *testing.T) {
	keyring.MockInit()
	t.Setenv(disabledEnvName, "")
	t.Setenv("OPENROUTER_API_KEY", "")
	store := NewStore(discardLogger())

	if err := store.Set("  stored-key  "); err != nil {
		t.Fatal(err)
	}
	if store.Key() != "stored-key" || store.Source() != SourceKeyring {
		t.Fatalf("key = %q, source = %q", store.Key(), store.Source())
	}
	stored, err := keyring.Get(service, account)
	if err != nil || stored != "stored-key" {
		t.Fatalf("stored key = %q, %v", stored, err)
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

func TestStoreRejectsEmptyAndDisabledWrites(t *testing.T) {
	keyring.MockInit()
	t.Setenv("OPENROUTER_API_KEY", "")
	t.Setenv(disabledEnvName, "")
	store := NewStore(discardLogger())

	for _, key := range []string{"", "   "} {
		if err := store.Set(key); !errors.Is(err, ErrEmptyKey) {
			t.Fatalf("Set(%q) error = %v", key, err)
		}
	}
	if _, err := keyring.Get(service, account); !errors.Is(err, keyring.ErrNotFound) {
		t.Fatalf("empty key was stored: %v", err)
	}

	t.Setenv(disabledEnvName, "1")
	if err := store.Set("secret"); err == nil || !strings.Contains(err.Error(), disabledEnvName) {
		t.Fatalf("disabled Set error = %v", err)
	}
	store.Refresh()
	if store.Source() != SourceNone {
		t.Fatalf("disabled source = %q", store.Source())
	}
}

func TestStoreEnvironmentWinsAfterSet(t *testing.T) {
	keyring.MockInit()
	t.Setenv(disabledEnvName, "")
	t.Setenv("OPENROUTER_API_KEY", "environment-key")
	store := NewStore(discardLogger())

	if err := store.Set("keyring-key"); err != nil {
		t.Fatal(err)
	}
	if store.Key() != "environment-key" || store.Source() != SourceEnvironment {
		t.Fatalf("key = %q, source = %q", store.Key(), store.Source())
	}
	stored, err := keyring.Get(service, account)
	if err != nil || stored != "keyring-key" {
		t.Fatalf("stored key = %q, %v", stored, err)
	}
	if err := store.Clear(); !errors.Is(err, ErrEnvironmentCredential) {
		t.Fatalf("environment Clear error = %v", err)
	}
	stored, err = keyring.Get(service, account)
	if err != nil || stored != "keyring-key" {
		t.Fatalf("keyring changed after failed Clear: %q, %v", stored, err)
	}
}

func TestStoreKeyringErrorsDegradeReadsAndFailWritesWithoutLeakingKey(t *testing.T) {
	keyringError := errors.New("keyring unavailable")
	keyring.MockInitWithError(keyringError)
	t.Setenv(disabledEnvName, "")
	t.Setenv("OPENROUTER_API_KEY", "")
	var output lockedBuffer
	logger := slog.New(slog.NewTextHandler(&output, nil))

	store := NewStore(logger)
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

func TestStoreConcurrentReadsAndWrites(t *testing.T) {
	keyring.MockInit()
	t.Setenv(disabledEnvName, "")
	t.Setenv("OPENROUTER_API_KEY", "")
	store := NewStore(discardLogger())

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
