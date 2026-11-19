package main

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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
