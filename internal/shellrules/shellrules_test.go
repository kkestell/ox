package shellrules

import "testing"

func TestAllowed(t *testing.T) {
	tests := []struct {
		name    string
		rules   []string
		command string
		want    bool
	}{
		{"exact word", []string{"ls"}, "ls -la /tmp", true},
		{"not a command prefix", []string{"ls"}, "lsof", false},
		{"multiword prefix", []string{"git status"}, "git status --short", true},
		{"short call", []string{"git status"}, "git", false},
		{"other subcommand", []string{"git status"}, "git push", false},
		{"wildcard word", []string{"go *"}, "go build", true},
		{"missing wildcard word", []string{"go *"}, "go", false},
		{"wildcard covers expansion", []string{"go *"}, "go $TARGET", true},
		{"quoted literal", []string{"git status"}, `git "status"`, true},
		{"single quoted literal", []string{"git status"}, `git 'status'`, true},
		{"constrained expansion", []string{"git status"}, "git $SUB", false},
		{"expanded command", []string{"git status"}, "$GIT status", false},
		{"assignment prefix", []string{"git status"}, "PATH=/tmp git status", false},
		{"loader assignment", []string{"command"}, "LD_PRELOAD=./evil.so command", false},
		{"bare assignment", []string{"ls"}, "FOO=1", false},
		{"matched pipeline", []string{"cat", "grep", "wc"}, "cat a | grep foo | wc -l", true},
		{"unmatched pipeline", []string{"git status"}, "git status | grep x", false},
		{"hidden substitution", []string{"echo"}, "echo $(rm -rf /)", false},
		{"matched substitution", []string{"echo"}, "echo $(echo hi)", true},
		{"subshell", []string{"ls", "pwd"}, "(ls && pwd)", true},
		{"loop body", []string{"echo"}, "for f in *; do echo x; done", true},
		{"unmatched loop body", []string{"echo"}, "for f in *; do rm x; done", false},
		{"function body", []string{"ls"}, "f() { rm x; }; ls", false},
		{"redirect", []string{"grep"}, "grep foo f > out.txt", true},
		{"background", []string{"ls"}, "ls &", true},
		{"quoted glob", []string{"find . -name *.go"}, "find . -name '*.go'", true},
		{"unquoted glob", []string{"find . -name *.go"}, "find . -name *.go", false},
		{"parse error", []string{"ls"}, "ls ((", false},
		{"test bashism", []string{"test"}, "[[ -f x ]]", false},
		{"process substitution", []string{"diff"}, "diff <(ls) <(pwd)", false},
		{"empty", []string{"ls"}, "", false},
		{"blank", []string{"ls"}, "   ", false},
		{"no rules", nil, "ls", false},
		{"blank rule", []string{""}, "ls", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := Allowed(test.rules, test.command); got != test.want {
				t.Fatalf("Allowed(%q, %q) = %v, want %v", test.rules, test.command, got, test.want)
			}
		})
	}
}

func TestSuggest(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    string
	}{
		{"subcommand", "go test ./...", "go test"},
		{"flags only", "ls -la", "ls"},
		{"subcommand with flags", "git commit -m x", "git commit"},
		{"assignment prefix", "FOO=1 go test", "go test"},
		{"single word", "pwd", "pwd"},
		{"shell", "sh -c 'rm -rf /'", ""},
		{"shell by path", "/bin/bash -lc 'rm -rf /'", ""},
		{"language runtime", "python3 -c 'import os'", ""},
		{"wrapper", "env FOO=1 rm -rf /", ""},
		{"privilege wrapper", "sudo rm -rf /", ""},
		{"wildcard program", "* --version", ""},
		{"wildcard argument", "rm *.txt", ""},
		{"expanded program", "$TOOL build", ""},
		{"expanded argument", "grep foo $FILE", ""},
		{"substituted argument", "echo $(rm -rf /)", ""},
		{"parse failure", "ls ((", ""},
		{"empty", "", ""},
		{"blank", "   ", ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := Suggest(test.command); got != test.want {
				t.Fatalf("Suggest(%q) = %q, want %q", test.command, got, test.want)
			}
		})
	}
}
