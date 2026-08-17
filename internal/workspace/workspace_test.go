package workspace

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCanonical(t *testing.T) {
	parent := t.TempDir()
	real := filepath.Join(parent, "real")
	if err := os.Mkdir(real, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(parent, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Canonical(link)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("Canonical(%q) = %q, want %q", link, got, want)
	}

	file := filepath.Join(parent, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		path string
		want string
	}{
		{name: "missing", path: filepath.Join(parent, "missing"), want: "cannot access the working directory"},
		{name: "file", path: file, want: "working directory is not a directory"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := Canonical(test.path)
			if err == nil || !strings.Contains(err.Error(), test.want) ||
				!strings.Contains(err.Error(), test.path) {
				t.Fatalf("Canonical(%q) error = %v, want %q naming the path", test.path, err, test.want)
			}
		})
	}
}

func TestCanonicalRejectsAnUnlistableDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can list directories regardless of their mode")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o100); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(dir, 0o700); err != nil {
			t.Errorf("restore directory mode: %v", err)
		}
	})

	if _, err := Canonical(dir); err == nil || !strings.Contains(err.Error(), dir) {
		t.Fatalf("Canonical(%q) error = %v, want an access error naming the directory", dir, err)
	}
}

func TestNewRequiresAnAbsoluteRoot(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("New did not panic for a relative root")
		}
	}()
	New("relative")
}

func TestResolveNormalizesPathsInsideTheWorkspace(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "project")
	real := filepath.Join(root, "real")
	if err := os.MkdirAll(real, 0o700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(real, "file.txt")
	if err := os.WriteFile(file, []byte("contents"), 0o600); err != nil {
		t.Fatal(err)
	}
	insideLink := filepath.Join(root, "linked")
	if err := os.Symlink(real, insideLink); err != nil {
		t.Fatal(err)
	}
	rootLink := filepath.Join(parent, "project-link")
	if err := os.Symlink(root, rootLink); err != nil {
		t.Fatal(err)
	}

	canonical, err := Canonical(root)
	if err != nil {
		t.Fatal(err)
	}
	workspace := New(canonical)
	if workspace.Root() != canonical {
		t.Fatalf("Root() = %q, want %q", workspace.Root(), canonical)
	}

	for _, test := range []struct {
		name string
		path string
		want string
	}{
		{name: "relative", path: filepath.Join("real", "file.txt"), want: "real/file.txt"},
		{name: "cleaned relative", path: filepath.Join("real", ".", "child", "..", "file.txt"), want: "real/file.txt"},
		{name: "absolute", path: file, want: "real/file.txt"},
		{name: "root", path: root, want: "."},
		{name: "empty", path: "", want: "."},
		{name: "root symlink spelling", path: filepath.Join(rootLink, "real", "file.txt"), want: "real/file.txt"},
		{name: "interior symlink", path: filepath.Join("linked", "file.txt"), want: "real/file.txt"},
		{name: "missing tail", path: filepath.Join("real", "new", "file.txt"), want: "real/new/file.txt"},
	} {
		t.Run(test.name, func(t *testing.T) {
			resolved, err := workspace.Resolve(test.path)
			if err != nil {
				t.Fatal(err)
			}
			if got := resolved.String(); got != test.want {
				t.Errorf("String() = %q, want %q", got, test.want)
			}
			wantAbsolute := filepath.Join(canonical, filepath.FromSlash(test.want))
			if got := resolved.Absolute(); got != wantAbsolute {
				t.Errorf("Absolute() = %q, want %q", got, wantAbsolute)
			}
		})
	}
}

func TestResolveRejectsPathsOutsideTheWorkspace(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "project")
	outside := filepath.Join(parent, "outside")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "file.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "outside-link")); err != nil {
		t.Fatal(err)
	}
	canonical, err := Canonical(root)
	if err != nil {
		t.Fatal(err)
	}
	workspace := New(canonical)

	for _, path := range []string{
		"..",
		filepath.Join("..", ".."),
		outside,
		filepath.Join("outside-link", "file.txt"),
		filepath.Join("outside-link", "missing", "file.txt"),
	} {
		t.Run(filepath.ToSlash(path), func(t *testing.T) {
			_, err := workspace.Resolve(path)
			want := "`" + path + "` is outside the workspace"
			if err == nil || err.Error() != want {
				t.Fatalf("Resolve(%q) error = %v, want %q", path, err, want)
			}
		})
	}
}

// An outside path Ox cannot even reach is still outside, and saying so keeps the
// host's permissions from answering for the boundary.
func TestResolveRefusesAnOutsidePathItCannotTraverse(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can traverse directories regardless of their mode")
	}
	parent := t.TempDir()
	root := filepath.Join(parent, "project")
	secret := filepath.Join(parent, "secret")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(secret, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(secret, "file.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	unreadable(t, secret)
	canonical, err := Canonical(root)
	if err != nil {
		t.Fatal(err)
	}
	workspace := New(canonical)

	for _, path := range []string{
		filepath.Join("..", "secret", "file.txt"),
		filepath.Join(secret, "file.txt"),
	} {
		t.Run(filepath.ToSlash(path), func(t *testing.T) {
			_, err := workspace.Resolve(path)
			want := "`" + path + "` is outside the workspace"
			if err == nil || err.Error() != want {
				t.Fatalf("Resolve(%q) error = %v, want %q", path, err, want)
			}
		})
	}
}

// A path Ox cannot reach inside its own tree keeps reporting the reason, which
// is the other half of deciding confinement before reporting a failure.
func TestResolveReportsAnAccessFailureInsideTheWorkspace(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can traverse directories regardless of their mode")
	}
	root, workspace := testWorkspace(t)
	private := filepath.Join(root, "private")
	if err := os.Mkdir(private, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(private, "file.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	unreadable(t, private)

	_, err := workspace.Resolve(filepath.Join("private", "file.txt"))
	if err == nil || !strings.Contains(err.Error(), "cannot access `private/file.txt`:") {
		t.Fatalf("Resolve error = %v, want an access failure", err)
	}
}

func TestResolveReportsAccessFailuresWithoutLeakingHostPaths(t *testing.T) {
	root, workspace := testWorkspace(t)
	dangling := filepath.Join(root, "dangling")
	if err := os.Symlink(filepath.Join(root, "host-only", "missing"), dangling); err != nil {
		t.Fatal(err)
	}

	_, err := workspace.Resolve("dangling")
	if err == nil || !strings.Contains(err.Error(), "cannot access `dangling`:") {
		t.Fatalf("Resolve(dangling) error = %v, want an access failure", err)
	}
	if strings.Contains(err.Error(), root) {
		t.Fatalf("Resolve(dangling) error leaked workspace root: %v", err)
	}
}

func TestPathOpenReadsConfinedRegularFiles(t *testing.T) {
	root, workspace := testWorkspace(t)
	dir := filepath.Join(root, "real")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(file, []byte("contents"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dir, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{
		filepath.Join("real", "file.txt"),
		file,
		filepath.Join("linked", "file.txt"),
	} {
		resolved, err := workspace.Resolve(path)
		if err != nil {
			t.Fatal(err)
		}
		handle, err := resolved.Open()
		if err != nil {
			t.Fatal(err)
		}
		contents, readErr := io.ReadAll(handle)
		closeErr := handle.Close()
		if readErr != nil || closeErr != nil {
			t.Fatalf("read %q: %v; close: %v", path, readErr, closeErr)
		}
		if string(contents) != "contents" {
			t.Errorf("read %q = %q, want contents", path, contents)
		}
	}
}

func TestPathOpenRechecksConfinement(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "project")
	outside := filepath.Join(parent, "outside.txt")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(root, "victim.txt")
	if err := os.WriteFile(victim, []byte("inside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	canonical, err := Canonical(root)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := New(canonical).Resolve("victim.txt")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(victim); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, victim); err != nil {
		t.Fatal(err)
	}

	_, err = resolved.Open()
	if err == nil || err.Error() != "`victim.txt` is outside the workspace" {
		t.Fatalf("Open() error = %v, want an outside-workspace refusal", err)
	}
}

func TestPathOpenRejectsNonRegularAndMissingFiles(t *testing.T) {
	root, workspace := testWorkspace(t)
	if err := os.Mkdir(filepath.Join(root, "dir"), 0o700); err != nil {
		t.Fatal(err)
	}
	directory, err := workspace.Resolve("dir")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := directory.Open(); err == nil || err.Error() != "cannot access `dir`: not a regular file" {
		t.Fatalf("open directory error = %v, want a non-regular-file refusal", err)
	}

	missing, err := workspace.Resolve("missing.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, err = missing.Open()
	if err == nil || !strings.Contains(err.Error(), "cannot access `missing.txt`:") {
		t.Fatalf("open missing file error = %v, want an access failure", err)
	}
	if strings.Contains(err.Error(), root) {
		t.Fatalf("open missing file error leaked workspace root: %v", err)
	}
}

// unreadable takes a directory's permissions away and restores them before the
// scratch tree is removed.
func unreadable(t *testing.T, dir string) {
	t.Helper()
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(dir, 0o700); err != nil {
			t.Errorf("restore directory mode: %v", err)
		}
	})
}

func testWorkspace(t *testing.T) (string, Workspace) {
	t.Helper()
	root, err := Canonical(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root, New(root)
}
