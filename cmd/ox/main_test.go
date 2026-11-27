package main

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

// A page whose domain rebinds to the relay's address, or a page of another
// origin, cannot load the page, open an event stream, or send a request to oxd.
func TestRelayRefusesForeignPages(t *testing.T) {
	relayEnd, daemonEnd := net.Pipe()
	defer relayEnd.Close()
	go io.Copy(io.Discard, daemonEnd)
	r := tailnetRelay(t)
	r.conns["page"] = relayEnd
	h := r.handler(fstest.MapFS{"index.html": {Data: []byte("ox")}})
	body := `{"jsonrpc":"2.0","id":1,"method":"workspace/create","params":{"path":"/"}}`
	for _, c := range []struct{ name, method, path, host, origin string }{
		{"page from a foreign host", "GET", "/", "attacker.example:8080", ""},
		{"events from a foreign host", "GET", "/events", "attacker.example:8080", ""},
		{"rpc from a foreign host", "POST", "/rpc?connection=page", "attacker.example:8080", "http://attacker.example:8080"},
		{"rpc from another origin", "POST", "/rpc?connection=page", "localhost:8080", "http://attacker.example"},
		{"rpc from another origin on the tailnet host", "POST", "/rpc?connection=page", "100.64.1.2:17840", "http://attacker.example"},
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

// The page reaches the relay directly, at the host -addr names in any case,
// or through the tailnet address.
func TestRelayAcceptsItsPage(t *testing.T) {
	relayEnd, daemonEnd := net.Pipe()
	defer relayEnd.Close()
	go io.Copy(io.Discard, daemonEnd)
	r, err := newRelay("", webAddrs("Ox.Test:8080", netip.MustParseAddr("100.64.1.2")))
	if err != nil {
		t.Fatal(err)
	}
	r.conns["page"] = relayEnd
	h := r.handler(fstest.MapFS{})
	for _, c := range []struct{ name, host, origin string }{
		{"direct", "localhost:8080", "http://localhost:8080"},
		{"configured host, lowercased by the browser", "ox.test:8080", "http://ox.test:8080"},
		{"tailnet host", "100.64.1.2:17840", "http://100.64.1.2:17840"},
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

// The relay serves only a named host. On every interface, any machine could
// reach oxd by sending a loopback name as Host.
func TestRelayRefusesEveryInterface(t *testing.T) {
	for _, addr := range []string{":8080", "0.0.0.0:8080", "[::]:8080"} {
		if _, err := newRelay("", webAddrs(addr, netip.Addr{})); err == nil {
			t.Errorf("-addr %s was accepted", addr)
		}
	}
	for _, addr := range []string{"localhost:8080", "127.0.0.1:8080", "[::1]:8080", "ox.test:8080"} {
		if _, err := newRelay("", webAddrs(addr, netip.Addr{})); err != nil {
			t.Errorf("-addr %s: %v", addr, err)
		}
	}
}

// tailnetRelay returns the relay ox runs at the default -addr with a
// tailscale_ip configured.
func tailnetRelay(t *testing.T) *relay {
	t.Helper()
	r, err := newRelay("", webAddrs("localhost:8080", netip.MustParseAddr("100.64.1.2")))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// Ox serves the web UI on the configured Tailscale address at port 17840,
// whatever -addr's port, and only at -addr without one.
func TestWebAddrs(t *testing.T) {
	if got := webAddrs("localhost:8080", netip.Addr{}); !slices.Equal(got, []string{"localhost:8080"}) {
		t.Errorf("without tailscale_ip: %v", got)
	}
	got := webAddrs("localhost:8081", netip.MustParseAddr("100.64.1.2"))
	if !slices.Equal(got, []string{"localhost:8081", "100.64.1.2:17840"}) {
		t.Errorf("with tailscale_ip: %v", got)
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
		strings.Repeat("x", 24<<20) + `"}}`
	response := httptest.NewRecorder()
	r.rpc(response, httptest.NewRequest("POST", "/rpc?connection=page", strings.NewReader(body)))
	relayEnd.Close()
	if response.Code != http.StatusRequestEntityTooLarge || !strings.Contains(response.Body.String(), "larger than 24 MiB") {
		t.Errorf("an oversized request returned %d: %s", response.Code, response.Body)
	}
	if n := <-forwarded; n != 0 {
		t.Errorf("an oversized request forwarded %d bytes to oxd", n)
	}
}

// The largest prompt oxd takes, 1 MiB of text escaped and 10 MiB of images
// as base64, reaches oxd whole.
func TestRPCRelaysTheLargestPrompt(t *testing.T) {
	relayEnd, daemonEnd := net.Pipe()
	defer daemonEnd.Close()
	r := &relay{conns: map[string]net.Conn{"page": relayEnd}}
	forwarded := make(chan []byte, 1)
	go func() {
		data, _ := io.ReadAll(daemonEnd)
		forwarded <- data
	}()
	images := base64.StdEncoding.EncodeToString(make([]byte, 10<<20))
	body := `{"jsonrpc":"2.0","id":1,"method":"session/send","params":{"session_id":"s","text":"` +
		strings.Repeat(`\u003c`, 1<<20) + `","images":["` + images + `"]}}`
	response := httptest.NewRecorder()
	r.rpc(response, httptest.NewRequest("POST", "/rpc?connection=page", strings.NewReader(body)))
	relayEnd.Close()
	if data := <-forwarded; response.Code != http.StatusNoContent || string(data) != body+"\n" {
		t.Fatalf("the largest prompt returned %d and relayed %d bytes", response.Code, len(data))
	}
}

// A post to a connection that is gone answers 503, which the page reads as a
// lost connection rather than an error to show.
func TestRPCAnswersGoneConnectionUnavailable(t *testing.T) {
	relayEnd, daemonEnd := net.Pipe()
	daemonEnd.Close()
	r := &relay{conns: map[string]net.Conn{"closed": relayEnd}}
	for _, connection := range []string{"unknown", "closed"} {
		response := httptest.NewRecorder()
		r.rpc(response, httptest.NewRequest("POST", "/rpc?connection="+connection, strings.NewReader(`{}`)))
		if response.Code != http.StatusServiceUnavailable {
			t.Errorf("a post to the %s connection returned %d: %s", connection, response.Code, response.Body)
		}
	}
}

// The relay serves an image it asks oxd for on a connection of its own,
// skipping the notifications oxd sends that connection first.
func TestImageServesOxdImage(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "oxd.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	png := []byte("\x89PNG\r\n\x1a\npixels")
	asked := make(chan string, 2)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				line, _ := bufio.NewReader(conn).ReadBytes('\n')
				var request struct {
					ID     json.RawMessage `json:"id"`
					Method string          `json:"method"`
					Params struct {
						SessionID string `json:"session_id"`
						ImageID   string `json:"image_id"`
					} `json:"params"`
				}
				json.Unmarshal(line, &request)
				asked <- request.Method + " " + request.Params.SessionID + " " + request.Params.ImageID
				fmt.Fprintln(conn, `{"jsonrpc":"2.0","method":"session/update","params":{"session_id":"other","type":"state","state":"idle"}}`)
				if request.Params.ImageID == "gone" {
					fmt.Fprintf(conn, `{"jsonrpc":"2.0","id":%s,"error":{"code":-32001,"message":"image not found"}}`+"\n", request.ID)
					return
				}
				fmt.Fprintf(conn, `{"jsonrpc":"2.0","id":%s,"result":{"mime_type":"image/png","data":"%s"}}`+"\n",
					request.ID, base64.StdEncoding.EncodeToString(png))
			}()
		}
	}()
	r, err := newRelay(socket, []string{"localhost:8080"})
	if err != nil {
		t.Fatal(err)
	}
	h := r.handler(fstest.MapFS{})
	get := func(path, host string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", path, nil)
		req.Host = host
		response := httptest.NewRecorder()
		h.ServeHTTP(response, req)
		return response
	}

	response := get("/images/s1/i1", "localhost:8080")
	if response.Code != http.StatusOK || response.Body.String() != string(png) {
		t.Fatalf("the image returned %d with %d bytes", response.Code, response.Body.Len())
	}
	if got := <-asked; got != "session/image s1 i1" {
		t.Errorf("the relay asked oxd %q", got)
	}
	if got := response.Header().Get("Content-Type"); got != "image/png" {
		t.Errorf("Content-Type %q", got)
	}
	if got := response.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options %q", got)
	}

	if response := get("/images/s1/gone", "localhost:8080"); response.Code != http.StatusNotFound {
		t.Errorf("an image oxd does not have returned %d", response.Code)
	}

	if response := get("/images/s1/i1", "attacker.example:8080"); response.Code != http.StatusForbidden {
		t.Errorf("an image request from a foreign host returned %d", response.Code)
	}
}
