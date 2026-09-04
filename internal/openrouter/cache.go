package openrouter

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
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

func readCatalogCache(path string) (*Catalog, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read catalog cache: %w", err)
	}
	var envelope modelEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("decode catalog cache: %w", err)
	}
	return newCatalog(envelope.Data), nil
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
