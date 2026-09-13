package agent

import "testing"

// TestFileReadEvidenceClearsOnAWrite covers the rule a write depends on: once a
// mutation invalidates the session's read evidence, no earlier read can still
// authorize a change.
func TestFileReadEvidenceClearsOnAWrite(t *testing.T) {
	value := &session{}
	reads := value.primaryFileReads()

	reads.Record("notes.txt", "one")
	reads.Record("other.txt", "two")
	if hash, ok := reads.Hash("notes.txt"); !ok || hash != "one" {
		t.Fatalf("recorded evidence = %q, %v", hash, ok)
	}

	reads.Clear()
	for _, key := range []string{"notes.txt", "other.txt"} {
		if _, exists := reads.Hash(key); exists {
			t.Fatalf("%s survived the clear", key)
		}
	}
}
