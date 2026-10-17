package mcpclient

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"ox/internal/mcptest"
	"ox/internal/settings"
)

func TestMCPHelper(t *testing.T) { mcptest.Run() }

func fixture(t *testing.T, config mcptest.Config) (*Clients, string) {
	t.Helper()
	definition, path := mcptest.Definition(t, config)
	clients := New(t.TempDir(), map[string]settings.MCPServer{"project": definition})
	t.Cleanup(clients.Close)
	return clients, path
}

func TestDiscoveryDispatchAndRefresh(t *testing.T) {
	marker := t.TempDir() + "/events"
	config := mcptest.Config{Tools: []*mcp.Tool{mcptest.Tool("z", false), mcptest.Tool("read_file", true), mcptest.Tool("odd 🦊", true), mcptest.Tool(strings.Repeat("a", 100), false)}, PageSize: 1, Marker: marker}
	clients, path := fixture(t, config)
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("constructed clients started a server")
	}
	catalog, err := clients.Prepare(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	valid := regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)
	previous := ""
	for _, tool := range catalog.Tools() {
		if !valid.MatchString(tool.Name) || tool.Name <= previous || tool.Description != "Test "+tool.OriginalName {
			t.Fatalf("tool: %+v", tool)
		}
		previous = tool.Name
		result, err := catalog.Call(context.Background(), tool.Name, json.RawMessage(`{"number":9007199254740993}`))
		if err != nil || !strings.Contains(string(result.Content[0]), "9007199254740993") {
			t.Fatalf("call: %+v %v", result, err)
		}
	}
	if len(catalog.Tools()) != 4 {
		t.Fatal(catalog.Tools())
	}
	config.Tools = []*mcp.Tool{mcptest.Tool("z", true)}
	config.Tools[0].Description = "Changed"
	mcptest.Write(t, path, config)
	newer, err := clients.Prepare(context.Background())
	if err != nil || len(newer.Tools()) != 1 || !newer.Tools()[0].ReadOnly || newer.Tools()[0].Description != "Changed" {
		t.Fatalf("refresh: %v %v", newer, err)
	}
	if old, _ := catalog.Lookup("mcp__project__z"); old.ReadOnly {
		t.Fatal("old catalog changed")
	}
	data, _ := os.ReadFile(marker)
	if strings.Count(string(data), "start ") != 1 {
		t.Fatalf("not reused: %s", data)
	}
}

func TestNamesIncludeUnambiguousOriginalPair(t *testing.T) {
	pairs := [][2]string{{"a", "read_file"}, {"b", "read_file"}, {"a__b", "c"}, {"a", "b__c"}, {"a", "x/y"}, {"a", "x?y"}, {"a", strings.Repeat("z", 200)}}
	seen := map[string]bool{}
	for _, pair := range pairs {
		name := modelName(pair[0], pair[1])
		if seen[name] || len(name) > 64 || name != modelName(pair[0], pair[1]) {
			t.Fatal(name)
		}
		seen[name] = true
	}
}

func TestProcessEnvironment(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "secret")
	t.Setenv("OX_INHERITED", "inherited")
	definition, _ := mcptest.Definition(t, mcptest.Config{Tools: []*mcp.Tool{mcptest.Tool("env", true)}, Calls: map[string]string{"env": "environment"}})
	definition.Args = append(definition.Args, "--", "literal argument")
	definition.Env["OX_EXPLICIT"] = "configured"
	clients := New(t.TempDir(), map[string]settings.MCPServer{"project": definition})
	defer clients.Close()
	catalog, err := clients.Prepare(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	result, err := catalog.Call(context.Background(), "mcp__project__env", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	raw := string(result.Content[0])
	for _, want := range []string{clients.workspace, "literal argument", "OX_INHERITED=inherited", "OX_EXPLICIT=configured"} {
		if !strings.Contains(raw, want) {
			t.Errorf("missing %s: %s", want, raw)
		}
	}
	if strings.Contains(raw, "OPENROUTER_API_KEY=") {
		t.Fatal("leaked provider key")
	}
}

func TestSetupFailureCanRetryAndCleansEarlierConnections(t *testing.T) {
	marker := t.TempDir() + "/events"
	first, _ := mcptest.Definition(t, mcptest.Config{Marker: marker})
	second, path := mcptest.Definition(t, mcptest.Config{Discovery: "error"})
	clients := New(t.TempDir(), map[string]settings.MCPServer{"a": first, "b": second})
	defer clients.Close()
	if _, err := clients.Prepare(context.Background()); err == nil || !strings.Contains(err.Error(), `"b"`) {
		t.Fatalf("error: %v", err)
	}
	if len(clients.sessions) != 0 || clients.ready {
		t.Fatal("failed setup retained sessions")
	}
	mcptest.Write(t, path, mcptest.Config{Tools: []*mcp.Tool{mcptest.Tool("works", true)}})
	if _, err := clients.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(marker)
	if strings.Count(string(data), "start ") != 2 {
		t.Fatalf("setup not retried: %s", data)
	}
}

func TestSetupTimeoutAndCancellation(t *testing.T) {
	for _, phase := range []string{"startup", "discovery"} {
		t.Run(phase, func(t *testing.T) {
			config := mcptest.Config{}
			if phase == "startup" {
				config.Startup = "wait"
			} else {
				config.Discovery = "wait"
			}
			clients, _ := fixture(t, config)
			clients.setupTimeout = 80 * time.Millisecond
			start := time.Now()
			if _, err := clients.Prepare(context.Background()); err == nil {
				t.Fatal("timeout succeeded")
			}
			if time.Since(start) > 3*time.Second {
				t.Fatal("timeout was not bounded")
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if _, err := clients.Prepare(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancel: %v", err)
			}
		})
	}
}

func TestCallCancellationAndTimeout(t *testing.T) {
	for _, behavior := range []string{"wait", "ignore"} {
		t.Run(behavior, func(t *testing.T) {
			marker := t.TempDir() + "/events"
			clients, _ := fixture(t, mcptest.Config{Tools: []*mcp.Tool{mcptest.Tool("slow", false)}, Calls: map[string]string{"slow": behavior}, Marker: marker})
			clients.callTimeout = 100 * time.Millisecond
			catalog, err := clients.Prepare(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			start := time.Now()
			if _, err := catalog.Call(context.Background(), "mcp__project__slow", json.RawMessage(`{}`)); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("call: %v", err)
			}
			if time.Since(start) > time.Second {
				t.Fatal("call waited for remote handler")
			}
			if behavior == "wait" {
				mcptest.WaitFor(t, marker, "cancel slow")
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if _, err := catalog.Call(ctx, "mcp__project__slow", json.RawMessage(`{}`)); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancel: %v", err)
			}
			data, _ := os.ReadFile(marker)
			if strings.Count(string(data), "call slow") != 1 {
				t.Fatalf("call retried: %s", data)
			}
		})
	}
}

func TestDisconnectedServerIsNotRestarted(t *testing.T) {
	marker := t.TempDir() + "/events"
	clients, _ := fixture(t, mcptest.Config{Tools: []*mcp.Tool{mcptest.Tool("exit", true)}, Calls: map[string]string{"exit": "exit"}, Marker: marker})
	catalog, err := clients.Prepare(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.Call(context.Background(), "mcp__project__exit", json.RawMessage(`{}`)); err == nil {
		t.Fatal("exit succeeded")
	}
	if _, err := clients.Prepare(context.Background()); err == nil || !strings.Contains(err.Error(), "close and reload") {
		t.Fatalf("disconnection: %v", err)
	}
	data, _ := os.ReadFile(marker)
	if strings.Count(string(data), "start ") != 1 {
		t.Fatal("restarted disconnected server")
	}
}

func TestCloseKillsRemainingGroupMembers(t *testing.T) {
	marker := t.TempDir() + "/child"
	clients, _ := fixture(t, mcptest.Config{ChildMarker: marker})
	if _, err := clients.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	data := mcptest.WaitFor(t, marker, "\n")
	pid, err := strconv.Atoi(strings.TrimSpace(data))
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	clients.Close()
	if time.Since(start) > 4*time.Second {
		t.Fatal("cleanup exceeded grace")
	}
	// The orphan's reaping is performed by the OS; allow it to finish.
	deadline := time.Now().Add(time.Second)
	for syscall.Kill(pid, 0) == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("child %d survived: %v", pid, err)
	}
}

func TestDuplicateToolFailsDiscovery(t *testing.T) {
	clients, _ := fixture(t, mcptest.Config{Tools: []*mcp.Tool{mcptest.Tool("x", true), mcptest.Tool("x", true)}, PageSize: 1})
	if _, err := clients.Prepare(context.Background()); err == nil || !strings.Contains(err.Error(), "collision") {
		t.Fatalf("duplicate: %v", err)
	}
}

func TestMultipleServersDispatchTheSameToolName(t *testing.T) {
	definitions := map[string]settings.MCPServer{}
	for _, name := range []string{"a", "b"} {
		definition, _ := mcptest.Definition(t, mcptest.Config{Tools: []*mcp.Tool{mcptest.Tool("read_file", true)}, Results: map[string]json.RawMessage{"read_file": json.RawMessage(`{"content":[{"type":"text","text":"` + name + `"}]}`)}})
		definitions[name] = definition
	}
	clients := New(t.TempDir(), definitions)
	defer clients.Close()
	catalog, err := clients.Prepare(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range catalog.Tools() {
		result, err := catalog.Call(context.Background(), tool.Name, json.RawMessage(`{}`))
		if err != nil || !strings.Contains(string(result.Content[0]), `"text":"`+tool.Server+`"`) {
			t.Fatalf("dispatch: %+v %v", result, err)
		}
	}
}

func TestDiscoveryPreservesSchemaNumbers(t *testing.T) {
	tool := mcptest.Tool("number", true)
	tool.InputSchema = json.RawMessage(`{"type":"object","properties":{"id":{"const":9007199254740993}}}`)
	clients, _ := fixture(t, mcptest.Config{Tools: []*mcp.Tool{tool}})
	catalog, err := clients.Prepare(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := string(catalog.Tools()[0].InputSchema); !strings.Contains(got, "9007199254740993") {
		t.Fatalf("schema number changed: %s", got)
	}
}

func TestBlockedWriteHonorsDeadline(t *testing.T) {
	marker := t.TempDir() + "/events"
	clients, _ := fixture(t, mcptest.Config{Tools: []*mcp.Tool{mcptest.Tool("read", true)}, Marker: marker})
	clients.callTimeout = 100 * time.Millisecond
	catalog, err := clients.Prepare(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	line := strings.Split(mcptest.WaitFor(t, marker, "start "), "\n")[0]
	pid, err := strconv.Atoi(strings.TrimPrefix(line, "start "))
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Kill(pid, syscall.SIGSTOP); err != nil {
		t.Fatal(err)
	}
	defer syscall.Kill(pid, syscall.SIGCONT)
	// Release the pipe even if cancellation regresses, so the test can fail.
	timer := time.AfterFunc(4*time.Second, func() { _ = syscall.Kill(pid, syscall.SIGCONT) })
	defer timer.Stop()
	start := time.Now()
	_, err = catalog.Call(context.Background(), "mcp__project__read", json.RawMessage(`{"data":"`+strings.Repeat("x", 1024*1024)+`"}`))
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 3*time.Second {
		t.Fatalf("blocked write took %v: %v", time.Since(start), err)
	}
	clients.Close()
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("blocked server survived: %v", err)
	}
	if _, err := clients.Prepare(context.Background()); err == nil {
		t.Fatal("closed connection reused")
	}
}
