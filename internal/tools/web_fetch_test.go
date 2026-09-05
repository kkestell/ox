package tools

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kkestell/ox/internal/agent"
)

var fixturePublicAddress = netip.MustParseAddr("93.184.216.34")

func webFixture(
	t *testing.T,
	handler http.Handler,
) (string, webFetchNetwork) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	_, port, err := net.SplitHostPort(parsed.Host)
	if err != nil {
		t.Fatal(err)
	}
	network := webFetchNetwork{
		lookup: func(_ context.Context, host string) ([]netip.Addr, error) {
			if host != "public.test" {
				t.Fatalf("resolved unexpected host %q", host)
			}
			return []netip.Addr{fixturePublicAddress}, nil
		},
		dial: func(ctx context.Context, protocol, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, protocol, parsed.Host)
		},
	}
	return "http://public.test:" + port, network
}

func fetchFixture(
	t *testing.T,
	ctx context.Context,
	network webFetchNetwork,
	rawURL string,
) (string, agent.Invocation, error) {
	t.Helper()
	arguments, err := json.Marshal(map[string]string{"url": rawURL})
	if err != nil {
		t.Fatal(err)
	}
	invocation := testInvocation(t, string(arguments))
	output, err := executeWebFetchWith(ctx, invocation, network)
	return output, invocation, err
}

func TestWebFetchExtractsSupportedSources(t *testing.T) {
	var base string
	base, network := webFixture(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/redirect":
			http.Redirect(writer, request, base+"/html", http.StatusFound)
		case "/html":
			writer.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(writer, `<html><body><h1>Hello</h1><p>A <strong>small</strong> page.</p></body></html>`)
		case "/text":
			writer.Header().Set("Content-Type", "text/plain")
			fmt.Fprint(writer, " plain text \n")
		case "/json":
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, `{"answer":42,"items":[true,false]}`)
		default:
			http.NotFound(writer, request)
		}
	}))

	for _, test := range []struct {
		path string
		want []string
	}{
		{path: "/redirect", want: []string{
			"Requested URL: " + base + "/redirect",
			"Final URL: " + base + "/html",
			"Content-Type: text/html",
			"<untrusted_web_source>", "# Hello", "**small**", "</untrusted_web_source>",
		}},
		{path: "/text", want: []string{"Content-Type: text/plain", "\nplain text\n"}},
		{path: "/json", want: []string{
			"Content-Type: application/json", "\n  \"answer\": 42,", "\n    true,", "\n    false",
		}},
	} {
		t.Run(strings.TrimPrefix(test.path, "/"), func(t *testing.T) {
			output, _, err := fetchFixture(t, context.Background(), network, base+test.path)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range test.want {
				if !strings.Contains(output, want) {
					t.Fatalf("output missing %q: %q", want, output)
				}
			}
		})
	}
}

func TestWebFetchRejectsInvalidURLsAndNonpublicDestinations(t *testing.T) {
	for _, raw := range []string{
		"file:///etc/passwd",
		"relative/path",
		"http://user:secret@example.com/",
	} {
		invocation := testInvocation(t, `{"url":`+strconv.Quote(raw)+`}`)
		if _, err := executeWebFetchWith(context.Background(), invocation, webFetchNetwork{}); err == nil {
			t.Fatalf("URL %q succeeded", raw)
		}
	}
	for _, arguments := range []string{`{}`, `{"url":7}`, `{"url":"https://example.com","extra":true}`} {
		invocation := testInvocation(t, arguments)
		if _, err := executeWebFetchWith(context.Background(), invocation, webFetchNetwork{}); err == nil {
			t.Fatalf("arguments %s succeeded", arguments)
		}
	}

	dialed := false
	network := webFetchNetwork{
		lookup: func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{fixturePublicAddress, netip.MustParseAddr("127.0.0.1")}, nil
		},
		dial: func(context.Context, string, string) (net.Conn, error) {
			dialed = true
			return nil, errors.New("unexpected dial")
		},
	}
	for _, raw := range []string{"http://127.0.0.1/", "http://mixed.test/"} {
		_, _, err := fetchFixture(t, context.Background(), network, raw)
		if err == nil || !strings.Contains(err.Error(), "nonpublic") {
			t.Fatalf("URL %q error = %v", raw, err)
		}
	}
	if dialed {
		t.Fatal("nonpublic destination was dialed")
	}
}

func TestPublicWebAddressPolicy(t *testing.T) {
	for _, test := range []struct {
		address string
		public  bool
	}{
		{address: "8.8.8.8", public: true},
		{address: "2606:4700:4700::1111", public: true},
		{address: "10.0.0.1"},
		{address: "100.64.0.1"},
		{address: "192.0.2.1"},
		{address: "198.18.0.1"},
		{address: "203.0.113.1"},
		{address: "::1"},
		{address: "::ffff:127.0.0.1"},
		{address: "64:ff9b::7f00:1"},
		{address: "2001:db8::1"},
		{address: "fc00::1"},
		{address: "fe80::1"},
	} {
		if got := publicWebAddress(netip.MustParseAddr(test.address)); got != test.public {
			t.Errorf("publicWebAddress(%s) = %v, want %v", test.address, got, test.public)
		}
	}
}

func TestWebFetchBoundsRedirectsAndDecodedBodies(t *testing.T) {
	var base string
	base, network := webFixture(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch {
		case strings.HasPrefix(request.URL.Path, "/redirect/"):
			step, _ := strconv.Atoi(strings.TrimPrefix(request.URL.Path, "/redirect/"))
			http.Redirect(writer, request, base+"/redirect/"+strconv.Itoa(step+1), http.StatusFound)
		case request.URL.Path == "/private":
			http.Redirect(writer, request, "http://127.0.0.1/private", http.StatusFound)
		case request.URL.Path == "/oversize":
			writer.Header().Set("Content-Type", "text/plain")
			fmt.Fprint(writer, strings.Repeat("x", webFetchBodyBytes+1))
		case request.URL.Path == "/exact":
			writer.Header().Set("Content-Type", "text/plain")
			fmt.Fprint(writer, strings.Repeat("x", webFetchBodyBytes))
		case request.URL.Path == "/gzip":
			writer.Header().Set("Content-Type", "text/plain")
			writer.Header().Set("Content-Encoding", "gzip")
			compressed := gzip.NewWriter(writer)
			_, _ = compressed.Write([]byte(strings.Repeat("x", webFetchBodyBytes+1)))
			_ = compressed.Close()
		case request.URL.Path == "/br":
			writer.Header().Set("Content-Type", "text/plain")
			writer.Header().Set("Content-Encoding", "br")
			fmt.Fprint(writer, "encoded")
		default:
			writer.Header().Set("Content-Type", "text/plain")
			fmt.Fprint(writer, "done")
		}
	}))

	for _, test := range []struct {
		path string
		want string
	}{
		{path: "/redirect/0", want: "redirect limit"},
		{path: "/private", want: "nonpublic"},
		{path: "/oversize", want: "decoded body limit"},
		{path: "/gzip", want: "decoded body limit"},
		{path: "/br", want: "content encoding"},
	} {
		t.Run(strings.TrimPrefix(test.path, "/"), func(t *testing.T) {
			_, _, err := fetchFixture(t, context.Background(), network, base+test.path)
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
	output, _, err := fetchFixture(t, context.Background(), network, base+"/exact")
	if err != nil || !strings.Contains(output, "full output at") {
		t.Fatalf("exact-limit output = %q, error = %v", output, err)
	}
}

func TestWebFetchRejectsBadResponsesWithoutLeakingContent(t *testing.T) {
	const secret = "response-secret-sentinel"
	base, network := webFixture(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/status":
			http.Error(writer, secret, http.StatusForbidden)
		case "/binary":
			writer.Header().Set("Content-Type", "application/octet-stream")
			_, _ = writer.Write([]byte{0, 1, 2})
		case "/utf8":
			writer.Header().Set("Content-Type", "text/plain")
			_, _ = writer.Write([]byte{0xff})
		case "/json":
			writer.Header().Set("Content-Type", "application/json")
			fmt.Fprint(writer, "{")
		case "/content-type":
			writer.Header().Set("Content-Type", "not a content type;")
			fmt.Fprint(writer, secret)
		}
	}))
	for _, path := range []string{"/status", "/binary", "/utf8", "/json", "/content-type"} {
		_, _, err := fetchFixture(t, context.Background(), network, base+path)
		if err == nil || strings.Contains(err.Error(), secret) {
			t.Fatalf("path %s error = %v", path, err)
		}
	}
}

func TestWebFetchUsesIsolatedHeadersAndSpills(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	base, network := webFixture(t, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		for _, header := range []string{"Authorization", "Cookie", "Proxy-Authorization"} {
			if got := request.Header.Get(header); got != "" {
				t.Errorf("%s = %q", header, got)
			}
		}
		if request.Header.Get("User-Agent") != webFetchUserAgent {
			t.Errorf("User-Agent = %q", request.Header.Get("User-Agent"))
		}
		writer.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(writer, strings.Repeat("spill content\n", 6_000))
	}))
	arguments, _ := json.Marshal(map[string]string{"url": base + "/large"})
	invocation := testInvocation(t, string(arguments))
	var spill string
	invocation.ReportSpill = func(path string) { spill = path }
	output, err := executeWebFetchWith(context.Background(), invocation, network)
	if err != nil {
		t.Fatal(err)
	}
	if spill == "" || !strings.Contains(output, "full output at "+spill) {
		t.Fatalf("output = %q, spill = %q", output, spill)
	}
	stored, err := os.ReadFile(spill)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(stored), "Requested URL: "+base+"/large") ||
		!strings.Contains(string(stored), "<untrusted_web_source>") ||
		!strings.Contains(string(stored), "spill content") {
		t.Fatalf("spill missing source wrapper: %q", string(stored[:min(len(stored), 500)]))
	}
}

func TestWebFetchCancellation(t *testing.T) {
	started := make(chan struct{})
	base, network := webFixture(t, http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		close(started)
		<-request.Context().Done()
	}))
	ctx, cancel := context.WithCancel(context.Background())
	arguments, _ := json.Marshal(map[string]string{"url": base + "/wait"})
	invocation := testInvocation(t, string(arguments))
	done := make(chan error, 1)
	go func() {
		_, err := executeWebFetchWith(ctx, invocation, network)
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("fetch did not start")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "context canceled") {
			t.Fatalf("cancellation error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("fetch did not cancel")
	}
}

func TestWebFetchHonorsEarlierRequestDeadline(t *testing.T) {
	base, network := webFixture(t, http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		<-request.Context().Done()
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	_, _, err := fetchFixture(t, ctx, network, base+"/wait")
	if err == nil || !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("deadline error = %v", err)
	}
}

func TestWebFetchTitle(t *testing.T) {
	tool := toolNamed(t, "web_fetch")
	if got := tool.Title(json.RawMessage(`{"url":"https://example.com/a"}`)); got != "Fetch https://example.com/a" {
		t.Fatalf("title = %q", got)
	}
	if got := tool.Title(json.RawMessage(`{}`)); got != "Fetch web page" {
		t.Fatalf("fallback title = %q", got)
	}
}
