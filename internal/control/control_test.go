package control

import "testing"

func TestEscapePrintsControlCharactersAsText(t *testing.T) {
	got := Escape("\x1b[2J\r\x07\u009b31m\n\t")
	if want := `\u{1b}[2J\r\u{7}\u{9b}31m` + "\n\t"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
