package openrouter

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
)

const (
	cacheDirectory = "ox"
	cacheFile      = "models.json"
)

var cacheWriteSequence atomic.Uint64

func (c *Client) resolvedCachePath() string {
	if c.cachePathOverride != "" {
		return c.cachePathOverride
	}
	return catalogCachePath(os.Getenv("XDG_CACHE_HOME"), os.Getenv("HOME"))
}

func catalogCachePath(xdgCacheHome, home string) string {
	var base string
	if filepath.IsAbs(xdgCacheHome) {
		base = xdgCacheHome
	} else if home != "" {
		base = filepath.Join(home, ".cache")
	}
	if !filepath.IsAbs(base) {
		return ""
	}
	return filepath.Join(base, cacheDirectory, cacheFile)
}

// readCatalogCache returns the cached catalog and how long ago it was written.
// A cache holding no models is refused: it parses, so nothing else would notice
// it, and memoizing it would answer every model lookup for the process with an
// empty catalog.
func readCatalogCache(path string) (*Catalog, time.Duration, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, fmt.Errorf("read catalog cache: %w", err)
	}
	info, statErr := file.Stat()
	raw, readErr := io.ReadAll(io.LimitReader(file, maxCatalogBytes+1))
	closeErr := file.Close()
	if err := errors.Join(statErr, readErr, closeErr); err != nil {
		return nil, 0, fmt.Errorf("read catalog cache: %w", err)
	}
	if len(raw) > maxCatalogBytes {
		return nil, 0, fmt.Errorf("catalog cache exceeds %d bytes", maxCatalogBytes)
	}
	var envelope modelEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, 0, fmt.Errorf("decode catalog cache: %w", err)
	}
	if len(envelope.Data) == 0 {
		return nil, 0, errors.New("catalog cache contains no models")
	}
	return newCatalog(envelope.Data), time.Since(info.ModTime()), nil
}

func writeCatalogCache(path string, models []Model) error {
	parent := filepath.Dir(path)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("create catalog cache directory: %w", err)
	}

	raw, err := json.Marshal(modelEnvelope{Data: models})
	if err != nil {
		return fmt.Errorf("encode catalog cache: %w", err)
	}
	temporary := filepath.Join(
		parent,
		fmt.Sprintf(
			"%s.%d.%d.tmp",
			filepath.Base(path),
			os.Getpid(),
			cacheWriteSequence.Add(1),
		),
	)
	if err := os.WriteFile(temporary, raw, 0o600); err != nil {
		return fmt.Errorf("write catalog cache temporary file: %w", err)
	}
	if err := os.Rename(temporary, path); err != nil {
		removeErr := os.Remove(temporary)
		return errors.Join(fmt.Errorf("install catalog cache: %w", err), removeErr)
	}
	return nil
}
