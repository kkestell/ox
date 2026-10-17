// Package mcptest runs a controllable MCP stdio server in test subprocesses.
package mcptest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"ox/internal/settings"
)

// Config can be rewritten between turns to test refreshed discovery.
type Config struct {
	Tools       []*mcp.Tool
	Results     map[string]json.RawMessage
	PageSize    int
	Startup     string
	Discovery   string
	Calls       map[string]string
	Marker      string
	ChildMarker string
}

func Definition(t *testing.T, config Config) (settings.MCPServer, string) {
	t.Helper()
	path := t.TempDir() + "/server.json"
	Write(t, path, config)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return settings.MCPServer{Command: executable, Args: []string{"-test.run=^TestMCPHelper$"}, Env: map[string]string{"OX_MCP_TEST_CONFIG": path}}, path
}

func Write(t *testing.T, path string, config Config) {
	t.Helper()
	data, err := json.Marshal(config)
	if err == nil {
		err = os.WriteFile(path, data, 0o600)
	}
	if err != nil {
		t.Fatal(err)
	}
}

func Tool(name string, readOnly bool) *mcp.Tool {
	return &mcp.Tool{Name: name, Description: "Test " + name, InputSchema: json.RawMessage(`{"type":"object","properties":{"number":{"type":"integer"}}}`), Annotations: &mcp.ToolAnnotations{ReadOnlyHint: readOnly}}
}

func mark(path, text string) {
	if path == "" {
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	fmt.Fprintln(f, text)
}

// Run exits only in a helper subprocess; each test package calls it from
// TestMCPHelper. All protocol handling stays in the official SDK.
func Run() {
	if path := os.Getenv("OX_MCP_TEST_CHILD"); path != "" {
		signal.Ignore(syscall.SIGTERM)
		mark(path, strconv.Itoa(os.Getpid()))
		for {
			time.Sleep(time.Hour)
		}
	}
	path := os.Getenv("OX_MCP_TEST_CONFIG")
	if path == "" {
		return
	}
	read := func() Config {
		data, err := os.ReadFile(path)
		if err != nil {
			panic(err)
		}
		var config Config
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.UseNumber()
		if err = decoder.Decode(&config); err != nil {
			panic(err)
		}
		return config
	}
	config := read()
	mark(config.Marker, fmt.Sprintf("start %d", os.Getpid()))
	if config.ChildMarker != "" {
		executable, _ := os.Executable()
		child := exec.Command(executable, "-test.run=^TestMCPHelper$")
		child.Env = append(os.Environ(), "OX_MCP_TEST_CHILD="+config.ChildMarker)
		child.Stdout = os.Stdout
		if err := child.Start(); err != nil {
			panic(err)
		}
		go func() { _ = child.Wait() }()
	}
	if config.Startup == "exit" {
		os.Exit(1)
	}
	if config.Startup == "wait" {
		for {
			time.Sleep(time.Hour)
		}
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "fixture", Version: "1"}, nil)
	server.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			config := read()
			switch method {
			case "tools/list":
				mark(config.Marker, "list")
				if config.Discovery == "error" {
					return nil, errors.New("discovery failed")
				}
				if config.Discovery == "wait" {
					<-ctx.Done()
					return nil, ctx.Err()
				}
				params := req.GetParams().(*mcp.ListToolsParams)
				offset := 0
				if params != nil && params.Cursor != "" {
					offset, _ = strconv.Atoi(params.Cursor)
				}
				size := config.PageSize
				if size <= 0 {
					size = len(config.Tools)
				}
				end := min(offset+size, len(config.Tools))
				result := &mcp.ListToolsResult{Tools: config.Tools[offset:end]}
				if end < len(config.Tools) {
					result.NextCursor = strconv.Itoa(end)
				}
				return result, nil
			case "tools/call":
				params := req.GetParams().(*mcp.CallToolParamsRaw)
				mark(config.Marker, "call "+params.Name+" "+string(params.Arguments))
				switch config.Calls[params.Name] {
				case "wait":
					<-ctx.Done()
					mark(config.Marker, "cancel "+params.Name)
					return nil, ctx.Err()
				case "ignore":
					for {
						time.Sleep(time.Hour)
					}
				case "exit":
					os.Exit(1)
				case "error":
					return nil, errors.New("protocol failure")
				case "environment":
					cwd, _ := os.Getwd()
					data, _ := json.Marshal(map[string]any{"cwd": cwd, "args": os.Args, "env": os.Environ()})
					return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(data)}}}, nil
				}
				if raw := config.Results[params.Name]; raw != nil {
					return &wireResult{raw: raw}, nil
				}
				return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(params.Arguments)}}}, nil
			}
			return next(ctx, method, req)
		}
	})
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

func WaitFor(t *testing.T, path, substring string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, _ := os.ReadFile(path)
		if strings.Contains(string(data), substring) {
			return string(data)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s did not contain %q", path, substring)
	return ""
}

// wireResult lets tests send malformed results through the SDK transport.
type wireResult struct {
	mcp.ResultBase
	raw json.RawMessage
}

func (r *wireResult) MarshalJSON() ([]byte, error) { return r.raw, nil }
