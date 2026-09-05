// Package skills discovers and loads Agent Skills confined to one workspace.
package skills

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"syscall"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

const (
	CatalogDir      = ".agents/skills"
	MaxFileBytes    = 64 << 10
	MaxCatalogBytes = 64 << 10
	MaxSkills       = 128
)

const skillFile = "SKILL.md"

var namePattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// Reference is the activation-frozen metadata and file identity for one skill.
type Reference struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Path        string `json:"path"`
	Digest      string `json:"digest"`
}

type frontmatter struct {
	Name          string            `yaml:"name"`
	Description   string            `yaml:"description"`
	License       *string           `yaml:"license"`
	Compatibility *string           `yaml:"compatibility"`
	Metadata      map[string]string `yaml:"metadata"`
	AllowedTools  *string           `yaml:"allowed-tools"`
}

// Discover returns valid skills in name order. Errors for individual skills
// are warnings; an unusable catalog root or an oversized valid catalog fails.
func Discover(workspace string) ([]Reference, []error, error) {
	root, err := os.OpenRoot(workspace)
	if err != nil {
		return nil, nil, fmt.Errorf("open workspace skills root: %w", err)
	}
	defer func() { _ = root.Close() }()

	missing, err := validateDirectory(root, ".agents")
	if err != nil {
		return nil, nil, catalogError(workspace, err)
	}
	if missing {
		return nil, nil, nil
	}
	missing, err = validateDirectory(root, CatalogDir)
	if err != nil {
		return nil, nil, catalogError(workspace, err)
	}
	if missing {
		return nil, nil, nil
	}

	catalogRoot, err := root.OpenRoot(filepath.FromSlash(CatalogDir))
	if err != nil {
		return nil, nil, catalogError(workspace, err)
	}
	defer func() { _ = catalogRoot.Close() }()
	directory, err := catalogRoot.Open(".")
	if err != nil {
		return nil, nil, catalogError(workspace, err)
	}
	entries, readErr := directory.Readdir(-1)
	closeErr := directory.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return nil, nil, catalogError(workspace, err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })

	var references []Reference
	var warnings []error
	for _, entry := range entries {
		if entry.Mode()&os.ModeSymlink != 0 {
			warnings = append(warnings, skillError(workspace, entry.Name(), errors.New("symbolic links are not allowed")))
			continue
		}
		if !entry.IsDir() {
			continue
		}
		reference, err := discoverOne(catalogRoot, workspace, entry.Name())
		if err != nil {
			warnings = append(warnings, err)
			continue
		}
		references = append(references, reference)
	}
	sort.Slice(references, func(i, j int) bool { return references[i].Name < references[j].Name })
	if len(references) > MaxSkills {
		return nil, warnings, fmt.Errorf(
			"workspace skill catalog has %d valid skills; maximum is %d",
			len(references), MaxSkills,
		)
	}
	return references, warnings, nil
}

// Load rereads a frozen reference and returns its Markdown instructions only
// when the complete SKILL.md still has the activation-time identity.
func Load(workspace string, reference Reference) (string, error) {
	if err := validateReference(reference); err != nil {
		return "", fmt.Errorf("invalid frozen skill %q: %w", reference.Name, err)
	}
	root, err := os.OpenRoot(workspace)
	if err != nil {
		return "", changedError(reference, err)
	}
	defer func() { _ = root.Close() }()
	data, err := readSkill(root, reference.Name)
	if err != nil {
		return "", changedError(reference, err)
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != reference.Digest {
		return "", changedError(reference, errors.New("file contents changed"))
	}
	metadata, body, err := parse(data, reference.Name)
	if err != nil {
		return "", changedError(reference, err)
	}
	if metadata.Name != reference.Name || metadata.Description != reference.Description {
		return "", changedError(reference, errors.New("metadata changed"))
	}
	return string(body), nil
}

// ValidateReferences validates the durable representation of a catalog.
func ValidateReferences(references []Reference) error {
	if len(references) > MaxSkills {
		return fmt.Errorf("skill catalog has %d entries; maximum is %d", len(references), MaxSkills)
	}
	for index, reference := range references {
		if err := validateReference(reference); err != nil {
			return fmt.Errorf("skill %d: %w", index+1, err)
		}
		if index > 0 && references[index-1].Name >= reference.Name {
			return errors.New("skill catalog is not in unique name order")
		}
	}
	return nil
}

func discoverOne(catalogRoot *os.Root, workspace, name string) (Reference, error) {
	info, err := catalogRoot.Lstat(name)
	if err != nil {
		return Reference{}, skillError(workspace, name, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return Reference{}, skillError(workspace, name, errors.New("symbolic links are not allowed"))
	}
	if !info.IsDir() {
		return Reference{}, skillError(workspace, name, errors.New("not a directory"))
	}
	skillRoot, err := catalogRoot.OpenRoot(name)
	if err != nil {
		return Reference{}, skillError(workspace, name, err)
	}
	defer func() { _ = skillRoot.Close() }()
	data, err := readBoundedRegular(skillRoot, skillFile)
	if err != nil {
		return Reference{}, skillError(workspace, name, err)
	}
	metadata, _, err := parse(data, name)
	if err != nil {
		return Reference{}, skillError(workspace, name, err)
	}
	sum := sha256.Sum256(data)
	return Reference{
		Name:        metadata.Name,
		Description: metadata.Description,
		Path:        path.Join(CatalogDir, name, skillFile),
		Digest:      hex.EncodeToString(sum[:]),
	}, nil
}

func readSkill(root *os.Root, name string) ([]byte, error) {
	for _, directory := range []string{".agents", CatalogDir, path.Join(CatalogDir, name)} {
		missing, err := validateDirectory(root, filepath.FromSlash(directory))
		if err != nil {
			return nil, err
		}
		if missing {
			return nil, fs.ErrNotExist
		}
	}
	skillRoot, err := root.OpenRoot(filepath.FromSlash(path.Join(CatalogDir, name)))
	if err != nil {
		return nil, err
	}
	defer func() { _ = skillRoot.Close() }()
	return readBoundedRegular(skillRoot, skillFile)
}

func validateDirectory(root *os.Root, name string) (bool, error) {
	info, err := root.Lstat(name)
	if errors.Is(err, fs.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return false, errors.New("symbolic links are not allowed")
	}
	if !info.IsDir() {
		return false, errors.New("not a directory")
	}
	return false, nil
}

func readBoundedRegular(root *os.Root, name string) ([]byte, error) {
	info, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("symbolic links are not allowed")
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("not a regular file")
	}
	file, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	opened, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !opened.Mode().IsRegular() {
		return nil, errors.New("not a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(file, MaxFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxFileBytes {
		return nil, fmt.Errorf("file exceeds %d bytes", MaxFileBytes)
	}
	if !utf8.Valid(data) {
		return nil, errors.New("file is not valid UTF-8")
	}
	return data, nil
}

func parse(data []byte, directoryName string) (frontmatter, []byte, error) {
	yamlData, body, err := splitFrontmatter(data)
	if err != nil {
		return frontmatter{}, nil, err
	}
	var metadata frontmatter
	if err := yaml.Unmarshal(yamlData, &metadata); err != nil {
		return frontmatter{}, nil, fmt.Errorf("parse YAML frontmatter: %w", err)
	}
	if !namePattern.MatchString(metadata.Name) || len(metadata.Name) > 64 {
		return frontmatter{}, nil, fmt.Errorf("name %q is invalid", metadata.Name)
	}
	if metadata.Name != directoryName {
		return frontmatter{}, nil, fmt.Errorf(
			"name %q does not match directory name %q", metadata.Name, directoryName,
		)
	}
	if metadata.Description == "" || utf8.RuneCountInString(metadata.Description) > 1024 {
		return frontmatter{}, nil, errors.New("description must contain 1 to 1024 characters")
	}
	if metadata.Compatibility != nil &&
		(*metadata.Compatibility == "" || utf8.RuneCountInString(*metadata.Compatibility) > 500) {
		return frontmatter{}, nil, errors.New("compatibility must contain 1 to 500 characters")
	}
	return metadata, body, nil
}

func splitFrontmatter(data []byte) ([]byte, []byte, error) {
	data = bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf})
	start := 0
	switch {
	case bytes.HasPrefix(data, []byte("---\n")):
		start = 4
	case bytes.HasPrefix(data, []byte("---\r\n")):
		start = 5
	default:
		return nil, nil, errors.New("missing YAML frontmatter")
	}
	for position := start; position <= len(data); {
		end := bytes.IndexByte(data[position:], '\n')
		if end < 0 {
			end = len(data)
		} else {
			end += position
		}
		line := bytes.TrimSuffix(data[position:end], []byte{'\r'})
		if bytes.Equal(line, []byte("---")) {
			bodyStart := end
			if bodyStart < len(data) {
				bodyStart++
			}
			return data[start:position], data[bodyStart:], nil
		}
		if end == len(data) {
			break
		}
		position = end + 1
	}
	return nil, nil, errors.New("unterminated YAML frontmatter")
}

func validateReference(reference Reference) error {
	if !namePattern.MatchString(reference.Name) || len(reference.Name) > 64 {
		return errors.New("invalid name")
	}
	if reference.Description == "" || utf8.RuneCountInString(reference.Description) > 1024 {
		return errors.New("invalid description")
	}
	if reference.Path != path.Join(CatalogDir, reference.Name, skillFile) {
		return errors.New("invalid path")
	}
	digest, err := hex.DecodeString(reference.Digest)
	if err != nil || len(digest) != sha256.Size {
		return errors.New("invalid digest")
	}
	return nil
}

func catalogError(workspace string, err error) error {
	return fmt.Errorf("load workspace skills %s: %w", filepath.Join(workspace, filepath.FromSlash(CatalogDir)), err)
}

func skillError(workspace, name string, err error) error {
	return fmt.Errorf(
		"load workspace skill %s: %w",
		filepath.Join(workspace, filepath.FromSlash(path.Join(CatalogDir, name, skillFile))),
		err,
	)
}

func changedError(reference Reference, err error) error {
	return fmt.Errorf(
		"skill %q at %s changed since activation (%v); reactivate the session",
		reference.Name,
		reference.Path,
		err,
	)
}
