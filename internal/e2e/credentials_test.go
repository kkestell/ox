package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kkestell/ox/internal/acp"
)

func TestNewSessionRequiresACredential(t *testing.T) {
	child := start(t, withCredential(""))
	initialize(t, child)

	responseError := child.requestError("session/new", newSessionRequest(child.cwd))
	if responseError.Code != acp.ErrCodeAuthRequired {
		t.Errorf("error code = %d, want %d", responseError.Code, acp.ErrCodeAuthRequired)
	}
	for _, want := range []string{"--credential-file", "ox login"} {
		if !strings.Contains(responseError.Message, want) {
			t.Errorf("error message = %q, want it to name %s", responseError.Message, want)
		}
	}

	// A round trip after the failure proves Ox keeps answering.
	initialize(t, child)
}

func TestCredentialReachesTheProviderAndNotTheLogs(t *testing.T) {
	const key = "credential-that-must-stay-secret"
	model := startModel(t, sse(evFinishReason("stop")))
	child, session := startSession(t,
		withModel(model),
		withCredential(key),
	)
	prompt(t, child, session, "credential check")

	if got := model.requestFor("credential check").Authorization; got != "Bearer "+key {
		t.Errorf("Authorization = %q, want %q", got, "Bearer "+key)
	}
	child.stop()
	if stderr := child.stderr.String(); strings.Contains(stderr, key) {
		t.Errorf("stderr leaked the credential: %s", stderr)
	}
	err := filepath.WalkDir(filepath.Join(child.cwd, "data", "ox", "sessions"), func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		raw, err := os.ReadFile(path)
		if err == nil && strings.Contains(string(raw), key) {
			t.Errorf("session record %s leaked the credential", path)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCredentialFileStartupValidation(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		options []startOption
		want    string
	}{
		{name: "missing", path: "missing", want: "inspect credential file"},
		{name: "directory", path: ".", want: "regular file"},
		{name: "blank", path: "bad", options: []startOption{withFile("bad", " \n")}, want: "empty"},
		{name: "multiple", path: "bad", options: []startOption{withFile("bad", "one\ntwo\n")}, want: "exactly one"},
		{name: "permissive", path: "bad", options: []startOption{withFileMode("bad", "secret\n", 0o644)}, want: "group or other"},
		{name: "unreadable", path: "bad", options: []startOption{withFileMode("bad", "secret\n", 0o200)}, want: "readable"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			options := append(test.options, withArguments("--credential-file", test.path))
			result := runCommand(t, "", nil, options...)
			if result.ExitCode != 1 || result.Stdout != "" || !strings.Contains(result.Stderr, test.want) {
				t.Fatalf("result = %#v", result)
			}
		})
	}
}
