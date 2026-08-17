package agent

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"
	"github.com/creachadair/jrpc2/server"

	"github.com/kkestell/ox/internal/acp"
	"github.com/kkestell/ox/internal/config"
	"github.com/kkestell/ox/internal/credentials"
	"github.com/kkestell/ox/internal/openrouter"
)

func testAgent() *Agent {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(
		"ox",
		"0.0.1",
		config.Environment{ModelOverride: "test/model"},
		credentials.NewStore("", true, logger),
		&openrouter.Client{Logger: logger},
		logger,
	)
}

func TestInitializeRetainsClientCapabilities(t *testing.T) {
	agent := testAgent()
	capabilities := &acp.ClientCapabilities{Terminal: true}

	response, err := agent.Initialize(t.Context(), acp.InitializeRequest{
		ProtocolVersion:    acp.ProtocolVersion,
		ClientCapabilities: capabilities,
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.ProtocolVersion != acp.ProtocolVersion {
		t.Fatalf("protocolVersion = %d, want %d", response.ProtocolVersion, acp.ProtocolVersion)
	}
	if got := agent.clientCapabilities.Load(); got != capabilities {
		t.Fatalf("clientCapabilities = %#v, want %#v", got, capabilities)
	}
}

func TestInitializeIsSafeConcurrently(t *testing.T) {
	agent := testAgent()
	const callers = 32

	capabilities := make([]*acp.ClientCapabilities, callers)
	errors := make(chan error, callers)
	var ready sync.WaitGroup
	ready.Add(callers)
	start := make(chan struct{})
	for index := range callers {
		capabilities[index] = &acp.ClientCapabilities{Terminal: index%2 == 0}
		go func() {
			ready.Done()
			<-start
			_, err := agent.Initialize(t.Context(), acp.InitializeRequest{
				ProtocolVersion:    acp.ProtocolVersion,
				ClientCapabilities: capabilities[index],
			})
			errors <- err
		}()
	}
	ready.Wait()
	close(start)

	for range callers {
		if err := <-errors; err != nil {
			t.Fatal(err)
		}
	}
	got := agent.clientCapabilities.Load()
	for _, candidate := range capabilities {
		if got == candidate {
			return
		}
	}
	t.Fatalf("clientCapabilities = %#v, want one of the stored values", got)
}

func TestCancelRequestCancelsInFlightContext(t *testing.T) {
	agent := testAgent()
	started := make(chan struct{})
	methods := agent.Methods()
	methods["stall"] = handler.New(func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		return jrpc2.Errorf(jrpc2.Code(acp.ErrCodeRequestCancelled), "request cancelled")
	})
	local := server.NewLocal(methods, &server.LocalOptions{
		Server: &jrpc2.ServerOptions{Concurrency: 2},
	})
	defer func() {
		if err := local.Close(); err != nil {
			t.Errorf("close local server: %v", err)
		}
	}()

	callError := make(chan error, 1)
	go func() {
		_, err := local.Client.Call(t.Context(), "stall", nil)
		callError <- err
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("stall handler did not start")
	}
	if err := local.Client.Notify(t.Context(), "$/cancel_request", acp.CancelRequestNotification{
		RequestID: json.RawMessage(`1`),
	}); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-callError:
		if code := jrpc2.ErrorCode(err); code != jrpc2.Code(acp.ErrCodeRequestCancelled) {
			t.Fatalf("cancellation error code = %d, want %d", code, acp.ErrCodeRequestCancelled)
		}
	case <-time.After(time.Second):
		t.Fatal("request context was not cancelled")
	}
}
