package server

import (
	"crypto/rand"
	"encoding/binary"
	"math/bits"
	"slices"
	"strings"
)

const idAlphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

// maxIDLength is the longest base58 form of 8 bytes.
const maxIDLength = 11

// newID returns 8 random bytes written in Bitcoin's base58, the form of every
// ID Ox makes for its own records.
func newID() string {
	var b [8]byte
	rand.Read(b[:])
	return encodeID(binary.BigEndian.Uint64(b[:]))
}

// encodeID writes the 8 big-endian bytes of n as Bitcoin's base58 does: each
// leading zero byte is a "1", and the rest is n in base 58.
func encodeID(n uint64) string {
	out := []byte(strings.Repeat("1", bits.LeadingZeros64(n)/8))
	var digits []byte
	for ; n > 0; n /= 58 {
		digits = append(digits, idAlphabet[n%58])
	}
	slices.Reverse(digits)
	return string(append(out, digits...))
}

// isID reports whether id is the canonical base58 form of 8 bytes, the form of
// an ID a client chooses. A value over 64 bits wraps, and the round-trip
// rejects it along with every other non-canonical form.
func isID(id string) bool {
	if len(id) > maxIDLength {
		return false
	}
	var n uint64
	for _, c := range id {
		d := strings.IndexRune(idAlphabet, c)
		if d < 0 {
			return false
		}
		n = n*58 + uint64(d)
	}
	return encodeID(n) == id
}
