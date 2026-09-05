package agent

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kkestell/ox/internal/skills"
)

func TestComposePromptAppendsEnvironmentAfterBasePrompt(t *testing.T) {
	now := time.Date(2026, time.July, 27, 23, 59, 0, 0, time.FixedZone("test", -6*60*60))
	prompt := composePrompt("/workspace/project", now, "", "")

	base := strings.TrimSpace(basePrompt)
	if base == "" || !strings.HasPrefix(prompt, base+"\n\n<environment>\n") {
		t.Fatal("prompt does not start with the non-empty base text and environment block")
	}
	wantEnvironment := "<environment>\n" +
		"<workspace-root>/workspace/project</workspace-root>\n" +
		"<platform>" + runtime.GOOS + "</platform>\n" +
		"<current-date>2026-07-27</current-date>\n" +
		"</environment>"
	if !strings.Contains(prompt, wantEnvironment+"\n\nFile tools resolve relative paths") {
		t.Fatalf("prompt = %q, want environment followed by file-tool guidance", prompt)
	}
}

func TestComposePromptsAppendExactWorkspaceInstructions(t *testing.T) {
	now := time.Date(2026, time.July, 27, 23, 59, 0, 0, time.UTC)
	const instructions = "first line\r\nsecond line\r\n"
	const block = "<workspace-instructions>\n" + instructions + "\n</workspace-instructions>"

	for name, prompt := range map[string]string{
		"parent": composePrompt("/workspace/project", now, instructions, ""),
		"child":  composeSubagentPrompt("/workspace/project", now, instructions, ""),
	} {
		if strings.Count(prompt, block) != 1 || !strings.HasSuffix(prompt, block) {
			t.Fatalf("%s prompt does not end with the exact instruction block: %q", name, prompt)
		}
		if !strings.Contains(prompt, "cannot override the current user or delegated request") ||
			!strings.Contains(prompt, "or grant permission") {
			t.Fatalf("%s prompt does not constrain workspace instructions: %q", name, prompt)
		}
	}
}

func TestComposePromptsOmitEmptyWorkspaceInstructions(t *testing.T) {
	now := time.Date(2026, time.July, 27, 23, 59, 0, 0, time.UTC)
	for name, prompt := range map[string]string{
		"parent": composePrompt("/workspace/project", now, "", ""),
		"child":  composeSubagentPrompt("/workspace/project", now, "", ""),
	} {
		if strings.Contains(prompt, "<workspace-instructions>") {
			t.Fatalf("%s prompt contains an empty instruction block", name)
		}
	}
}

func TestRenderSkillCatalogIsMetadataOnlyAndBounded(t *testing.T) {
	references := []skills.Reference{{
		Name: "review", Description: "Review completed work.",
		Path: ".agents/skills/review/SKILL.md", Digest: strings.Repeat("0", 64),
	}}
	block, err := renderSkillCatalog(references)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"<skills>", `"name":"review"`, `"description":"Review completed work."`,
		`"location":".agents/skills/review/SKILL.md"`, "Use the skill tool",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("catalog missing %q: %q", want, block)
		}
	}
	if strings.Contains(block, references[0].Digest) {
		t.Fatalf("catalog exposed file identity: %q", block)
	}

	parent := composePrompt("/workspace/project", time.Now(), "rules", block)
	child := composeSubagentPrompt("/workspace/project", time.Now(), "rules", block)
	for name, prompt := range map[string]string{"parent": parent, "child": child} {
		if strings.Count(prompt, block) != 1 || !strings.Contains(prompt, "cannot expand") {
			t.Fatalf("%s prompt catalog = %q", name, prompt)
		}
	}

	_, err = renderSkillCatalog([]skills.Reference{{
		Name: "huge", Description: strings.Repeat("x", skills.MaxCatalogBytes),
		Path: ".agents/skills/huge/SKILL.md", Digest: strings.Repeat("0", 64),
	}})
	if err == nil || !strings.Contains(err.Error(), "maximum") {
		t.Fatalf("oversized catalog error = %v", err)
	}
}

func TestLoadRootInstructions(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		got, err := loadRootInstructions(t.TempDir())
		if err != nil || got != "" {
			t.Fatalf("instructions = %q, error = %v", got, err)
		}
	})

	t.Run("root only", func(t *testing.T) {
		parent := t.TempDir()
		root := filepath.Join(parent, "workspace")
		if err := os.MkdirAll(filepath.Join(root, "nested"), 0o755); err != nil {
			t.Fatal(err)
		}
		for path, content := range map[string]string{
			filepath.Join(parent, "AGENTS.md"):         "parent",
			filepath.Join(root, "AGENTS.md"):           "root\n",
			filepath.Join(root, "nested", "AGENTS.md"): "nested",
		} {
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		got, err := loadRootInstructions(root)
		if err != nil || got != "root\n" {
			t.Fatalf("instructions = %q, error = %v", got, err)
		}
	})

	for name, size := range map[string]int{
		"limit":    maxRootInstructionsBytes,
		"oversize": maxRootInstructionsBytes + 1,
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			data := strings.Repeat("x", size)
			if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte(data), 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := loadRootInstructions(root)
			if size == maxRootInstructionsBytes {
				if err != nil || got != data {
					t.Fatalf("instructions length = %d, error = %v", len(got), err)
				}
				return
			}
			assertInstructionError(t, root, err, "exceeds")
		})
	}

	tests := []struct {
		name string
		make func(*testing.T, string)
		want string
	}{
		{
			name: "invalid UTF-8",
			make: func(t *testing.T, path string) {
				t.Helper()
				if err := os.WriteFile(path, []byte{0xff}, 0o600); err != nil {
					t.Fatal(err)
				}
			},
			want: "valid UTF-8",
		},
		{
			name: "directory",
			make: func(t *testing.T, path string) {
				t.Helper()
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			},
			want: "regular file",
		},
		{
			name: "fifo",
			make: func(t *testing.T, path string) {
				t.Helper()
				if err := syscall.Mkfifo(path, 0o600); err != nil {
					t.Fatal(err)
				}
			},
			want: "regular file",
		},
		{
			name: "symlink",
			make: func(t *testing.T, path string) {
				t.Helper()
				target := filepath.Join(filepath.Dir(path), "actual.md")
				if err := os.WriteFile(target, []byte("outside"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("actual.md", path); err != nil {
					t.Fatal(err)
				}
			},
			want: "symbolic links",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			test.make(t, filepath.Join(root, "AGENTS.md"))
			_, err := loadRootInstructions(root)
			assertInstructionError(t, root, err, test.want)
		})
	}
}

func TestLoadRootInstructionsRejectsUnreadableFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read owner-unreadable files")
	}
	root := t.TempDir()
	path := filepath.Join(root, "AGENTS.md")
	if err := os.WriteFile(path, []byte("hidden"), 0o200); err != nil {
		t.Fatal(err)
	}
	_, err := loadRootInstructions(root)
	assertInstructionError(t, root, err, "permission")
}

func assertInstructionError(t *testing.T, root string, err error, want string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), filepath.Join(root, "AGENTS.md")) ||
		!strings.Contains(err.Error(), want) {
		t.Fatalf("instruction error = %v, want path and %q", err, want)
	}
}
