// ox-mcp connects a harness's MCP client, over stdio, to oxd's socket on
// behalf of one session.
package main

import (
	"bufio"
	"encoding/json"
	"io"
	"log"
	"net"
	"os"
)

func main() {
	log.SetFlags(0)
	log.SetPrefix("ox-mcp: ")
	socket, sessionID := os.Getenv("OX_SOCKET"), os.Getenv("OX_SESSION_ID")

	conn, err := net.Dial("unix", socket)
	if err != nil {
		log.Fatalf("connect to oxd at %q: %v", socket, err)
	}
	defer conn.Close()

	attach, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": "attach", "method": "mcp/attach",
		"params": map[string]string{"session_id": sessionID},
	})
	if _, err := conn.Write(append(attach, '\n')); err != nil {
		log.Fatalf("connect to oxd at %q: %v", socket, err)
	}

	// Everything before the attach result is a notification meant for pages.
	reader := bufio.NewReader(conn)
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			log.Fatal("oxd closed the connection before attaching")
		}
		var response struct {
			ID    string `json:"id"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(line, &response) != nil || response.ID != "attach" {
			continue
		}
		if response.Error != nil {
			log.Fatalf("attach to session %q: %s", sessionID, response.Error.Message)
		}
		break
	}

	go func() {
		io.Copy(conn, os.Stdin)
		conn.(*net.UnixConn).CloseWrite()
	}()
	io.Copy(os.Stdout, reader)
}
