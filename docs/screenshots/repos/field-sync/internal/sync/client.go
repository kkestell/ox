// Package sync uploads survey records to the records API.
package sync

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
)

// Client uploads records to one records API.
type Client struct {
	base   string
	http   *http.Client
	policy Policy
}

// NewClient returns a client for the API at base.
func NewClient(base string) *Client {
	return &Client{base: base, http: http.DefaultClient, policy: DefaultPolicy}
}

// UploadAll uploads every record in dir, retrying each as its policy allows.
func (c *Client) UploadAll(ctx context.Context, dir string) error {
	paths, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return err
	}
	for _, path := range paths {
		if err := Retry(ctx, c.policy, func(ctx context.Context) error { return c.upload(ctx, path) }); err != nil {
			return fmt.Errorf("upload %s: %w", path, err)
		}
	}
	return nil
}

func (c *Client) upload(ctx context.Context, path string) error {
	body, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrPermanent, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/records", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode < 300:
		return nil
	case resp.StatusCode < 500 && resp.StatusCode != http.StatusTooManyRequests:
		return fmt.Errorf("%w: %s", ErrPermanent, resp.Status)
	default:
		return fmt.Errorf("server: %s", resp.Status)
	}
}
