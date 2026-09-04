package agent

import "testing"

func TestFileReadScopesIsolateEvidenceAndClearTogether(t *testing.T) {
	value := &session{}
	parent := value.primaryFileReads()
	first, releaseFirst := value.childFileReads()
	defer releaseFirst()
	second, releaseSecond := value.childFileReads()
	defer releaseSecond()

	parent.Record("parent", "one")
	first.Record("shared", "old")
	first.Record("first", "one")
	second.Record("shared", "old")
	second.Record("second", "two")
	first.Record("shared", "new")

	if hash, _ := second.Hash("shared"); hash != "old" {
		t.Fatalf("sibling read evidence = %q, want old", hash)
	}
	if _, exists := parent.Hash("first"); exists {
		t.Fatal("parent observed child read evidence")
	}

	first.Clear()
	for name, reads := range map[string]FileReads{
		"parent": parent,
		"first":  first,
		"second": second,
	} {
		if _, exists := reads.Hash("shared"); exists {
			t.Fatalf("%s scope survived global clear", name)
		}
	}
}
