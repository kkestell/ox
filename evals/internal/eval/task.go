package eval

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

const TaskSchemaVersion = 1

type Task struct {
	Schema   int     `json:"schema"`
	ID       string  `json:"id"`
	Budget   Budget  `json:"budget"`
	Phases   []Phase `json:"phases"`
	Success  Success `json:"success"`
	path     string
	revision string
}

type Budget struct {
	TimeoutMS        int `json:"timeout_ms"`
	ProviderRequests int `json:"provider_requests"`
}

type Phase struct {
	Action        string `json:"action"`
	Prompt        string `json:"prompt,omitempty"`
	Permission    string `json:"permission,omitempty"`
	CancelAfterMS int    `json:"cancel_after_ms,omitempty"`
	ExpectedStop  string `json:"expected_stop,omitempty"`
	Overlay       string `json:"overlay,omitempty"`
}

type Success struct {
	Files                       map[string]string `json:"files,omitempty"`
	Command                     []string          `json:"command,omitempty"`
	Overlay                     string            `json:"overlay,omitempty"`
	MinimumPermissionRejections int               `json:"minimum_permission_rejections,omitempty"`
}

func LoadTask(path string) (Task, error) {
	raw, err := os.ReadFile(filepath.Join(path, "task.json"))
	if err != nil {
		return Task{}, fmt.Errorf("read task manifest: %w", err)
	}
	var task Task
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&task); err != nil {
		return Task{}, fmt.Errorf("decode task manifest: %w", err)
	}
	if err := validateTask(task); err != nil {
		return Task{}, err
	}
	if err := validateMutationFixtures(path, task); err != nil {
		return Task{}, err
	}
	revision, err := taskRevision(path)
	if err != nil {
		return Task{}, err
	}
	task.path = path
	task.revision = revision
	return task, nil
}

func validateTask(task Task) error {
	if task.Schema != TaskSchemaVersion {
		return fmt.Errorf("task schema = %d, want %d", task.Schema, TaskSchemaVersion)
	}
	if task.ID == "" || strings.ContainsAny(task.ID, `/\\`) {
		return errors.New("task id must be a nonempty path-free name")
	}
	if task.Budget.TimeoutMS <= 0 || task.Budget.ProviderRequests <= 0 {
		return errors.New("task budget must have positive timeout_ms and provider_requests")
	}
	if len(task.Phases) == 0 {
		return errors.New("task must have at least one phase")
	}
	seenPrompt := false
	for index, phase := range task.Phases {
		switch phase.Action {
		case "prompt":
			seenPrompt = true
			if strings.TrimSpace(phase.Prompt) == "" {
				return fmt.Errorf("phase %d prompt is empty", index+1)
			}
			if phase.Permission != "allow" && phase.Permission != "deny" {
				return fmt.Errorf("phase %d permission must be allow or deny", index+1)
			}
		case "restart":
			if !seenPrompt {
				return fmt.Errorf("phase %d cannot restart before a prompt", index+1)
			}
		case "mutate":
			if !seenPrompt {
				return fmt.Errorf("phase %d cannot mutate before a prompt", index+1)
			}
			if index+1 == len(task.Phases) || task.Phases[index+1].Action != "prompt" {
				return fmt.Errorf("phase %d mutation must be followed by a prompt", index+1)
			}
			if err := validateRelativePath(phase.Overlay); err != nil {
				return fmt.Errorf("phase %d mutation overlay: %w", index+1, err)
			}
		default:
			return fmt.Errorf("phase %d has unknown action %q", index+1, phase.Action)
		}
	}
	if len(task.Success.Files) == 0 && len(task.Success.Command) == 0 {
		return errors.New("task success must declare files or a command")
	}
	if len(task.Success.Command) > 0 && strings.TrimSpace(task.Success.Command[0]) == "" {
		return errors.New("task success command executable is empty")
	}
	if task.Success.MinimumPermissionRejections < 0 {
		return errors.New("task success minimum_permission_rejections must not be negative")
	}
	for path := range task.Success.Files {
		if err := validateRelativePath(path); err != nil {
			return fmt.Errorf("success file %q: %w", path, err)
		}
	}
	if task.Success.Overlay != "" {
		if err := validateRelativePath(task.Success.Overlay); err != nil {
			return fmt.Errorf("success overlay: %w", err)
		}
	}
	return nil
}

func validateMutationFixtures(root string, task Task) error {
	for index, phase := range task.Phases {
		if phase.Action != "mutate" {
			continue
		}
		path := filepath.Join(root, filepath.FromSlash(phase.Overlay))
		info, err := os.Lstat(path)
		if err != nil {
			return fmt.Errorf("phase %d mutation overlay: %w", index+1, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("phase %d mutation overlay must be a directory", index+1)
		}
	}
	return nil
}

func validateRelativePath(path string) error {
	if path == "" || strings.Contains(path, "\\") || filepath.IsAbs(path) || filepath.Clean(path) != path || path == ".." || strings.HasPrefix(path, ".."+string(filepath.Separator)) {
		return errors.New("path must be clean and relative")
	}
	return nil
}

func taskRevision(root string) (string, error) {
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("task contains symlink %s", path)
		}
		if !entry.IsDir() {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("inspect task: %w", err)
	}
	sort.Strings(paths)
	hash := sha256.New()
	for _, path := range paths {
		relative, _ := filepath.Rel(root, path)
		_, _ = io.WriteString(hash, filepath.ToSlash(relative))
		_, _ = hash.Write([]byte{0})
		file, err := os.Open(path)
		if err != nil {
			return "", fmt.Errorf("open task file %s: %w", relative, err)
		}
		_, copyErr := io.Copy(hash, file)
		closeErr := file.Close()
		if err := errors.Join(copyErr, closeErr); err != nil {
			return "", fmt.Errorf("hash task file %s: %w", relative, err)
		}
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func seedWorkspace(task Task, destination string) error {
	source := filepath.Join(task.path, "workspace")
	if _, err := os.Stat(source); errors.Is(err, os.ErrNotExist) {
		return os.MkdirAll(destination, 0o755)
	} else if err != nil {
		return fmt.Errorf("inspect task workspace: %w", err)
	}
	return copyTree(source, destination)
}

func copyTree(source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("refuse symlink %s", path)
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
		if err != nil {
			_ = input.Close()
			return err
		}
		_, copyErr := io.Copy(output, input)
		return errors.Join(copyErr, input.Close(), output.Close())
	})
}

func verifyTask(ctx context.Context, task Task, workspace string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("verifier deadline: %w", err)
	}
	if task.Success.Overlay != "" {
		if err := copyOverlay(filepath.Join(task.path, task.Success.Overlay), workspace); err != nil {
			return "", fmt.Errorf("install verifier overlay: %w", err)
		}
	}
	for path, want := range task.Success.Files {
		target, err := confinedWorkspacePath(workspace, path)
		if err != nil {
			return "", fmt.Errorf("expected file %s: %w", path, err)
		}
		info, err := os.Lstat(target)
		if err != nil {
			return "", fmt.Errorf("inspect expected file %s: %w", path, err)
		}
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("expected file %s is not a regular file", path)
		}
		raw, err := os.ReadFile(target)
		if err != nil {
			return "", fmt.Errorf("read expected file %s: %w", path, err)
		}
		if string(raw) != want {
			return "", fmt.Errorf("file %s did not match expected content", path)
		}
	}
	if len(task.Success.Command) == 0 {
		return "", nil
	}
	command := exec.CommandContext(ctx, task.Success.Command[0], task.Success.Command[1:]...)
	command.Dir = workspace
	command.Env = append(sanitizedEnvironment(), "HOME="+workspace)
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		return string(output), fmt.Errorf("verifier timed out: %w", ctx.Err())
	}
	if err != nil {
		return string(output), fmt.Errorf("verifier failed: %w", err)
	}
	return string(output), nil
}

func copyOverlay(source, workspace string) error {
	if _, err := os.Stat(source); err != nil {
		return err
	}
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("refuse symlink %s", path)
		}
		target, err := confinedWorkspacePath(workspace, filepath.ToSlash(relative))
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if info, err := os.Lstat(target); err == nil && info.Mode()&os.ModeSymlink != 0 {
			if err := os.Remove(target); err != nil {
				return err
			}
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return os.WriteFile(target, raw, 0o600)
	})
}

func confinedWorkspacePath(workspace, relative string) (string, error) {
	if err := validateRelativePath(relative); err != nil {
		return "", err
	}
	current := workspace
	parts := strings.Split(filepath.FromSlash(relative), string(filepath.Separator))
	for _, part := range parts[:len(parts)-1] {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return "", fmt.Errorf("workspace path crosses non-directory %s", current)
		}
	}
	return filepath.Join(workspace, filepath.FromSlash(relative)), nil
}
