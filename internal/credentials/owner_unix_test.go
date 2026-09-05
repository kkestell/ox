//go:build unix

package credentials

import (
	"io/fs"
	"os"
	"syscall"
	"testing"
	"time"
)

func TestOwnedByCurrentUserRejectsAnotherOwner(t *testing.T) {
	info := fakeFileInfo{stat: &syscall.Stat_t{Uid: uint32(os.Geteuid() + 1)}}
	if ownedByCurrentUser(info) {
		t.Fatal("different owner was accepted")
	}
}

type fakeFileInfo struct{ stat *syscall.Stat_t }

func (f fakeFileInfo) Name() string       { return "credential" }
func (f fakeFileInfo) Size() int64        { return 1 }
func (f fakeFileInfo) Mode() fs.FileMode  { return 0o600 }
func (f fakeFileInfo) ModTime() time.Time { return time.Time{} }
func (f fakeFileInfo) IsDir() bool        { return false }
func (f fakeFileInfo) Sys() any           { return f.stat }
