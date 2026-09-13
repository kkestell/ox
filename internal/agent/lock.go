package agent

import "github.com/kkestell/ox/internal/workspace"

func syncDirectory(path string) error {
	return workspace.SyncDirectory(path)
}
