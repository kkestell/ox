package agent

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	memoryVersion        = 1
	maxMemoryFacts       = 256
	maxMemoryFactBytes   = 2 << 10
	maxMemoryResultBytes = 8 << 10
	maxMemoryStoreBytes  = 1 << 20
	memoryRetention      = 30 * 24 * time.Hour
)

// MemoryFact is the model-visible representation of one active workspace fact.
type MemoryFact struct {
	ID            string    `json:"id"`
	Type          string    `json:"type"`
	Content       string    `json:"content"`
	SourceSession string    `json:"source_session"`
	CreatedAt     time.Time `json:"created_at"`
	ExpiresAt     time.Time `json:"expires_at"`
	Supersedes    string    `json:"supersedes,omitempty"`
}

type memoryDocument struct {
	Version   int          `json:"version"`
	Workspace string       `json:"workspace"`
	Facts     []MemoryFact `json:"facts"`
}

type memoryStore struct {
	root         string
	now          func() time.Time
	mu           sync.Mutex
	beforeRename func() error
}

func MemoryPath(xdgDataHome, home string) string {
	if xdgDataHome != "" {
		return filepath.Join(xdgDataHome, "ox", "memory")
	}
	return filepath.Join(home, ".local", "share", "ox", "memory")
}

func newMemoryStore(root string) (*memoryStore, error) {
	if root == "" {
		var err error
		root, err = os.MkdirTemp("", "ox-memory-")
		if err != nil {
			return nil, fmt.Errorf("create temporary memory store: %w", err)
		}
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create memory store: %w", err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		return nil, fmt.Errorf("secure memory store: %w", err)
	}
	return &memoryStore{root: root, now: time.Now}, nil
}

func (s *memoryStore) search(workspace, query string) ([]MemoryFact, error) {
	var result []MemoryFact
	err := s.withDocument(workspace, true, func(document *memoryDocument) (bool, error) {
		terms := strings.Fields(strings.ToLower(query))
		for _, fact := range document.Facts {
			content := strings.ToLower(fact.Content)
			matched := true
			for _, term := range terms {
				if !strings.Contains(content, term) {
					matched = false
					break
				}
			}
			if matched {
				result = append(result, fact)
			}
		}
		sort.Slice(result, func(i, j int) bool {
			if result[i].CreatedAt.Equal(result[j].CreatedAt) {
				return result[i].ID < result[j].ID
			}
			return result[i].CreatedAt.After(result[j].CreatedAt)
		})
		if len(result) > 10 {
			result = result[:10]
		}
		for len(result) > 0 {
			data, err := json.Marshal(result)
			if err != nil {
				return false, err
			}
			if len(data) <= maxMemoryResultBytes {
				break
			}
			result = result[:len(result)-1]
		}
		return false, nil
	})
	return result, err
}

func (s *memoryStore) write(
	workspace, sourceSession, factType, content, supersedes string,
) (MemoryFact, error) {
	if err := validateMemoryType(factType); err != nil {
		return MemoryFact{}, err
	}
	if !utf8.ValidString(content) || strings.TrimSpace(content) == "" {
		return MemoryFact{}, errors.New("memory content must be nonempty UTF-8 text")
	}
	if len(content) > maxMemoryFactBytes {
		return MemoryFact{}, fmt.Errorf(
			"memory content is %d bytes; maximum is %d", len(content), maxMemoryFactBytes,
		)
	}
	if !validSessionID(sourceSession) {
		return MemoryFact{}, errors.New("invalid source session")
	}
	if supersedes != "" && !validSessionID(supersedes) {
		return MemoryFact{}, errors.New("invalid superseded memory ID")
	}
	id, err := randomID()
	if err != nil {
		return MemoryFact{}, fmt.Errorf("generate memory ID: %w", err)
	}
	var result MemoryFact
	err = s.withDocument(workspace, true, func(document *memoryDocument) (bool, error) {
		if supersedes != "" {
			index := -1
			for current := range document.Facts {
				if document.Facts[current].ID == supersedes {
					index = current
					break
				}
			}
			if index < 0 {
				return false, fmt.Errorf("active memory %q does not exist", supersedes)
			}
			document.Facts = append(document.Facts[:index], document.Facts[index+1:]...)
		}
		if len(document.Facts) >= maxMemoryFacts {
			return false, fmt.Errorf("workspace memory has reached its %d-fact limit", maxMemoryFacts)
		}
		now := s.now().UTC()
		result = MemoryFact{
			ID: id, Type: factType, Content: content, SourceSession: sourceSession,
			CreatedAt: now, ExpiresAt: now.Add(memoryRetention), Supersedes: supersedes,
		}
		document.Facts = append(document.Facts, result)
		return true, nil
	})
	return result, err
}

func (s *memoryStore) delete(workspace, id string) error {
	if !validSessionID(id) {
		return errors.New("invalid memory ID")
	}
	return s.withDocument(workspace, true, func(document *memoryDocument) (bool, error) {
		for index := range document.Facts {
			if document.Facts[index].ID == id {
				document.Facts = append(document.Facts[:index], document.Facts[index+1:]...)
				return true, nil
			}
		}
		return false, fmt.Errorf("active memory %q does not exist", id)
	})
}

func (s *memoryStore) deleteSource(workspace, sessionID string) error {
	return s.withDocument(workspace, true, func(document *memoryDocument) (bool, error) {
		kept := document.Facts[:0]
		for _, fact := range document.Facts {
			if fact.SourceSession != sessionID {
				kept = append(kept, fact)
			}
		}
		changed := len(kept) != len(document.Facts)
		document.Facts = kept
		return changed, nil
	})
}

func (s *memoryStore) withDocument(
	workspace string,
	allowWrite bool,
	operation func(*memoryDocument) (bool, error),
) (err error) {
	if workspace == "" || !filepath.IsAbs(workspace) {
		return errors.New("memory workspace must be canonical and absolute")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	key := memoryKey(workspace)
	lockFile, err := os.OpenFile(filepath.Join(s.root, key+".lock"), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("open workspace memory lock: %w", err)
	}
	defer func() { err = errors.Join(err, lockFile.Close()) }()
	if err := lock(lockFile); err != nil {
		return fmt.Errorf("lock workspace memory: %w", err)
	}
	defer unlock(lockFile)

	document, err := s.load(workspace, key)
	if err != nil {
		return err
	}
	expired := document.Facts[:0]
	now := s.now().UTC()
	for _, fact := range document.Facts {
		if fact.ExpiresAt.After(now) {
			expired = append(expired, fact)
		}
	}
	changed := len(expired) != len(document.Facts)
	document.Facts = expired
	operationChanged, err := operation(&document)
	if err != nil {
		return err
	}
	if (changed || operationChanged) && allowWrite {
		return s.save(key, document)
	}
	return nil
}

func (s *memoryStore) load(workspace, key string) (memoryDocument, error) {
	path := filepath.Join(s.root, key+".json")
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return memoryDocument{Version: memoryVersion, Workspace: workspace}, nil
	}
	if err != nil {
		return memoryDocument{}, fmt.Errorf("open workspace memory: %w", err)
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return memoryDocument{}, fmt.Errorf("inspect workspace memory: %w", err)
	}
	if !info.Mode().IsRegular() {
		return memoryDocument{}, errors.New("workspace memory is not a regular file")
	}
	if info.Size() > maxMemoryStoreBytes {
		return memoryDocument{}, fmt.Errorf("workspace memory exceeds %d bytes", maxMemoryStoreBytes)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxMemoryStoreBytes+1))
	if err != nil {
		return memoryDocument{}, fmt.Errorf("read workspace memory: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var document memoryDocument
	if err := decoder.Decode(&document); err != nil {
		return memoryDocument{}, fmt.Errorf("decode workspace memory: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return memoryDocument{}, errors.New("workspace memory must contain one JSON document")
	}
	if document.Version != memoryVersion {
		return memoryDocument{}, fmt.Errorf("unsupported workspace memory version %d", document.Version)
	}
	if document.Workspace != workspace {
		return memoryDocument{}, errors.New("workspace memory root does not match")
	}
	if len(document.Facts) > maxMemoryFacts {
		return memoryDocument{}, fmt.Errorf("workspace memory has more than %d facts", maxMemoryFacts)
	}
	seen := make(map[string]struct{}, len(document.Facts))
	for index, fact := range document.Facts {
		if err := validateMemoryFact(fact); err != nil {
			return memoryDocument{}, fmt.Errorf("workspace memory fact %d: %w", index+1, err)
		}
		if _, ok := seen[fact.ID]; ok {
			return memoryDocument{}, fmt.Errorf("workspace memory has duplicate ID %q", fact.ID)
		}
		seen[fact.ID] = struct{}{}
	}
	return document, nil
}

func (s *memoryStore) save(key string, document memoryDocument) (err error) {
	data, err := json.Marshal(document)
	if err != nil {
		return fmt.Errorf("encode workspace memory: %w", err)
	}
	data = append(data, '\n')
	if len(data) > maxMemoryStoreBytes {
		return fmt.Errorf("workspace memory exceeds %d bytes", maxMemoryStoreBytes)
	}
	temporary, err := os.CreateTemp(s.root, key+".tmp-")
	if err != nil {
		return fmt.Errorf("create workspace memory replacement: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return fmt.Errorf("secure workspace memory replacement: %w", err)
	}
	if _, err := temporary.Write(data); err != nil {
		return fmt.Errorf("write workspace memory replacement: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("sync workspace memory replacement: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close workspace memory replacement: %w", err)
	}
	if s.beforeRename != nil {
		if err := s.beforeRename(); err != nil {
			return err
		}
	}
	if err := os.Rename(temporaryPath, filepath.Join(s.root, key+".json")); err != nil {
		return fmt.Errorf("replace workspace memory: %w", err)
	}
	if err := syncDirectory(s.root); err != nil {
		return fmt.Errorf("sync workspace memory: %w", err)
	}
	return nil
}

func memoryKey(workspace string) string {
	digest := sha256.Sum256([]byte(workspace))
	return hex.EncodeToString(digest[:])
}

func validateMemoryType(value string) error {
	switch value {
	case "preference", "decision", "finding":
		return nil
	default:
		return fmt.Errorf("invalid memory type %q", value)
	}
}

func validateMemoryFact(fact MemoryFact) error {
	if !validSessionID(fact.ID) {
		return errors.New("invalid ID")
	}
	if err := validateMemoryType(fact.Type); err != nil {
		return err
	}
	if !utf8.ValidString(fact.Content) || strings.TrimSpace(fact.Content) == "" ||
		len(fact.Content) > maxMemoryFactBytes {
		return errors.New("invalid content")
	}
	if !validSessionID(fact.SourceSession) {
		return errors.New("invalid source session")
	}
	if fact.CreatedAt.IsZero() || fact.ExpiresAt.IsZero() ||
		!fact.ExpiresAt.Equal(fact.CreatedAt.Add(memoryRetention)) {
		return errors.New("invalid retention")
	}
	if fact.Supersedes != "" && !validSessionID(fact.Supersedes) {
		return errors.New("invalid superseded memory ID")
	}
	return nil
}
