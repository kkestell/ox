package lsp

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPositionEncodingRoundTrip(t *testing.T) {
	text := "a😀éz\n"
	for _, encoding := range []encodingKind{encodingUTF8, encodingUTF16, encodingUTF32} {
		for column := 1; column <= 5; column++ {
			wire, err := encodePosition(text, Position{Line: 1, Column: column}, encoding)
			if err != nil {
				t.Fatalf("encode %s column %d: %v", encoding, column, err)
			}
			got, err := decodePosition(text, wire, encoding)
			if err != nil {
				t.Fatalf("decode %s column %d: %v", encoding, column, err)
			}
			if got != (Position{Line: 1, Column: column}) {
				t.Fatalf("%s column %d round trip = %#v", encoding, column, got)
			}
		}
	}
	utf8Position, _ := encodePosition(text, Position{Line: 1, Column: 3}, encodingUTF8)
	utf16Position, _ := encodePosition(text, Position{Line: 1, Column: 3}, encodingUTF16)
	utf32Position, _ := encodePosition(text, Position{Line: 1, Column: 3}, encodingUTF32)
	if utf8Position.Character != 5 || utf16Position.Character != 3 || utf32Position.Character != 2 {
		t.Fatalf("encoded positions = %#v %#v %#v", utf8Position, utf16Position, utf32Position)
	}
	if _, err := decodePosition(text, wirePosition{Line: 0, Character: 2}, encodingUTF8); err == nil {
		t.Fatal("UTF-8 position splitting a rune was accepted")
	}
	if _, err := encodePosition(text, Position{Line: 1, Column: 6}, encodingUTF16); err == nil {
		t.Fatal("out-of-range model column was accepted")
	}
}

func TestFileURIsAndWorkspaceConfinement(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "with space", "héllo.go")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("package hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	uri := pathToURI(target)
	got, ok := uriToPath(uri)
	if !ok || got != target {
		t.Fatalf("URI round trip = %q, %v; URI %q", got, ok, uri)
	}
	for _, invalid := range []string{"https://example.com/a.go", "file://remote/a.go", "file:///bad%ZZ"} {
		if _, ok := uriToPath(invalid); ok {
			t.Fatalf("accepted invalid URI %q", invalid)
		}
	}
	manager, err := New(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	abs, rel, ok := manager.resolveURI(uri)
	canonicalTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || abs != canonicalTarget || rel != "with space/héllo.go" {
		t.Fatalf("resolved URI = %q, %q, %v", abs, rel, ok)
	}
	outside := filepath.Join(t.TempDir(), "outside.go")
	if err := os.WriteFile(outside, []byte("package outside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := manager.resolveURI(pathToURI(outside)); ok {
		t.Fatal("outside URI was accepted")
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape.go")); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := manager.resolveURI(pathToURI(filepath.Join(root, "escape.go"))); ok {
		t.Fatal("symlink escape URI was accepted")
	}
}

func TestDefinitionValidationAndDeterministicRouting(t *testing.T) {
	root := t.TempDir()
	if _, err := New(root, []Definition{{Name: "", Command: "x", Extensions: []string{"go"}}}); err == nil {
		t.Fatal("blank name was accepted")
	}
	if _, err := New(root, []Definition{{Name: "x", Command: "", Extensions: []string{"go"}}}); err == nil {
		t.Fatal("blank command was accepted")
	}
	if _, err := New(root, []Definition{{Name: "x", Command: "x"}}); err == nil {
		t.Fatal("empty extensions were accepted")
	}
	if _, err := New(root, []Definition{
		{Name: "z", Command: "z", Extensions: []string{".GO"}},
		{Name: "a", Command: "a", Extensions: []string{"go"}},
	}); err == nil {
		t.Fatal("overlapping extensions were accepted")
	}
	manager, err := New(root, []Definition{
		{Name: "z", Command: "z", Extensions: []string{"rs"}},
		{Name: "a", Command: "a", Extensions: []string{"go"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	if manager.servers[0].definition.Name != "a" || manager.byExt["go"] != manager.servers[0] {
		t.Fatalf("servers not sorted/routed deterministically: %#v", manager.servers)
	}
}
