// Package keyring keeps the OpenRouter API key in the system keyring: the
// macOS keychain through `security`, or the Secret Service through
// `secret-tool` elsewhere.
package keyring

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

const (
	service = "ox"
	account = "OPENROUTER_API_KEY"
)

// macOSNotFound is the exit status `security` returns for a missing item.
const macOSNotFound = 44

// Get returns the saved key, or "" when none is saved.
func Get() (string, error) {
	var cmd *exec.Cmd
	if runtime.GOOS == "darwin" {
		cmd = exec.Command("security", "find-generic-password", "-s", service, "-a", account, "-w")
	} else {
		cmd = exec.Command("secret-tool", "lookup", "service", service, "account", account)
	}
	output, err := cmd.Output()
	if missing(err) {
		return "", nil
	}
	if err != nil {
		return "", failure(err)
	}
	return strings.TrimRight(string(output), "\n"), nil
}

// Set saves the key, replacing any saved key.
func Set(key string) error {
	var cmd *exec.Cmd
	if runtime.GOOS == "darwin" {
		// `security -i` reads the command from stdin, which keeps the key
		// out of the process arguments.
		quoted := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(key)
		cmd = exec.Command("security", "-i")
		cmd.Stdin = strings.NewReader(fmt.Sprintf("add-generic-password -U -s %s -a %s -w \"%s\"\n", service, account, quoted))
	} else {
		cmd = exec.Command("secret-tool", "store", "--label=ox OpenRouter API key", "service", service, "account", account)
		cmd.Stdin = strings.NewReader(key)
	}
	if err := cmd.Run(); err != nil {
		return failure(err)
	}
	return nil
}

// Delete removes the saved key and reports whether one was saved.
func Delete() (bool, error) {
	if runtime.GOOS != "darwin" {
		saved, err := Get()
		if err != nil || saved == "" {
			return false, err
		}
		return true, exec.Command("secret-tool", "clear", "service", service, "account", account).Run()
	}
	err := exec.Command("security", "delete-generic-password", "-s", service, "-a", account).Run()
	if missing(err) {
		return false, nil
	}
	if err != nil {
		return false, failure(err)
	}
	return true, nil
}

func missing(err error) bool {
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		return false
	}
	// secret-tool lookup exits 1 with no output for a missing item.
	return exit.ExitCode() == macOSNotFound || runtime.GOOS != "darwin" && exit.ExitCode() == 1 && len(bytes.TrimSpace(exit.Stderr)) == 0
}

func failure(err error) error {
	var exit *exec.ExitError
	if errors.As(err, &exit) && len(exit.Stderr) > 0 {
		return fmt.Errorf("could not access the system keyring: %s", strings.TrimSpace(string(exit.Stderr)))
	}
	return fmt.Errorf("could not access the system keyring: %w", err)
}
