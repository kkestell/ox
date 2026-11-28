package engine

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"slices"
	"strings"

	"github.com/kkestell/ox/internal/agent"
)

// protocolVersion is the MCP version ur asks for.
const protocolVersion = "2025-06-18"

// forwarder is the MCP forwarder oxd names, which ur runs to reach Ox's
// tools as session sessionID. Only the loop goroutine writes to it, after
// start; its reader goroutine reads its answers.
type forwarder struct {
	sessionID   string
	cmd         *exec.Cmd
	stdin       io.WriteCloser
	stdout      *bufio.Reader
	definitions []json.RawMessage // the tools' function definitions, which every request offers
	names       []string          // the tools' names, which the model calls them by
	lastID      int               // the latest request's ID
	exited      chan struct{}     // closed once the forwarder has exited and its output is read
}

// answered is the forwarder's answer to the tools/call request id.
type answered struct {
	id      int
	outcome outcome
}

// forwarderExited reports that the forwarder has exited, with its exit's
// error.
type forwarderExited struct{ err error }

// response is a JSON-RPC response from the forwarder.
type response struct {
	ID     *int            `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// startForwarder starts server's forwarder for sessionID, with ur's
// environment, less the OpenRouter key, and the server's, and its stderr as
// ur's. It initializes the MCP connection and lists Ox's tools, one request
// at a time, since oxd may answer concurrent requests in either order. A
// forwarder that fails to start is ended.
func startForwarder(server agent.MCPServer, sessionID string, stderr io.Writer) (*forwarder, error) {
	cmd := exec.Command(server.Command)
	cmd.Env = environment()
	for name, value := range server.Env {
		cmd.Env = append(cmd.Env, name+"="+value)
	}
	cmd.Stderr = stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	f := &forwarder{sessionID: sessionID, cmd: cmd, stdin: stdin, stdout: bufio.NewReader(stdout), exited: make(chan struct{})}
	if err := f.list(); err != nil {
		stdin.Close()
		cmd.Wait()
		return nil, err
	}
	return f, nil
}

// list initializes the connection and reads Ox's tools.
func (f *forwarder) list() error {
	if _, err := f.request("initialize", map[string]any{
		"protocolVersion": protocolVersion, "capabilities": map[string]any{},
		"clientInfo": map[string]string{"name": "ur", "version": "0"},
	}); err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	if _, err := f.stdin.Write([]byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n")); err != nil {
		return err
	}
	result, err := f.request("tools/list", struct{}{})
	if err != nil {
		return fmt.Errorf("tools/list: %w", err)
	}
	var list struct {
		Tools []struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			InputSchema json.RawMessage `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(result, &list); err != nil {
		return fmt.Errorf("tools/list: %w", err)
	}
	for _, tool := range list.Tools {
		// Strings and valid JSON always marshal.
		definition, _ := json.Marshal(map[string]any{"type": "function", "function": map[string]any{
			"name": tool.Name, "description": tool.Description, "parameters": tool.InputSchema,
		}})
		f.definitions = append(f.definitions, definition)
		f.names = append(f.names, tool.Name)
	}
	return nil
}

// request writes a request and reads lines until its response, and returns
// its result.
func (f *forwarder) request(method string, params any) (json.RawMessage, error) {
	id, err := f.write(method, params)
	if err != nil {
		return nil, err
	}
	for {
		line, err := f.stdout.ReadBytes('\n')
		if len(line) > 0 {
			var r response
			if json.Unmarshal(line, &r) == nil && r.ID != nil && *r.ID == id {
				if r.Error != nil {
					return nil, errors.New(r.Error.Message)
				}
				return r.Result, nil
			}
		}
		if err != nil {
			return nil, errors.New("ox-mcp exited")
		}
	}
}

// write writes a request and returns its ID.
func (f *forwarder) write(method string, params any) (int, error) {
	f.lastID++
	// Strings and the tools' arguments, valid JSON, always marshal.
	line, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": f.lastID, "method": method, "params": params})
	_, err := f.stdin.Write(append(line, '\n'))
	return f.lastID, err
}

// offers reports whether name is one of Ox's tools. Without a forwarder Ur
// has none.
func (f *forwarder) offers(name string) bool {
	return f != nil && slices.Contains(f.names, name)
}

// call writes a tools/call request for the model's call toolUseID and
// returns its ID. oxd takes the tool_use ID where Claude Code sends it, so a
// child's send_message to its parent stands in for its report.
func (f *forwarder) call(name string, arguments json.RawMessage, toolUseID string) (int, error) {
	return f.write("tools/call", map[string]any{
		"name": name, "arguments": arguments, "_meta": map[string]string{"claudecode/toolUseId": toolUseID},
	})
}

// read delivers each answer until the forwarder's output ends, then reaps it,
// closes exited, and delivers its exit.
func (f *forwarder) read(deliver func(event)) {
	for {
		line, err := f.stdout.ReadBytes('\n')
		var r response
		if len(line) > 0 && json.Unmarshal(line, &r) == nil && r.ID != nil {
			deliver(answered{*r.ID, r.outcome()})
		}
		if err != nil {
			break
		}
	}
	err := f.cmd.Wait()
	close(f.exited)
	deliver(forwarderExited{err})
}

// outcome is a tools/call response's outcome: the result's text, or the
// error's message.
func (r response) outcome() outcome {
	if r.Error != nil {
		return outcome{r.Error.Message, true}
	}
	var result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(r.Result, &result); err != nil {
		return outcome{"tools/call: " + err.Error(), true}
	}
	var text strings.Builder
	for _, part := range result.Content {
		text.WriteString(part.Text)
	}
	return outcome{text.String(), result.IsError}
}

// close closes the forwarder's stdin, which ends it, and waits for it to
// exit: it half-closes its connection to oxd, which then closes it.
func (f *forwarder) close() {
	f.stdin.Close()
	<-f.exited
}
