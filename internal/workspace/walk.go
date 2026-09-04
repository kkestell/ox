package workspace

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
)

type WalkedFile struct {
	root    *os.Root
	name    string
	Display string
}

func (f WalkedFile) Open() (*os.File, error) {
	return f.root.Open(f.name)
}

func walkRoot(
	ctx context.Context,
	root *os.Root,
	start string,
	display string,
	yield func(WalkedFile) bool,
) error {
	info, err := root.Stat(start)
	if err != nil {
		return accessError(err, display)
	}
	if !info.IsDir() {
		if info.Mode().IsRegular() {
			yield(WalkedFile{root: root, name: start, Display: display})
		}
		return ctx.Err()
	}

	patterns := readHostAncestorGitignores(root.Name())
	if start != "." {
		for _, dir := range ancestorNames(filepath.Dir(start)) {
			patterns = append(patterns, readGitignore(root, dir, root.Name())...)
		}
	}

	var walkErr error
	var walk func(string, string, bool) bool
	walk = func(displayDir, name string, top bool) bool {
		if ctx.Err() != nil {
			return false
		}
		base := len(patterns)
		patterns = append(patterns, readGitignore(root, name, root.Name())...)
		defer func() {
			patterns = patterns[:base]
		}()

		dir, err := root.Open(name)
		if err != nil {
			if top {
				walkErr = accessError(err, displayDir)
				return false
			}
			return true
		}
		matcher := gitignore.NewMatcher(patterns)
		for {
			entries, readErr := dir.ReadDir(256)
			for _, entry := range entries {
				if ctx.Err() != nil {
					_ = dir.Close()
					return false
				}
				entryName := entry.Name()
				if strings.HasPrefix(entryName, ".") {
					continue
				}
				childName := filepath.Join(name, entryName)
				childDisplay := joinDisplay(displayDir, entryName)
				isDir := entry.IsDir()
				if matcher.Match(pathComponents(filepath.Join(root.Name(), childName)), isDir) {
					continue
				}
				if isDir {
					if !walk(childDisplay, childName, false) {
						_ = dir.Close()
						return false
					}
					continue
				}
				if entry.Type().IsRegular() &&
					!yield(WalkedFile{root: root, name: childName, Display: childDisplay}) {
					_ = dir.Close()
					return false
				}
			}
			if errors.Is(readErr, io.EOF) {
				break
			}
			if readErr != nil {
				_ = dir.Close()
				if top {
					walkErr = accessError(readErr, displayDir)
					return false
				}
				return true
			}
			if ctx.Err() != nil {
				_ = dir.Close()
				return false
			}
		}
		_ = dir.Close()
		return true
	}
	walk(display, start, true)
	if walkErr != nil {
		return walkErr
	}
	return ctx.Err()
}

func ancestorNames(dir string) []string {
	if dir == "." || dir == "" {
		return []string{"."}
	}
	var names []string
	for dir != "." && dir != "" {
		names = append([]string{dir}, names...)
		dir = filepath.Dir(dir)
	}
	return append([]string{"."}, names...)
}

func joinDisplay(dir, name string) string {
	if strings.HasSuffix(dir, "/") {
		return dir + name
	}
	return dir + "/" + name
}

func pathComponents(path string) []string {
	clean := filepath.ToSlash(filepath.Clean(path))
	if clean == "." || clean == "" {
		return nil
	}
	return strings.Split(strings.TrimPrefix(clean, "/"), "/")
}

func readGitignore(root *os.Root, dir, domainRoot string) []gitignore.Pattern {
	file, err := root.Open(filepath.Join(dir, ".gitignore"))
	if err != nil {
		return nil
	}
	defer func() {
		_ = file.Close()
	}()
	return parseGitignore(file, pathComponents(filepath.Join(domainRoot, dir)))
}

func readHostAncestorGitignores(root string) []gitignore.Pattern {
	var ancestors []string
	for dir := filepath.Dir(root); ; dir = filepath.Dir(dir) {
		ancestors = append([]string{dir}, ancestors...)
		if dir == filepath.Dir(dir) {
			break
		}
	}
	var patterns []gitignore.Pattern
	for _, dir := range ancestors {
		file, err := os.Open(filepath.Join(dir, ".gitignore"))
		if err != nil {
			continue
		}
		patterns = append(patterns, parseGitignore(file, pathComponents(dir))...)
		_ = file.Close()
	}
	return patterns
}

func parseGitignore(file *os.File, domain []string) []gitignore.Pattern {
	var patterns []gitignore.Pattern
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#") || strings.TrimRight(line, " ") == "" {
			continue
		}
		patterns = append(patterns, gitignore.ParsePattern(line, domain))
	}
	return patterns
}

func RelativeTo(root, entry string) string {
	if entry == root {
		return ""
	}
	if rest, ok := strings.CutPrefix(entry, strings.TrimSuffix(root, "/")+"/"); ok {
		return rest
	}
	return entry
}
