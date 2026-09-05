package skills

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestDiscoverValidSkillsInNameOrder(t *testing.T) {
	workspace := t.TempDir()
	writeSkill(t, workspace, "zebra", `---
name: zebra
description: Last skill.
allowed-tools: Shell
metadata:
  owner: test
---
Zebra body.
`)
	writeSkill(t, workspace, "alpha", `---
name: alpha
description: First skill.
license: MIT
compatibility: Requires git.
---
Alpha body.
`)

	got, warnings, err := Discover(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v", warnings)
	}
	if len(got) != 2 || got[0].Name != "alpha" || got[1].Name != "zebra" {
		t.Fatalf("skills = %#v", got)
	}
	if got[0].Path != ".agents/skills/alpha/SKILL.md" || len(got[0].Digest) != 64 {
		t.Fatalf("alpha reference = %#v", got[0])
	}
	body, err := Load(workspace, got[0])
	if err != nil {
		t.Fatal(err)
	}
	if body != "Alpha body.\n" {
		t.Fatalf("body = %q", body)
	}
}

func TestDiscoverMissingCatalogIsEmpty(t *testing.T) {
	got, warnings, err := Discover(t.TempDir())
	if err != nil || got != nil || warnings != nil {
		t.Fatalf("discover = %#v, %v, %v", got, warnings, err)
	}
}

func TestDiscoverSkipsMalformedEntriesWithPaths(t *testing.T) {
	workspace := t.TempDir()
	for name, content := range map[string]string{
		"Bad": `---
name: Bad
description: uppercase
---
`,
		"mismatch": `---
name: other
description: mismatch
---
`,
		"missing-description": `---
name: missing-description
---
`,
		"bad-yaml": `---
name: [bad
---
`,
		"no-frontmatter": "plain text",
	} {
		writeSkill(t, workspace, name, content)
	}
	writeSkill(t, workspace, "good", `---
name: good
description: valid
---
body
`)
	missing := filepath.Join(workspace, filepath.FromSlash(CatalogDir), "missing-file")
	if err := os.MkdirAll(missing, 0o755); err != nil {
		t.Fatal(err)
	}

	got, warnings, err := Discover(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "good" {
		t.Fatalf("skills = %#v", got)
	}
	if len(warnings) != 6 {
		t.Fatalf("warnings = %d: %v", len(warnings), warnings)
	}
	for _, warning := range warnings {
		if !strings.Contains(warning.Error(), filepath.Join(workspace, filepath.FromSlash(CatalogDir))) {
			t.Fatalf("warning lacks path: %v", warning)
		}
	}
}

func TestDiscoverRejectsInvalidFileContent(t *testing.T) {
	for name, data := range map[string][]byte{
		"invalid-utf8": append([]byte("---\nname: invalid-utf8\ndescription: "), 0xff),
		"oversized":    []byte(strings.Repeat("x", MaxFileBytes+1)),
	} {
		t.Run(name, func(t *testing.T) {
			workspace := t.TempDir()
			path := filepath.Join(workspace, filepath.FromSlash(CatalogDir), name, skillFile)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			got, warnings, err := Discover(workspace)
			if err != nil || len(got) != 0 || len(warnings) != 1 {
				t.Fatalf("discover = %#v, %v, %v", got, warnings, err)
			}
		})
	}
}

func TestDiscoverAcceptsFileAtLimit(t *testing.T) {
	workspace := t.TempDir()
	prefix := "---\nname: limit\ndescription: valid\n---\n"
	content := prefix + strings.Repeat("x", MaxFileBytes-len(prefix))
	writeSkill(t, workspace, "limit", content)
	got, warnings, err := Discover(workspace)
	if err != nil || len(warnings) != 0 || len(got) != 1 {
		t.Fatalf("discover = %#v, %v, %v", got, warnings, err)
	}
}

func TestDiscoverSkipsNonRegularSkillFiles(t *testing.T) {
	for _, test := range []struct {
		name string
		make func(string) error
	}{
		{name: "directory", make: func(path string) error { return os.Mkdir(path, 0o700) }},
		{name: "fifo", make: func(path string) error { return syscall.Mkfifo(path, 0o600) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			workspace := t.TempDir()
			path := filepath.Join(workspace, filepath.FromSlash(CatalogDir), "special", skillFile)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := test.make(path); err != nil {
				t.Skipf("special file unavailable: %v", err)
			}
			got, warnings, err := Discover(workspace)
			if err != nil || len(got) != 0 || len(warnings) != 1 ||
				!strings.Contains(warnings[0].Error(), "regular file") {
				t.Fatalf("discover = %#v, %v, %v", got, warnings, err)
			}
		})
	}
}

func TestDiscoverEnforcesSkillCount(t *testing.T) {
	workspace := t.TempDir()
	for index := 0; index <= MaxSkills; index++ {
		name := fmt.Sprintf("skill-%03d", index)
		writeSkill(t, workspace, name, fmt.Sprintf("---\nname: %s\ndescription: valid\n---\n", name))
	}
	_, _, err := Discover(workspace)
	if err == nil || !strings.Contains(err.Error(), "129 valid skills") {
		t.Fatalf("error = %v", err)
	}
}

func TestDiscoverRejectsCatalogRootSymlinks(t *testing.T) {
	for _, name := range []string{".agents", filepath.FromSlash(CatalogDir)} {
		t.Run(name, func(t *testing.T) {
			workspace := t.TempDir()
			target := t.TempDir()
			link := filepath.Join(workspace, name)
			if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, link); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
			_, _, err := Discover(workspace)
			if err == nil || !strings.Contains(err.Error(), "symbolic links") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestDiscoverSkipsSymlinkedSkillAndFile(t *testing.T) {
	workspace := t.TempDir()
	catalog := filepath.Join(workspace, filepath.FromSlash(CatalogDir))
	if err := os.MkdirAll(catalog, 0o755); err != nil {
		t.Fatal(err)
	}
	target := t.TempDir()
	if err := os.Symlink(target, filepath.Join(catalog, "linked-dir")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	dir := filepath.Join(catalog, "linked-file")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	targetFile := filepath.Join(target, skillFile)
	if err := os.WriteFile(targetFile, []byte("skill"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(targetFile, filepath.Join(dir, skillFile)); err != nil {
		t.Fatal(err)
	}

	got, warnings, err := Discover(workspace)
	if err != nil || len(got) != 0 || len(warnings) != 2 {
		t.Fatalf("discover = %#v, %v, %v", got, warnings, err)
	}
}

func TestLoadRejectsChangedMissingAndSymlinkedFiles(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(t *testing.T, path string)
	}{
		{name: "changed", change: func(t *testing.T, path string) {
			writeFile(t, path, `---
name: demo
description: changed
---
new body
`)
		}},
		{name: "missing", change: func(t *testing.T, path string) {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "symlinked", change: func(t *testing.T, path string) {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(t.TempDir(), skillFile)
			writeFile(t, target, "replacement")
			if err := os.Symlink(target, path); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			workspace := t.TempDir()
			path := writeSkill(t, workspace, "demo", `---
name: demo
description: original
---
body
`)
			references, _, err := Discover(workspace)
			if err != nil {
				t.Fatal(err)
			}
			test.change(t, path)
			_, err = Load(workspace, references[0])
			if err == nil || !strings.Contains(err.Error(), references[0].Path) ||
				!strings.Contains(err.Error(), "reactivate") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestValidateReferencesRejectsTampering(t *testing.T) {
	valid := Reference{
		Name: "demo", Description: "description",
		Path: ".agents/skills/demo/SKILL.md", Digest: strings.Repeat("0", 64),
	}
	for _, references := range [][]Reference{
		{{Name: "Bad", Description: valid.Description, Path: valid.Path, Digest: valid.Digest}},
		{{Name: valid.Name, Description: "", Path: valid.Path, Digest: valid.Digest}},
		{{Name: valid.Name, Description: valid.Description, Path: "elsewhere", Digest: valid.Digest}},
		{{Name: valid.Name, Description: valid.Description, Path: valid.Path, Digest: "bad"}},
		{valid, valid},
	} {
		if err := ValidateReferences(references); err == nil {
			t.Fatalf("references unexpectedly valid: %#v", references)
		}
	}
}

func TestSplitFrontmatterSupportsBOMAndCRLF(t *testing.T) {
	header, body, err := splitFrontmatter([]byte("\xef\xbb\xbf---\r\nname: demo\r\ndescription: ok\r\n---\r\nbody\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(header), "name: demo") || string(body) != "body\r\n" {
		t.Fatalf("header = %q, body = %q", header, body)
	}
}

func writeSkill(t *testing.T, workspace, name, content string) string {
	t.Helper()
	path := filepath.Join(workspace, filepath.FromSlash(CatalogDir), name, skillFile)
	writeFile(t, path, content)
	return path
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoverUnreadableCatalog(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read permissionless directories")
	}
	workspace := t.TempDir()
	catalog := filepath.Join(workspace, filepath.FromSlash(CatalogDir))
	if err := os.MkdirAll(catalog, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(catalog, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(catalog, 0o755) })
	_, _, err := Discover(workspace)
	if err == nil || !errors.Is(err, os.ErrPermission) {
		t.Fatalf("error = %v", err)
	}
}
