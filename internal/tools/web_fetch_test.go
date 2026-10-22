package tools

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"ox/internal/transcript"
)

func TestWebFetchConvertsHTMLAndFollowsRedirects(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.Header.Get("User-Agent") == "" {
			t.Errorf("unexpected request: %s %v", r.Method, r.Header)
		}
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/docs/page", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<html><body><h1>Hello 雪</h1><p>A <b>small</b> page with a <a href="next">link</a>.</p><script>hiddenScript()</script></body></html>`)
	}))
	t.Cleanup(server.Close)
	tools := toolbox(t)
	outcome := tools.call(WebFetch, object{"url": server.URL + "/start"})
	if outcome.Status != transcript.ToolCompleted {
		t.Fatal(outcome)
	}
	for _, want := range []string{"# " + server.URL + "/docs/page", "# Hello 雪", "**small**", "[link](" + server.URL + "/docs/next)"} {
		if !strings.Contains(outcome.Text, want) {
			t.Errorf("missing %q in %q", want, outcome.Text)
		}
	}
	if strings.Contains(outcome.Text, "hiddenScript") || len(outcome.Content) != 1 || entries(t, tools.Workspace) != 0 {
		t.Fatalf("unexpected output or files: %+v", outcome)
	}
}

func TestWebFetchPreservesTextAndDecodesHTMLCharset(t *testing.T) {
	for _, test := range []struct{ contentType, body, want string }{
		{"text/plain", "one\ntwo < three & four\n", "one\ntwo < three & four"},
		{"application/json", `{"value":"<tag>"}`, `{"value":"<tag>"}`},
		{"text/html; charset=iso-8859-1", "<h1>caf\xe9</h1>", "# café"},
	} {
		t.Run(test.contentType, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", test.contentType)
				fmt.Fprint(w, test.body)
			}))
			t.Cleanup(server.Close)
			outcome := toolbox(t).call(WebFetch, object{"url": server.URL})
			if outcome.Status != transcript.ToolCompleted || !strings.HasSuffix(outcome.Text, test.want+"\n") {
				t.Fatalf("%+v", outcome)
			}
		})
	}
}

func TestWebFetchSavesLargePagesForShell(t *testing.T) {
	for _, body := range []string{strings.Repeat("雪", outputLimit), strings.Repeat("paragraph\n", 900)} {
		tools := toolbox(t)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			fmt.Fprint(w, body)
		}))
		t.Cleanup(server.Close)
		for range 2 {
			outcome := tools.call(WebFetch, object{"url": server.URL})
			if outcome.Status != transcript.ToolCompleted || len(outcome.Text) > outputLimit || !utf8.ValidString(outcome.Text) {
				t.Fatalf("invalid output: %+v", outcome)
			}
			_, rest, ok := strings.Cut(outcome.Text, "saved to ")
			if !ok {
				t.Fatalf("missing saved path: %q", outcome.Text)
			}
			path, _, _ := strings.Cut(rest, ".\n")
			data, err := os.ReadFile(filepath.Join(tools.Workspace, path))
			if err != nil || string(data) != strings.TrimSpace(body)+"\n" {
				t.Fatalf("saved page: %d bytes, %v", len(data), err)
			}
			read := tools.call(Shell, object{"command": "python3 -c 'from pathlib import Path; print(Path(\"" + path + "\").read_text()[:100])'"})
			if read.Status != transcript.ToolCompleted || !strings.Contains(read.Text, string([]rune(strings.TrimSpace(body))[:10])) {
				t.Fatalf("could not read saved page: %+v", read)
			}
		}
		files, err := filepath.Glob(filepath.Join(tools.Workspace, ".ox/tmp/web-fetch-*.md"))
		if err != nil || len(files) != 2 {
			t.Fatalf("repeated fetch overwrote a file: %v, %v", files, err)
		}
	}
}

func TestWebFetchRejectsInvalidArguments(t *testing.T) {
	tools := toolbox(t)
	for _, arguments := range []any{
		"{", "{}", "null", `{"url":null}`, object{"url": 123}, object{"url": ""},
		object{"url": "/relative"}, object{"url": "file:///etc/passwd"}, object{"url": "http:///missing-host"},
		object{"url": "https://example.com", "extra": true}, `{"url":"https://example.com"} {}`,
	} {
		if outcome := tools.call(WebFetch, arguments); outcome.Status != transcript.ToolFailed {
			t.Errorf("accepted %v: %+v", arguments, outcome)
		}
	}
}

func TestWebFetchReportsHTTPAndContentErrors(t *testing.T) {
	for _, test := range []struct {
		name, contentType, body, want string
		status                        int
	}{
		{"not found", "text/plain", "missing", "HTTP 404", http.StatusNotFound},
		{"binary", "application/pdf", "%PDF", "unsupported content type", http.StatusOK},
		{"invalid text", "text/plain", "bad\xff", "not UTF-8 text", http.StatusOK},
		{"NUL", "text/plain", "bad\x00", "not UTF-8 text", http.StatusOK},
		{"oversized", "text/plain", strings.Repeat("x", webFetchBodyCap+1), "10 MiB limit", http.StatusOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", test.contentType)
				w.WriteHeader(test.status)
				fmt.Fprint(w, test.body)
			}))
			t.Cleanup(server.Close)
			outcome := toolbox(t).call(WebFetch, object{"url": server.URL})
			if outcome.Status != transcript.ToolFailed || !strings.Contains(outcome.Text, test.want) {
				t.Fatalf("%+v", outcome)
			}
		})
	}
}

func TestWebFetchCancellationStopsTheRequest(t *testing.T) {
	started := make(chan struct{})
	stopped := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(stopped)
	}))
	t.Cleanup(server.Close)
	tools := toolbox(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan transcript.ToolOutcome, 1)
	go func() { done <- tools.Execute(ctx, call(WebFetch, object{"url": server.URL})) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("request did not start")
	}
	cancel()
	select {
	case outcome := <-done:
		if outcome.Status != transcript.ToolCancelled || entries(t, tools.Workspace) != 0 {
			t.Fatalf("cancelled fetch: %+v", outcome)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("fetch did not return after cancellation")
	}
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("request continued after cancellation")
	}
}
