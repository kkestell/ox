package main

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBinaryExitsCleanlyOnStdinEOF(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "ox")
	build := exec.Command("go", "build", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build ox: %v\n%s", err, output)
	}

	for _, logLevel := range []string{"", "unrecognized"} {
		t.Run(logLevel, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			command := exec.Command(binary)
			command.Env = []string{"OX_LOG_LEVEL=" + logLevel}
			command.Stdin = strings.NewReader("")
			command.Stdout = &stdout
			command.Stderr = &stderr

			if err := command.Run(); err != nil {
				t.Fatalf("run ox: %v", err)
			}
			if stdout.Len() != 0 {
				t.Errorf("stdout = %q, want empty", stdout.String())
			}
			if !strings.Contains(stderr.String(), `level=INFO msg="ox starting"`) {
				t.Errorf("stderr = %q, want info startup log", stderr.String())
			}
		})
	}
}
