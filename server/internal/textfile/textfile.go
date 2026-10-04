// Package textfile reads the small UTF-8 files Ox loads: workspace
// instructions, skill definitions, and settings.
package textfile

import (
	"errors"
	"fmt"
	"io"
	"os"
	"unicode/utf8"
)

// MaxBytes is the largest file Read accepts.
const MaxBytes = 32 * 1024

// Read reads a UTF-8 text file of at most MaxBytes.
func Read(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, MaxBytes+1))
	if err != nil {
		return "", err
	}
	if len(data) > MaxBytes {
		return "", fmt.Errorf("larger than %d KiB", MaxBytes/1024)
	}
	if !utf8.Valid(data) {
		return "", errors.New("not UTF-8 text")
	}
	return string(data), nil
}
