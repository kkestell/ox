package credentials

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"

	"github.com/zalando/go-keyring"
)

const (
	service         = "ox"
	account         = "openrouter"
	disabledEnvName = "OX_KEYRING_DISABLED"

	KeyringDisabledMessage = "cannot store an OpenRouter API key: " + disabledEnvName +
		"=1 turns off keyring access"
	NoCredentialMessage = "no OpenRouter credential is configured: set OPENROUTER_API_KEY " +
		"or store a key in the OS keyring under service ox, account openrouter"
)

var (
	ErrEmptyKey              = errors.New("OpenRouter API key is empty")
	ErrEnvironmentCredential = errors.New("OPENROUTER_API_KEY cannot be cleared by logout")
)

type Source string

const (
	SourceNone        Source = "none"
	SourceEnvironment Source = "environment"
	SourceKeyring     Source = "keyring"
)

type Store struct {
	logger *slog.Logger

	mu     sync.RWMutex
	key    string
	source Source
}

func NewStore(logger *slog.Logger) *Store {
	if logger == nil {
		logger = slog.Default()
	}
	store := &Store{logger: logger}
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

func (s *Store) KeyringDisabled() bool {
	return keyringDisabled()
}

func (s *Store) Refresh() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resolve()
}

func (s *Store) Set(key string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return ErrEmptyKey
	}
	if keyringDisabled() {
		s.logger.Warn("credential store skipped", "keyring_disabled", true)
		return errors.New(KeyringDisabledMessage)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := keyring.Set(service, account, key); err != nil {
		s.logger.Error("credential store failed", "operation", "set", "error", err)
		return fmt.Errorf("store OpenRouter API key in keyring: %w", err)
	}
	s.resolve()
	s.logger.Info("credential stored", "source", s.source)
	return nil
}

func (s *Store) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if key := strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY")); key != "" {
		s.key = key
		s.source = SourceEnvironment
		s.logger.Warn("credential clear failed", "source", s.source)
		return ErrEnvironmentCredential
	}
	if !keyringDisabled() {
		if err := keyring.Delete(service, account); err != nil && !errors.Is(err, keyring.ErrNotFound) {
			s.logger.Error("credential clear failed", "error", err)
			return fmt.Errorf("delete OpenRouter API key from keyring: %w", err)
		}
	}
	s.resolve()
	s.logger.Info("credential cleared", "source", s.source)
	return nil
}

func (s *Store) resolve() {
	if key := strings.TrimSpace(os.Getenv("OPENROUTER_API_KEY")); key != "" {
		s.key = key
		s.source = SourceEnvironment
		s.logger.Info("credential resolved", "source", s.source)
		return
	}
	if keyringDisabled() {
		s.key = ""
		s.source = SourceNone
		s.logger.Info("credential resolved", "source", s.source, "keyring_disabled", true)
		return
	}

	key, err := keyring.Get(service, account)
	switch {
	case err == nil && strings.TrimSpace(key) != "":
		s.key = strings.TrimSpace(key)
		s.source = SourceKeyring
	case err == nil, errors.Is(err, keyring.ErrNotFound):
		s.key = ""
		s.source = SourceNone
	default:
		s.key = ""
		s.source = SourceNone
		s.logger.Warn("credential keyring read failed", "error", err)
	}
	s.logger.Info("credential resolved", "source", s.source)
}

func keyringDisabled() bool {
	return os.Getenv(disabledEnvName) == "1"
}
