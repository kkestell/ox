// Package socket locates the Unix socket that oxd serves and ox connects to.
package socket

import (
	"os"
	"path/filepath"
)

// Path returns oxd.sock in the ox directory of the user cache directory,
// creating the directory.
func Path() (string, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(cache, "ox")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "oxd.sock"), nil
}
