// Package skills loads skill definitions: `<name>/SKILL.md` entries in the
// skills directories, read when a session becomes active.
package skills

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"ox/internal/textfile"
)

const fileName = "SKILL.md"

// Skill is one skill definition. Instructions are the Markdown after the
// frontmatter.
type Skill struct {
	Name         string
	Description  string
	ArgumentHint string
	Instructions string
}

// Load returns the skill catalog for a session in workspace, ordered by name,
// and one message for each skipped definition or unreadable directory. The
// skills directories are `~/.config/ox/skills`, `~/.agents/skills`, and the
// workspace's `.agents/skills`, highest priority first. A skill replaces any
// lower-priority skill with the same name.
//
// An invalid definition is skipped but still claims its directory name, so a
// lower-priority skill never runs under the name of a broken one.
func Load(home, workspace string) (skills []Skill, skipped []string) {
	directories := []struct{ path, shown string }{
		{filepath.Join(home, ".config/ox/skills"), "~/.config/ox/skills"},
		{filepath.Join(home, ".agents/skills"), "~/.agents/skills"},
		{filepath.Join(workspace, ".agents/skills"), ".agents/skills"},
	}
	claimed := map[string]bool{}
	for _, directory := range directories {
		entries, err := os.ReadDir(directory.path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			skipped = append(skipped, fmt.Sprintf("%s: %v", directory.shown, err))
			continue
		}
		for _, entry := range entries {
			path := filepath.Join(directory.path, entry.Name())
			if info, err := os.Stat(path); err != nil || !info.IsDir() {
				continue
			}
			name := entry.Name()
			skill, err := load(filepath.Join(path, fileName), name)
			unclaimed := !claimed[name]
			claimed[name] = true
			if err != nil {
				skipped = append(skipped, fmt.Sprintf("%s/%s/%s: %v", directory.shown, name, fileName, err))
			} else if unclaimed {
				skills = append(skills, skill)
			}
		}
	}
	slices.SortFunc(skills, func(a, b Skill) int { return strings.Compare(a.Name, b.Name) })
	return skills, skipped
}

func load(path, directoryName string) (Skill, error) {
	text, err := textfile.Read(path)
	if err != nil {
		return Skill{}, err
	}
	return parse(directoryName, text)
}

func parse(directoryName, text string) (Skill, error) {
	frontmatter, body, err := splitFrontmatter(text)
	if err != nil {
		return Skill{}, err
	}
	// Other frontmatter keys belong to other agents and are ignored.
	var fields struct {
		Name         *string `yaml:"name"`
		Description  *string `yaml:"description"`
		ArgumentHint string  `yaml:"argument-hint"`
	}
	if err := yaml.Unmarshal([]byte(frontmatter), &fields); err != nil {
		return Skill{}, fmt.Errorf("frontmatter: %w", err)
	}
	if fields.Name == nil || fields.Description == nil {
		return Skill{}, errors.New("frontmatter: name and description are required")
	}
	name := *fields.Name
	if name == "" || strings.Trim(name, "abcdefghijklmnopqrstuvwxyz0123456789-") != "" {
		return Skill{}, fmt.Errorf("name %q may contain only lowercase letters, digits, and hyphens", name)
	}
	if name != directoryName {
		return Skill{}, fmt.Errorf("name %q does not match its directory", name)
	}
	if strings.TrimSpace(*fields.Description) == "" {
		return Skill{}, errors.New("description is blank")
	}
	instructions := strings.TrimSpace(body)
	if instructions == "" {
		return Skill{}, errors.New("instructions are blank")
	}
	return Skill{Name: name, Description: *fields.Description, ArgumentHint: fields.ArgumentHint, Instructions: instructions}, nil
}

// splitFrontmatter splits `---` delimited YAML frontmatter from the Markdown
// that follows it.
func splitFrontmatter(text string) (string, string, error) {
	missing := errors.New("does not begin with --- delimited YAML frontmatter")
	rest, ok := strings.CutPrefix(text, "---\n")
	if !ok {
		if rest, ok = strings.CutPrefix(text, "---\r\n"); !ok {
			return "", "", missing
		}
	}
	offset := 0
	for line := range strings.Lines(rest) {
		if strings.TrimRight(line, "\r\n") == "---" {
			return rest[:offset], rest[offset+len(line):], nil
		}
		offset += len(line)
	}
	return "", "", missing
}
