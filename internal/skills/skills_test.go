package skills

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func writeSkill(t *testing.T, directory, name, text string) {
	t.Helper()
	path := filepath.Join(directory, name)
	if err := os.MkdirAll(path, 0o777); err != nil {
		t.Fatal(err)
	}
	if text != "" {
		if err := os.WriteFile(filepath.Join(path, fileName), []byte(text), 0o666); err != nil {
			t.Fatal(err)
		}
	}
}

func valid(name string) string {
	return "---\nname: " + name + "\ndescription: Valid.\nargument-hint: target\n---\nBody\n"
}

func names(loaded []Skill) []string {
	var names []string
	for _, skill := range loaded {
		names = append(names, skill.Name)
	}
	return names
}

func TestInvalidDefinitionsAreSkippedWithTheirPath(t *testing.T) {
	for _, test := range []struct{ name, text, want string }{
		{"Goal", "---\nname: Goal\ndescription: Goal.\n---\nBody\n", "may contain only lowercase letters, digits, and hyphens"},
		{"other", "---\nname: goal\ndescription: Goal.\n---\nBody\n", "does not match its directory"},
		{"blank", "---\nname: blank\ndescription: \" \"\n---\nBody\n", "description is blank"},
		{"empty", "---\nname: empty\ndescription: Empty.\n---\n\n", "instructions are blank"},
		{"plain", "No frontmatter.\n", "does not begin with --- delimited YAML"},
		{"missing", "", "no such file or directory"},
	} {
		home, workspace := t.TempDir(), t.TempDir()
		directory := filepath.Join(workspace, ".agents/skills")
		writeSkill(t, directory, test.name, test.text)
		writeSkill(t, directory, "neighbor", valid("neighbor"))
		loaded, skipped := Load(home, workspace)
		if !slices.Equal(names(loaded), []string{"neighbor"}) || len(skipped) != 1 ||
			!strings.HasPrefix(skipped[0], ".agents/skills/"+test.name+"/SKILL.md: ") || !strings.Contains(skipped[0], test.want) {
			t.Errorf("%s: %v %v", test.name, names(loaded), skipped)
		}
		if loaded[0].ArgumentHint != "target" || loaded[0].Instructions != "Body" {
			t.Errorf("neighbor = %+v", loaded[0])
		}
	}
}

func TestHigherPrioritySkillsAndBrokenDefinitionsClaimTheirNames(t *testing.T) {
	home, workspace := t.TempDir(), t.TempDir()
	ox := filepath.Join(home, ".config/ox/skills")
	agents := filepath.Join(home, ".agents/skills")
	local := filepath.Join(workspace, ".agents/skills")
	writeSkill(t, agents, "careful", "No frontmatter.\n")
	writeSkill(t, local, "careful", valid("careful"))
	writeSkill(t, ox, "goal", valid("goal"))
	writeSkill(t, local, "goal", "No frontmatter.\n")
	writeSkill(t, local, "zeta", valid("zeta"))
	loaded, skipped := Load(home, workspace)
	if !slices.Equal(names(loaded), []string{"goal", "zeta"}) {
		t.Errorf("a broken ~/.agents/skills/careful keeps the workspace careful out: %v", names(loaded))
	}
	slices.Sort(skipped)
	if len(skipped) != 2 || !strings.HasPrefix(skipped[0], ".agents/skills/goal/SKILL.md: ") ||
		!strings.HasPrefix(skipped[1], "~/.agents/skills/careful/SKILL.md: ") {
		t.Errorf("skipped = %v", skipped)
	}
}
