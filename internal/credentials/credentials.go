package credentials

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"

	"github.com/zalando/go-keyring"
)

const (
	service = "ox"
	account = "openrouter"

	KeyringDisabledMessage = "cannot store an OpenRouter API key: keyring access is disabled"
	NoCredentialMessage    = "no OpenRouter credential is configured: pass --credential-file or store a key with ox login"
)

var (
	ErrEmptyKey                = errors.New("OpenRouter API key is empty")
	ErrCredentialFileImmutable = errors.New("the credential supplied by --credential-file cannot be changed by login or logout")
)

type Source string

const (
	SourceNone           Source = "none"
	SourceCredentialFile Source = "credential_file"
	SourceKeyring        Source = "keyring"
)

type Store struct {
	logger *slog.Logger

	mu              sync.RWMutex
	key             string
	source          Source
	fileCredential  string
	keyringDisabled bool
}

// NewStore resolves a credential from immutable process inputs. A supplied
// credential file value takes precedence over the keyring.
func NewStore(logger *slog.Logger, fileCredential string, keyringDisabled bool) *Store {
	if logger == nil {
		logger = slog.Default()
	}
	store := &Store{
		logger:          logger,
		fileCredential:  strings.TrimSpace(fileCredential),
		keyringDisabled: keyringDisabled,
	}
	store.Refresh()
	return store
}

// LoadFile reads one credential from an owner-only regular file. The file is
// opened and its identity rechecked so a path replacement cannot make the
// validation describe a different file than the bytes that were read.
func LoadFile(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("credential file path must not be blank")
	}
	before, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("inspect credential file %s: %w", path, err)
	}
	if err := validateFile(before); err != nil {
		return "", fmt.Errorf("credential file %s: %w", path, err)
	}
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open credential file %s: %w", path, err)
	}
	after, statErr := file.Stat()
	if statErr == nil && !os.SameFile(before, after) {
		statErr = errors.New("file changed while it was being opened")
	}
	if statErr == nil {
		statErr = validateFile(after)
	}
	raw, readErr := io.ReadAll(file)
	closeErr := file.Close()
	if err := errors.Join(statErr, readErr, closeErr); err != nil {
		return "", fmt.Errorf("read credential file %s: %w", path, err)
	}
	key := strings.TrimSpace(string(raw))
	if key == "" {
		return "", fmt.Errorf("credential file %s is empty", path)
	}
	if strings.ContainsAny(key, "\r\n") {
		return "", fmt.Errorf("credential file %s must contain exactly one credential", path)
	}
	return key, nil
}

func validateFile(info os.FileInfo) error {
	if !info.Mode().IsRegular() {
		return errors.New("must be a regular file, not a directory, device, or symlink")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("must not be accessible by group or other users (mode is %04o)", info.Mode().Perm())
	}
	if info.Mode().Perm()&0o400 == 0 {
		return errors.New("must be readable by its owner")
	}
	if !ownedByCurrentUser(info) {
		return errors.New("must be owned by the current user")
	}
	return nil
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

func (s *Store) KeyringDisabled() bool { return s.keyringDisabled }

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
	if s.fileCredential != "" {
		return ErrCredentialFileImmutable
	}
	if s.keyringDisabled {
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
	if s.fileCredential != "" {
		return ErrCredentialFileImmutable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.keyringDisabled {
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
	if s.fileCredential != "" {
		s.key = s.fileCredential
		s.source = SourceCredentialFile
		s.logger.Info("credential resolved", "source", s.source)
		return
	}
	if s.keyringDisabled {
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
