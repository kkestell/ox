package main

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// A page whose domain rebinds to the relay's address, or a page of another
// origin, cannot open an event stream or send a request to oxd.
func TestRelayRefusesForeignPages(t *testing.T) {
	relayEnd, daemonEnd := net.Pipe()
	defer relayEnd.Close()
	go io.Copy(io.Discard, daemonEnd)
	r := &relay{host: "ox.test", conns: map[string]net.Conn{"page": relayEnd}}
	h := r.handler(fstest.MapFS{})
	body := `{"jsonrpc":"2.0","id":1,"method":"workspace/create","params":{"path":"/"}}`
	for _, c := range []struct{ name, method, path, host, origin string }{
		{"events from a foreign host", "GET", "/events", "attacker.example:8080", ""},
		{"rpc from a foreign host", "POST", "/rpc?connection=page", "attacker.example:8080", "http://attacker.example:8080"},
		{"rpc from another origin", "POST", "/rpc?connection=page", "localhost:8080", "http://attacker.example"},
	} {
		req := httptest.NewRequest(c.method, c.path, strings.NewReader(body))
		req.Host = c.host
		if c.origin != "" {
			req.Header.Set("Origin", c.origin)
		}
		response := httptest.NewRecorder()
		h.ServeHTTP(response, req)
		if response.Code != http.StatusForbidden {
			t.Errorf("%s returned %d", c.name, response.Code)
		}
	}
}

// The page reaches the relay directly, or under make dev through Vite's
// proxy, which sends the relay's own address as Host with the page's Origin.
func TestRelayAcceptsItsPage(t *testing.T) {
	relayEnd, daemonEnd := net.Pipe()
	defer relayEnd.Close()
	go io.Copy(io.Discard, daemonEnd)
	r := &relay{host: "localhost", conns: map[string]net.Conn{"page": relayEnd}}
	h := r.handler(fstest.MapFS{})
	for _, c := range []struct{ name, host, origin string }{
		{"direct", "localhost:8080", "http://localhost:8080"},
		{"through Vite", "localhost:8081", "http://localhost:8080"},
	} {
		req := httptest.NewRequest("POST", "/rpc?connection=page", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"harness/list"}`))
		req.Host = c.host
		req.Header.Set("Origin", c.origin)
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		response := httptest.NewRecorder()
		h.ServeHTTP(response, req)
		if response.Code != http.StatusNoContent {
			t.Errorf("%s returned %d: %s", c.name, response.Code, response.Body)
		}
	}
}

// An oversized request fails at once instead of reaching oxd truncated, where
// its parse error carries no ID and the page's request would never settle.
func TestRPCRefusesOversizedBody(t *testing.T) {
	relayEnd, daemonEnd := net.Pipe()
	r := &relay{conns: map[string]net.Conn{"page": relayEnd}}
	forwarded := make(chan int)
	go func() {
		data, _ := io.ReadAll(daemonEnd)
		forwarded <- len(data)
	}()
	body := `{"jsonrpc":"2.0","id":1,"method":"session/send","params":{"session_id":"s","text":"` +
		strings.Repeat("x", 1<<20) + `"}}`
	response := httptest.NewRecorder()
	r.rpc(response, httptest.NewRequest("POST", "/rpc?connection=page", strings.NewReader(body)))
	relayEnd.Close()
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("an oversized request returned %d", response.Code)
	}
	if n := <-forwarded; n != 0 {
		t.Errorf("an oversized request forwarded %d bytes to oxd", n)
	}
}
