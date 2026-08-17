// Package credentials resolves the OpenRouter credential from the process
// environment or the OS keyring, in that order.
package credentials

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/zalando/go-keyring"
)

const (
	service                 = "ox"
	account                 = "openrouter"
	apiKeyVariable          = "OPENROUTER_API_KEY"
	keyringDisabledVariable = "OX_KEYRING_DISABLED"

	NoCredentialMessage = "no OpenRouter credential is configured: set " + apiKeyVariable +
		" or store a key in the OS keyring under service " + service + ", account " + account
	KeyringDisabledMessage = "cannot store an OpenRouter API key: " + keyringDisabledVariable +
		"=1 turns off keyring access"
)

// Source names the layer that supplied the resolved credential. Its values also
// read naturally in log messages.
type Source string

const (
	SourceNone        Source = "none"
	SourceEnvironment Source = "environment"
	SourceKeyring     Source = "keyring"
)

// Store caches the resolved credential and its source. Refresh, Set, and Clear
// are the only operations that consult or change the keyring, which on macOS
// costs a subprocess, so reads come from the cache.
type Store struct {
	logger          *slog.Logger
	environmentKey  string
	keyringDisabled bool

	mu     sync.RWMutex
	key    string
	source Source
}

func NewStore(apiKey string, keyringDisabled bool, logger *slog.Logger) *Store {
	store := &Store{
		logger:          logger,
		environmentKey:  strings.TrimSpace(apiKey),
		keyringDisabled: keyringDisabled,
	}
	store.Refresh()
	return store
}

func (s *Store) Key() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.key
}

func (s *Store) Source() Source {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.source
}

// KeyringDisabled reports whether Set and Clear are turned off, so a caller can
// refuse to collect a key it cannot keep.
func (s *Store) KeyringDisabled() bool {
	return s.keyringDisabled
}

func (s *Store) Refresh() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resolve()
}

func (s *Store) Set(key string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return errors.New("OpenRouter API key is empty")
	}
	if s.keyringDisabled {
		return errors.New(KeyringDisabledMessage)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := keyring.Set(service, account, key); err != nil {
		return fmt.Errorf("store OpenRouter API key in keyring: %w", err)
	}
	s.resolve()
	return nil
}

// Clear deletes the keyring entry. It refuses while the environment supplies
// the credential, because deleting the entry would not change which credential
// Ox is using.
func (s *Store) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.environmentKey != "" {
		return fmt.Errorf("%s supplies the credential and cannot be cleared", apiKeyVariable)
	}
	if !s.keyringDisabled {
		if err := keyring.Delete(service, account); err != nil && !errors.Is(err, keyring.ErrNotFound) {
			return fmt.Errorf("delete OpenRouter API key from keyring: %w", err)
		}
	}
	s.resolve()
	return nil
}

// resolve is the single place that defines credential precedence. The caller
// holds s.mu for writing.
func (s *Store) resolve() {
	if s.environmentKey != "" {
		s.key = s.environmentKey
		s.source = SourceEnvironment
		return
	}
	if s.keyringDisabled {
		s.key = ""
		s.source = SourceNone
		return
	}

	key, err := keyring.Get(service, account)
	key = strings.TrimSpace(key)
	switch {
	case err == nil && key != "":
		s.key = key
		s.source = SourceKeyring
	case err == nil, errors.Is(err, keyring.ErrNotFound):
		s.key = ""
		s.source = SourceNone
	default:
		// A keyring Ox cannot read is no credential rather than a dead process.
		// The error names the failure and never the key.
		s.key = ""
		s.source = SourceNone
		s.logger.Warn("reading the OpenRouter credential from the keyring failed", "error", err)
	}
}
