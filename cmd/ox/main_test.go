package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitializeAndStdoutPurity(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "ox")
	build := exec.Command("go", "build", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build ox: %v\n%s", err, output)
	}

	var stderr bytes.Buffer
	command := exec.Command(binary)
	command.Env = append(os.Environ(), "OX_LOG_LEVEL=debug")
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(stdout)

	writeRequest(t, stdin, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1,"clientCapabilities":{"fs":{"readTextFile":true,"writeTextFile":true},"terminal":true},"clientInfo":{"name":"test-client","version":"1.0.0"}}}`)
	response := readResponse(t, scanner)
	if response["jsonrpc"] != "2.0" || response["id"] != float64(1) {
		t.Fatalf("unexpected initialize response: %#v", response)
	}
	result, ok := response["result"].(map[string]any)
	if !ok || result["protocolVersion"] != float64(1) {
		t.Fatalf("unexpected initialize result: %#v", response)
	}
	capabilities, ok := result["agentCapabilities"].(map[string]any)
	if !ok || capabilities["loadSession"] != false {
		t.Fatalf("unexpected agent capabilities: %#v", result["agentCapabilities"])
	}
	promptCapabilities, ok := capabilities["promptCapabilities"].(map[string]any)
	if !ok || len(promptCapabilities) != 0 {
		t.Fatalf("unexpected prompt capabilities: %#v", capabilities["promptCapabilities"])
	}
	authMethods, ok := result["authMethods"].([]any)
	if !ok || len(authMethods) != 0 {
		t.Fatalf("unexpected auth methods: %#v", result["authMethods"])
	}
	agentInfo, ok := result["agentInfo"].(map[string]any)
	if !ok || agentInfo["name"] != "ox" || agentInfo["version"] != "0.0.1" {
		t.Fatalf("unexpected agent info: %#v", result["agentInfo"])
	}

	writeRequest(t, stdin, `{"jsonrpc":"2.0","id":2,"method":"initialize","params":{"protocolVersion":999}}`)
	response = readResponse(t, scanner)
	if errorCode(response) != 0 || response["result"].(map[string]any)["protocolVersion"] != float64(1) {
		t.Fatalf("unexpected version negotiation response: %#v", response)
	}

	writeRequest(t, stdin, `{"jsonrpc":"2.0","id":3,"method":"initialize","params":{}}`)
	if response = readResponse(t, scanner); errorCode(response) != -32602 {
		t.Fatalf("missing protocol version response: %#v", response)
	}

	writeRequest(t, stdin, `{"jsonrpc":"2.0","id":4,"method":"unknown","params":{}}`)
	if response = readResponse(t, scanner); errorCode(response) != -32601 {
		t.Fatalf("unknown method response: %#v", response)
	}

	writeRequest(t, stdin, `{not json`)
	if response = readResponse(t, scanner); errorCode(response) != -32700 {
		t.Fatalf("malformed JSON response: %#v", response)
	}

	writeRequest(t, stdin, `{"jsonrpc":"2.0","method":"$/cancel_request","params":{"requestId":"missing"}}`)
	writeRequest(t, stdin, `{"jsonrpc":"2.0","method":"$/cancel_request","params":{"requestId":999}}`)
	writeRequest(t, stdin, `{"jsonrpc":"2.0","method":"$/cancel_request","params":{"requestId":null}}`)
	writeRequest(t, stdin, `{"jsonrpc":"2.0","id":5,"method":"initialize","params":{"protocolVersion":1}}`)
	if response = readResponse(t, scanner); response["id"] != float64(5) || response["result"] == nil {
		t.Fatalf("server did not remain alive after cancellation notifications: %#v", response)
	}

	if err := stdin.Close(); err != nil {
		t.Fatal(err)
	}
	for scanner.Scan() {
		var message map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
			t.Fatalf("stdout was not JSON-RPC: %q: %v", scanner.Text(), err)
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err != nil {
		t.Fatalf("ox exit: %v\nstderr:\n%s", err, stderr.String())
	}
	if !strings.Contains(stderr.String(), `level=INFO msg="ox starting"`) {
		t.Errorf("stderr = %q, want info startup log", stderr.String())
	}
}

func TestBinaryExitsCleanlyOnStdinEOF(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "ox")
	build := exec.Command("go", "build", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build ox: %v\n%s", err, output)
	}

	for _, logLevel := range []string{"", "unrecognized"} {
		t.Run(logLevel, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			command := exec.Command(binary)
			command.Env = []string{"OX_LOG_LEVEL=" + logLevel}
			command.Stdin = strings.NewReader("")
			command.Stdout = &stdout
			command.Stderr = &stderr

			if err := command.Run(); err != nil {
				t.Fatalf("run ox: %v", err)
			}
			if stdout.Len() != 0 {
				t.Errorf("stdout = %q, want empty", stdout.String())
			}
			if !strings.Contains(stderr.String(), `level=INFO msg="ox starting"`) {
				t.Errorf("stderr = %q, want info startup log", stderr.String())
			}
		})
	}
}

func writeRequest(t *testing.T, stdin io.Writer, request string) {
	t.Helper()
	if _, err := fmt.Fprintln(stdin, request); err != nil {
		t.Fatal(err)
	}
}

func readResponse(t *testing.T, scanner *bufio.Scanner) map[string]any {
	t.Helper()
	if !scanner.Scan() {
		t.Fatalf("read response: %v", scanner.Err())
	}
	var response map[string]any
	if err := json.Unmarshal(scanner.Bytes(), &response); err != nil {
		t.Fatalf("stdout was not JSON-RPC: %q: %v", scanner.Text(), err)
	}
	return response
}

func errorCode(response map[string]any) float64 {
	errorObject, _ := response["error"].(map[string]any)
	code, _ := errorObject["code"].(float64)
	return code
}
