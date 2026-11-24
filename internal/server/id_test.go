package server

import (
	"encoding/binary"
	"encoding/hex"
	"strings"
	"testing"
)

var idVectors = map[string]string{
	"0000000000000000": "11111111",
	"0000000000000001": "11111112",
	"00ff000000000000": "1AfUXwLfh7d",
	"0123456789abcdef": "C3CPq7c8PY",
	"ffffffffffffffff": "jpXCZedGfVQ",
}

func TestIDEncoding(t *testing.T) {
	for in, want := range idVectors {
		b, _ := hex.DecodeString(in)
		if got := encodeID(binary.BigEndian.Uint64(b)); got != want {
			t.Errorf("%s encoded as %s, not %s", in, got, want)
		}
		if !isID(want) {
			t.Errorf("%s was refused", want)
		}
	}
	if id := newID(); !isID(id) {
		t.Errorf("a new ID %q was refused", id)
	}
	for _, id := range []string{
		"", "12", "0jpXCZedGf", "ljpXCZedGf", "jpXCZedGfVR", "111111111",
		strings.Repeat("a", 32), "00112233445566778899aabbccddeeff",
	} {
		if isID(id) {
			t.Errorf("%q was accepted", id)
		}
	}
}
