package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
	"github.com/JohannesKaufmann/html-to-markdown/v2/converter"
	"golang.org/x/net/html/charset"

	"ox/internal/transcript"
)

const (
	webFetchTimeout = 30 * time.Second
	webFetchBodyCap = 10 * 1024 * 1024
)

var webFetchSchema = compact(`{
  "type": "function",
  "function": {
    "name": "web_fetch",
    "description": "Fetch an HTTP(S) URL. Converts HTML to Markdown and preserves plain text. Follows redirects, without executing JavaScript. Returns at most 16 KiB; larger pages are saved under .ox/tmp/ with a preview and a path to read using shell. Requests time out after 30 seconds; responses over 10 MiB and binary content are rejected. Example: {\"url\":\"https://example.com\"}.",
    "parameters": {
      "type": "object",
      "properties": {
        "url": {"type": "string", "description": "Absolute HTTP or HTTPS URL."}
      },
      "required": ["url"],
      "additionalProperties": false
    }
  }
}`)

func webFetch(ctx context.Context, root, arguments string) (string, []transcript.ToolContent, error) {
	var args struct {
		URL *string `json:"url"`
	}
	if err := decodeArguments(arguments, &args); err != nil {
		return "", nil, err
	}
	if args.URL == nil {
		return "", nil, errors.New("arguments: missing field `url`")
	}
	u, err := url.Parse(*args.URL)
	if err != nil {
		return "", nil, fmt.Errorf("invalid URL: %w", err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return "", nil, errors.New("URL must be an absolute http or https URL")
	}
	ctx, cancel := context.WithTimeout(ctx, webFetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64; rv:128.0) Gecko/20100101 Firefox/128.0")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,text/plain;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", nil, fmt.Errorf("web fetch: HTTP %d for %s", resp.StatusCode, resp.Request.URL)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, webFetchBodyCap+1))
	if err != nil {
		return "", nil, fmt.Errorf("read response body: %w", err)
	}
	if len(body) > webFetchBodyCap {
		return "", nil, errors.New("response exceeds the 10 MiB limit")
	}
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = http.DetectContentType(body)
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return "", nil, fmt.Errorf("invalid content type: %w", err)
	}
	finalURL := resp.Request.URL.String()
	if mediaType == "text/html" || mediaType == "application/xhtml+xml" {
		reader, err := charset.NewReader(bytes.NewReader(body), contentType)
		if err != nil {
			return "", nil, err
		}
		body, err = htmltomarkdown.ConvertReader(reader, converter.WithDomain(finalURL), converter.WithContext(ctx))
		if err != nil {
			return "", nil, fmt.Errorf("convert HTML to Markdown: %w", err)
		}
	} else if !strings.HasPrefix(mediaType, "text/") && mediaType != "application/json" && mediaType != "application/xml" {
		return "", nil, fmt.Errorf("unsupported content type: %s", mediaType)
	}
	if !utf8.Valid(body) || bytes.IndexByte(body, 0) >= 0 {
		return "", nil, errors.New("response is not UTF-8 text")
	}
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}
	markdown := strings.TrimSpace(string(body))
	header := "# " + truncate(finalURL, 2048) + "\n\n"
	summary := fmt.Sprintf("Fetched %d bytes", len(markdown))
	content := []transcript.ToolContent{{Text: summary}}
	if len(header)+len(markdown)+1 <= outputLimit && strings.Count(markdown, "\n") < 800 {
		return header + markdown + "\n", content, nil
	}
	dir := filepath.Join(root, ".ox", "tmp")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", nil, err
	}
	file, err := os.CreateTemp(dir, "web-fetch-*.md")
	if err != nil {
		return "", nil, err
	}
	_, writeErr := io.WriteString(file, markdown+"\n")
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		os.Remove(file.Name())
		return "", nil, err
	}
	path := filepath.Join(".ox", "tmp", filepath.Base(file.Name()))
	text := fmt.Sprintf("%s%s; saved to %s.\nUse shell with sed or Python to read sections of the full page.\n\n%s", header, summary, path, truncate(markdown, 2048))
	return text, content, nil
}
