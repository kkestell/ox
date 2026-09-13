package agent

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	maxSessionBytes = 64 << 20
	maxRecordBytes  = 8 << 20
)

var errSessionLocked = errors.New("session is active in another runtime")

type fileStore struct {
	root   string
	logger *slog.Logger
}

type sessionLog struct {
	file *os.File
	// sinceCheckpoint counts the bytes appended since the log's last checkpoint,
	// which is what a load would have to fold if no newer checkpoint is written.
	sinceCheckpoint int
}

func SessionPath(xdgDataHome, home string) string {
	if xdgDataHome != "" {
		return filepath.Join(xdgDataHome, "ox", "sessions")
	}
	return filepath.Join(home, ".local", "share", "ox", "sessions")
}

func newFileStore(root string, logger *slog.Logger) (*fileStore, error) {
	if root == "" {
		var err error
		root, err = os.MkdirTemp("", "ox-sessions-")
		if err != nil {
			return nil, fmt.Errorf("create temporary session store: %w", err)
		}
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create session store: %w", err)
	}
	if err := os.Chmod(root, 0o700); err != nil {
		return nil, fmt.Errorf("secure session store: %w", err)
	}
	return &fileStore{root: root, logger: logger}, nil
}

func (s *fileStore) create(id string, record sessionRecord) (*sessionLog, error) {
	path, err := s.path(id)
	if err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create session log: %w", err)
	}
	log := &sessionLog{file: file}
	if err := tryLock(file); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return nil, err
	}
	if err := log.append(record); err != nil {
		log.close()
		_ = os.Remove(path)
		return nil, err
	}
	if err := syncDirectory(s.root); err != nil {
		log.close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("sync session store: %w", err)
	}
	return log, nil
}

func (s *fileStore) open(id string) (*sessionLog, []sessionRecord, bool, error) {
	path, err := s.path(id)
	if err != nil {
		return nil, nil, false, err
	}
	file, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		return nil, nil, false, fmt.Errorf("open session log: %w", err)
	}
	if err := tryLock(file); err != nil {
		_ = file.Close()
		return nil, nil, false, err
	}
	records, truncateAt, sinceCheckpoint, err := readRecords(file)
	if err != nil {
		unlock(file)
		_ = file.Close()
		return nil, nil, false, err
	}
	repaired := truncateAt >= 0
	if repaired {
		if err := file.Truncate(truncateAt); err != nil {
			unlock(file)
			_ = file.Close()
			return nil, nil, false, fmt.Errorf("repair torn session tail: %w", err)
		}
		if err := file.Sync(); err != nil {
			unlock(file)
			_ = file.Close()
			return nil, nil, false, fmt.Errorf("sync repaired session log: %w", err)
		}
	}
	if _, err := file.Seek(0, io.SeekEnd); err != nil {
		unlock(file)
		_ = file.Close()
		return nil, nil, false, fmt.Errorf("seek session log: %w", err)
	}
	return &sessionLog{file: file, sinceCheckpoint: sinceCheckpoint}, records, repaired, nil
}

// list projects every session in the store, newest first. A session whose log
// cannot be read or projected is skipped and logged: one damaged file must not
// hide every other session from the client.
func (s *fileStore) list() ([]sessionListEntry, error) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return nil, fmt.Errorf("list session store: %w", err)
	}
	listed := make([]sessionListEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".jsonl" {
			continue
		}
		id := strings.TrimSuffix(entry.Name(), ".jsonl")
		if !validSessionID(id) {
			s.skip(entry.Name(), errors.New("invalid session filename"))
			continue
		}
		projected, err := s.project(entry.Name())
		if err != nil {
			s.skip(entry.Name(), err)
			continue
		}
		if projected.id != id {
			s.skip(entry.Name(), errors.New("session ID does not match its filename"))
			continue
		}
		listed = append(listed, projected)
	}
	sort.Slice(listed, func(i, j int) bool {
		if listed[i].updatedAt.Equal(listed[j].updatedAt) {
			return listed[i].id > listed[j].id
		}
		return listed[i].updatedAt.After(listed[j].updatedAt)
	})
	return listed, nil
}

func (s *fileStore) project(name string) (sessionListEntry, error) {
	file, err := os.Open(filepath.Join(s.root, name))
	if err != nil {
		return sessionListEntry{}, err
	}
	records, _, _, readErr := readRecords(file)
	closeErr := file.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return sessionListEntry{}, err
	}
	return foldListProjection(records)
}

func (s *fileStore) skip(name string, err error) {
	if s.logger == nil {
		return
	}
	s.logger.Warn("skipping unlistable session", "file", name, "error", err)
}

func (s *fileStore) delete(id string, before func(durableState) error) (err error) {
	path, err := s.path(id)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		if os.IsNotExist(err) {
			spill := s.spillDir(id)
			if _, statErr := os.Stat(spill); statErr != nil {
				return fmt.Errorf("open session for deletion: %w", err)
			}
			if removeErr := os.RemoveAll(spill); removeErr != nil {
				return fmt.Errorf("delete orphaned session spill directory: %w", removeErr)
			}
			if syncErr := syncDirectory(s.root); syncErr != nil {
				return fmt.Errorf("sync orphaned session spill deletion: %w", syncErr)
			}
			return nil
		}
		return fmt.Errorf("open session for deletion: %w", err)
	}
	if err := tryLock(file); err != nil {
		_ = file.Close()
		return err
	}
	defer func() {
		unlock(file)
		if closeErr := file.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close session for deletion: %w", closeErr))
		}
	}()
	if before != nil {
		records, _, _, readErr := readRecords(file)
		if readErr != nil {
			return fmt.Errorf("read session for deletion: %w", readErr)
		}
		state, foldErr := foldRecords(records)
		if foldErr != nil {
			return fmt.Errorf("fold session for deletion: %w", foldErr)
		}
		if beforeErr := before(state); beforeErr != nil {
			return beforeErr
		}
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	if err := os.RemoveAll(s.spillDir(id)); err != nil {
		return fmt.Errorf("delete session spill directory: %w", err)
	}
	if err := syncDirectory(s.root); err != nil {
		return fmt.Errorf("sync session deletion: %w", err)
	}
	return nil
}

func (s *fileStore) spillDir(id string) string {
	return filepath.Join(s.root, id+".spill")
}

func (s *fileStore) path(id string) (string, error) {
	if !validSessionID(id) {
		return "", errors.New("invalid session ID")
	}
	return filepath.Join(s.root, id+".jsonl"), nil
}

// encodeRecord renders one log line, so a caller can measure a record before
// deciding to append it.
func encodeRecord(record sessionRecord) ([]byte, error) {
	encoded, err := json.Marshal(record)
	if err != nil {
		return nil, fmt.Errorf("encode session record: %w", err)
	}
	if len(encoded) > maxRecordBytes {
		return nil, fmt.Errorf("session record is %d bytes; limit is %d", len(encoded), maxRecordBytes)
	}
	return append(encoded, '\n'), nil
}

func (l *sessionLog) append(records ...sessionRecord) error {
	var data []byte
	since := l.sinceCheckpoint
	for _, record := range records {
		encoded, err := encodeRecord(record)
		if err != nil {
			return err
		}
		data = append(data, encoded...)
		if record.Type == recordCheckpoint {
			since = 0
		} else {
			since += len(encoded)
		}
	}
	for len(data) > 0 {
		written, err := l.file.Write(data)
		if err != nil {
			return fmt.Errorf("append session record: %w", err)
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		data = data[written:]
	}
	if err := l.file.Sync(); err != nil {
		return fmt.Errorf("sync session record: %w", err)
	}
	l.sinceCheckpoint = since
	return nil
}

func (l *sessionLog) close() {
	unlock(l.file)
	_ = l.file.Close()
}

// readRecords returns the log's records, the offset a torn tail must be
// truncated to or -1, and the bytes that follow the log's last checkpoint.
func readRecords(file *os.File) ([]sessionRecord, int64, int, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, -1, 0, fmt.Errorf("seek session log: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		return nil, -1, 0, fmt.Errorf("stat session log: %w", err)
	}
	if info.Size() > maxSessionBytes {
		return nil, -1, 0, fmt.Errorf("session log is %d bytes; limit is %d", info.Size(), maxSessionBytes)
	}
	data, err := io.ReadAll(io.LimitReader(file, maxSessionBytes+1))
	if err != nil {
		return nil, -1, 0, fmt.Errorf("read session log: %w", err)
	}
	truncateAt := int64(-1)
	if len(data) > 0 && data[len(data)-1] != '\n' {
		last := bytes.LastIndexByte(data, '\n')
		truncateAt = int64(last + 1)
		data = data[:last+1]
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 64*1024), maxRecordBytes+1)
	var records []sessionRecord
	line := 0
	sinceCheckpoint := 0
	for scanner.Scan() {
		line++
		if len(scanner.Bytes()) == 0 {
			return nil, -1, 0, fmt.Errorf("empty interior record at line %d", line)
		}
		var record sessionRecord
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			return nil, -1, 0, fmt.Errorf("decode record at line %d: %w", line, err)
		}
		if record.Type == recordCheckpoint {
			sinceCheckpoint = 0
		} else {
			sinceCheckpoint += len(scanner.Bytes()) + 1
		}
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		return nil, -1, 0, fmt.Errorf("scan session log: %w", err)
	}
	if len(records) == 0 {
		return nil, -1, 0, errors.New("session log has no creation record")
	}
	return records, truncateAt, sinceCheckpoint, nil
}

func validSessionID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for _, char := range id {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

type listCursor struct {
	CWD       string `json:"cwd,omitempty"`
	UpdatedAt string `json:"updatedAt"`
	SessionID string `json:"sessionId"`
}

func encodeCursor(cursor listCursor) string {
	data, err := json.Marshal(cursor)
	if err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(data)
}

func decodeCursor(value string) (listCursor, error) {
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return listCursor{}, errors.New("invalid session cursor")
	}
	var cursor listCursor
	if err := json.Unmarshal(data, &cursor); err != nil ||
		cursor.UpdatedAt == "" || !validSessionID(cursor.SessionID) {
		return listCursor{}, errors.New("invalid session cursor")
	}
	return cursor, nil
}
