package e2e

import "testing"

// developmentVersion is what a build without link-time injection reports. It is
// the development default declared by cmd/ox; a release replaces that value
// with the tag it was built from.
const developmentVersion = "devel"

// TestVersionReportsBuildVersion pins the informational output contract: one
// conventional line on stdout, nothing on stderr, and a successful exit.
func TestVersionReportsBuildVersion(t *testing.T) {
	result := runCommand(t, "", []string{"--version"})
	if result.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr = %q", result.ExitCode, result.Stderr)
	}
	if result.Stdout != "ox "+developmentVersion+"\n" {
		t.Errorf("stdout = %q, want %q", result.Stdout, "ox "+developmentVersion+"\n")
	}
	if result.Stderr != "" {
		t.Errorf("stderr = %q, want no output", result.Stderr)
	}
}

// TestVersionNeedsNoRuntimePrerequisites proves that reporting the version
// happens before configuration, credentials, and provider work: the settings
// file is invalid, no credential is available, there is no home directory, and
// stdio is empty.
func TestVersionNeedsNoRuntimePrerequisites(t *testing.T) {
	result := runCommand(t, "", []string{"--version"},
		withGlobalConfig("{ not valid settings"),
		withCredential(""),
		withEnvironment("HOME", ""),
	)
	if result.ExitCode != 0 {
		t.Fatalf("exit code = %d, stderr = %q", result.ExitCode, result.Stderr)
	}
	if result.Stdout != "ox "+developmentVersion+"\n" {
		t.Errorf("stdout = %q, want %q", result.Stdout, "ox "+developmentVersion+"\n")
	}
	if result.Stderr != "" {
		t.Errorf("stderr = %q, want no output", result.Stderr)
	}
}

// TestVersionKeepsUsageErrors checks that a version request does not turn a
// malformed command line into a successful exit.
func TestVersionKeepsUsageErrors(t *testing.T) {
	for _, arguments := range [][]string{
		{"--version", "unknown"},
		{"--version", "login"},
		{"--version", "one", "two"},
		{"--version=maybe"},
	} {
		result := runCommand(t, "", arguments)
		if result.ExitCode != 2 {
			t.Errorf("ox %v: exit code = %d, stderr = %q", arguments, result.ExitCode, result.Stderr)
		}
		if result.Stdout != "" {
			t.Errorf("ox %v: stdout = %q, want no output", arguments, result.Stdout)
		}
	}
}
