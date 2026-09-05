package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestInvalidRootInstructionsRejectActivationAndProcessContinues(t *testing.T) {
	child := start(t)
	initialize(t, child)
	defer child.stop()
	path := filepath.Join(child.cwd, "AGENTS.md")

	tests := []struct {
		name string
		make func(*testing.T)
		want string
	}{
		{
			name: "oversize",
			make: func(t *testing.T) {
				t.Helper()
				if err := os.WriteFile(path, []byte(strings.Repeat("x", (64<<10)+1)), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			want: "exceeds",
		},
		{
			name: "invalid UTF-8",
			make: func(t *testing.T) {
				t.Helper()
				if err := os.WriteFile(path, []byte{0xff}, 0o600); err != nil {
					t.Fatal(err)
				}
			},
			want: "valid UTF-8",
		},
		{
			name: "directory",
			make: func(t *testing.T) {
				t.Helper()
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			},
			want: "regular file",
		},
		{
			name: "fifo",
			make: func(t *testing.T) {
				t.Helper()
				if err := syscall.Mkfifo(path, 0o600); err != nil {
					t.Fatal(err)
				}
			},
			want: "regular file",
		},
		{
			name: "symlink",
			make: func(t *testing.T) {
				t.Helper()
				target := filepath.Join(child.cwd, "actual-instructions.md")
				if err := os.WriteFile(target, []byte("rules"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Base(target), path); err != nil {
					t.Fatal(err)
				}
			},
			want: "symbolic links",
		},
	}
	if os.Geteuid() != 0 {
		tests = append(tests, struct {
			name string
			make func(*testing.T)
			want string
		}{
			name: "unreadable",
			make: func(t *testing.T) {
				t.Helper()
				if err := os.WriteFile(path, []byte("hidden"), 0o200); err != nil {
					t.Fatal(err)
				}
			},
			want: "permission",
		})
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_ = os.Chmod(path, 0o700)
			if err := os.RemoveAll(path); err != nil {
				t.Fatal(err)
			}
			test.make(t)
			responseError := child.requestError("session/new", newSessionRequest(child.cwd))
			if responseError.Code != -32603 || !strings.Contains(responseError.Message, path) ||
				!strings.Contains(responseError.Message, test.want) {
				t.Fatalf("activation error = %#v, want path and %q", responseError, test.want)
			}
		})
	}

	_ = os.Chmod(path, 0o700)
	if err := os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("valid instructions\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if session := newSession(t, child, child.cwd); session == "" {
		t.Fatal("valid session returned no ID")
	}
}
