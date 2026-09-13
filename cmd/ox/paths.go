package main

import (
	"os"
	"path/filepath"
)

// storePath resolves one of Ox's per-user directories. An XDG variable is
// honored only when it is absolute, because a relative one would anchor durable
// storage to whatever directory Ox happened to start in. Otherwise the
// conventional location under an absolute HOME applies. When neither is
// absolute there is no answer, and each store falls back to a temporary
// directory rather than guessing.
func storePath(xdgValue, home string, homeRelative []string, name ...string) string {
	base := ""
	switch {
	case filepath.IsAbs(xdgValue):
		base = xdgValue
	case filepath.IsAbs(home):
		base = filepath.Join(append([]string{home}, homeRelative...)...)
	default:
		return ""
	}
	return filepath.Join(append([]string{base}, name...)...)
}

// processPaths are the per-user locations Ox reads and writes. They are
// resolved once at startup so no lower boundary reads the environment.
type processPaths struct {
	settings     string
	sessions     string
	memory       string
	modelCatalog string
}

func resolveProcessPaths() processPaths {
	home := os.Getenv("HOME")
	data := os.Getenv("XDG_DATA_HOME")
	return processPaths{
		settings: storePath(
			os.Getenv("XDG_CONFIG_HOME"), home, []string{".config"}, "ox", "settings.json",
		),
		sessions: storePath(
			data, home, []string{".local", "share"}, "ox", "sessions",
		),
		memory: storePath(
			data, home, []string{".local", "share"}, "ox", "memory",
		),
		modelCatalog: storePath(
			os.Getenv("XDG_CACHE_HOME"), home, []string{".cache"}, "ox", "models.json",
		),
	}
}
