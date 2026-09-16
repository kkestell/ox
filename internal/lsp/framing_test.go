package lsp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/channel"
)

func TestBoundedFrames(t *testing.T) {
	body := `{"jsonrpc":"2.0","id":1,"result":null}`
	frame := func(body string) string { return fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(body), body) }
	header := func(size int) string {
		base := fmt.Sprintf("Content-Length: %d\r\n", len(body))
		return base + "X: " + strings.Repeat("x", size-len(base)-7) + "\r\n\r\n" + body
	}
	prefix := `{"jsonrpc":"2.0","id":1,"result":"`
	maxBody := prefix + strings.Repeat("x", maxMessageBytes-len(prefix)-2) + `"}`
	for _, test := range []struct {
		name, frame string
		valid       bool
	}{
		{"ordinary", frame(body), true},
		{"maximum body", frame(maxBody), true},
		{"oversized body", fmt.Sprintf("Content-Length: %d\r\n\r\n", maxMessageBytes+1), false},
		{"maximum header", header(maxHeaderBytes), true},
		{"oversized header", header(maxHeaderBytes + 1), false},
		{"unterminated header", strings.Repeat("x", maxHeaderBytes+1), false},
		{"missing length", "X: ignored\r\n\r\n", false},
		{"negative length", "Content-Length: -1\r\n\r\n", false},
		{"truncated body", "Content-Length: 40\r\n\r\n{}", false},
		{"invalid json", frame("{"), false},
		{"missing version", frame(`{"id":1,"result":null}`), false},
		{"wrong version", frame(`{"jsonrpc":"1.0","id":1,"result":null}`), false},
		{"batch", frame("[" + body + "]"), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := readMessage(bufio.NewReaderSize(strings.NewReader(test.frame), maxHeaderBytes+1))
			if (err == nil) != test.valid {
				t.Fatalf("valid = %v, error = %v", test.valid, err)
			}
		})
	}
}

func TestRPCAdapterRejectsMalformedResponse(t *testing.T) {
	clientSide, serverSide := channel.Direct()
	rpc := jrpc2.NewClient(clientSide, nil)
	defer rpc.Close()
	defer serverSide.Close()
	go func() {
		raw, err := serverSide.Recv()
		if err != nil {
			return
		}
		var request struct {
			ID json.RawMessage `json:"id"`
		}
		_ = json.Unmarshal(raw, &request)
		body, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": request.ID})
		_ = serverSide.Send(body)
	}()
	adapter := &client{rpc: rpc}
	_, err := adapter.request(context.Background(), "malformed", nil)
	if err == nil {
		t.Fatal("response without result or error accepted")
	}
}

func TestBoundedFrameSend(t *testing.T) {
	var output bytes.Buffer
	writer := bufferCloser{&output}
	transport := boundedChannel{stdin: writer, stdout: io.NopCloser(strings.NewReader(""))}
	body := []byte(`{"jsonrpc":"2.0","method":"exit"}`)
	if err := transport.Send(body); err != nil {
		t.Fatal(err)
	}
	got, err := readMessage(bufio.NewReader(&output))
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("round trip = %s, %v", got, err)
	}
	if err := transport.Send(make([]byte, maxMessageBytes+1)); err == nil {
		t.Fatal("oversized outbound message accepted")
	}
}

type bufferCloser struct{ *bytes.Buffer }

func (bufferCloser) Close() error { return nil }
