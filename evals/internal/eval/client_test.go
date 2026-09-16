package eval

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/channel"
	"github.com/kkestell/ox/internal/acp"
)

func protocolClient(t *testing.T) (*processClient, channel.Channel, *os.File) {
	t.Helper()
	local, peer := net.Pipe()
	file, err := os.CreateTemp(t.TempDir(), "events")
	if err != nil {
		t.Fatal(err)
	}
	client := &processClient{events: &eventWriter{file: file}}
	client.transport = &eventChannel{Channel: channel.Line(local, local), events: client.events, client: client}
	client.rpc = jrpc2.NewClient(client.transport, &jrpc2.ClientOptions{OnCallback: client.respondToRequest})
	remote := channel.Line(peer, peer)
	t.Cleanup(func() { _ = peer.Close(); _ = client.rpc.Close(); _ = file.Close() })
	return client, remote, file
}

func sendProtocol(t *testing.T, remote channel.Channel, message any) {
	t.Helper()
	raw, err := json.Marshal(message)
	if err != nil {
		t.Error(err)
		return
	}
	if err := remote.Send(raw); err != nil {
		t.Error(err)
	}
}

func receiveProtocol(t *testing.T, remote channel.Channel) (json.RawMessage, string) {
	t.Helper()
	raw, err := remote.Recv()
	if err != nil {
		t.Error(err)
		return nil, ""
	}
	var message struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
	}
	if err := json.Unmarshal(raw, &message); err != nil {
		t.Error(err)
	}
	return message.ID, message.Method
}

func TestProtocolCapturesUpdatesBeforeResultAndRecordsRawEvents(t *testing.T) {
	client, remote, events := protocolClient(t)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		id, _ := receiveProtocol(t, remote)
		for range 100 {
			sendProtocol(t, remote, map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{
				"sessionId": "session", "update": map[string]any{"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "x"}},
			}})
		}
		sendProtocol(t, remote, map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{
			"sessionId": "session", "update": map[string]any{"sessionUpdate": "usage_update", "cost": map[string]any{"currency": "USD", "amount": 0.25}},
		}})
		sendProtocol(t, remote, map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"stopReason": "end_turn"}})
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, cancelled, err := client.call(ctx, "session/prompt", acp.PromptRequest{SessionID: "session", Prompt: []acp.ContentBlock{{Type: "text", Text: "finish"}}}, "allow", "session", 0)
	if err != nil || cancelled {
		t.Fatalf("call = %v, cancelled = %v", err, cancelled)
	}
	client.statsMu.Lock()
	if client.stats.Answer.String() != strings.Repeat("x", 100) || client.stats.CostUSD == nil || *client.stats.CostUSD != 0.25 {
		t.Error("result overtook final updates")
	}
	client.statsMu.Unlock()
	<-finished
	raw, err := os.ReadFile(events.Name())
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 103 || !strings.Contains(lines[0], `"direction":"sent"`) {
		t.Fatalf("raw events = %d", len(lines))
	}
	for _, line := range lines {
		if !json.Valid([]byte(line)) {
			t.Fatal("invalid raw artifact")
		}
	}
}

func TestProtocolDeadlineKeepsPromptAliveForCancellationResult(t *testing.T) {
	client, remote, _ := protocolClient(t)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		id, _ := receiveProtocol(t, remote)
		_, method := receiveProtocol(t, remote)
		if method != "session/cancel" {
			t.Errorf("cancel method = %q", method)
		}
		sendProtocol(t, remote, map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"stopReason": "cancelled"}})
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	raw, cancelled, err := client.call(ctx, "session/prompt", map[string]any{"sessionId": "session"}, "allow", "session", 0)
	if err != nil || !cancelled || !strings.Contains(string(raw), "cancelled") {
		t.Fatalf("cancel result = %s, %v, %v", raw, cancelled, err)
	}
	<-finished
}

func TestProtocolCancellationGraceExpires(t *testing.T) {
	client, remote, _ := protocolClient(t)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		_, _ = receiveProtocol(t, remote)
		_, method := receiveProtocol(t, remote)
		if method != "session/cancel" {
			t.Errorf("cancel = %q", method)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, cancelled, err := client.call(ctx, "session/prompt", map[string]any{"sessionId": "session"}, "allow", "session", 0)
	if !cancelled || !errors.Is(err, context.DeadlineExceeded) || time.Since(started) < 2*time.Second {
		t.Fatalf("grace = %v, %v, %s", cancelled, err, time.Since(started))
	}
	<-finished
}
