// Command ox serves the Ox web UI. Each page gets its own connection to oxd:
// ox streams the connection's messages to the page as Server-Sent Events and
// writes the page's requests to it, so the page speaks the oxd protocol
// directly.
package main

import (
	"bufio"
	"crypto/rand"
	_ "embed"
	"flag"
	"fmt"
	"html/template"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"sync"

	"github.com/kkestell/ox/internal/socket"
)

//go:embed web/index.html
var index string

var page = template.Must(template.New("index").Parse(index))

func main() {
	addr := flag.String("addr", "localhost:8080", "address to serve the web UI on")
	socketPath := flag.String("socket", "", "override the oxd socket in the user cache directory")
	flag.Parse()
	path := *socketPath
	if path == "" {
		var err error
		if path, err = socket.Path(); err != nil {
			log.Fatal(err)
		}
	}
	workspace, err := os.Getwd()
	if err != nil {
		log.Fatal(err)
	}

	r := &relay{socket: path, sessions: make(map[string]net.Conn)}
	http.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		page.Execute(w, struct{ Workspace string }{workspace})
	})
	http.HandleFunc("GET /events", r.events)
	http.HandleFunc("POST /rpc", r.rpc)
	log.Printf("serving http://%s", *addr)
	log.Fatal(http.ListenAndServe(*addr, nil))
}

// relay holds one oxd connection per open page, keyed by session ID.
type relay struct {
	socket   string
	mu       sync.Mutex
	sessions map[string]net.Conn
}

// events opens a session: it connects to oxd, sends the session ID as a
// "session" event, then forwards every line from oxd as a message until the
// page or oxd closes.
func (r *relay) events(w http.ResponseWriter, req *http.Request) {
	conn, err := net.Dial("unix", r.socket)
	if err != nil {
		http.Error(w, fmt.Sprintf("connect to oxd: %v", err), http.StatusBadGateway)
		return
	}
	defer conn.Close()
	id := rand.Text()
	r.mu.Lock()
	r.sessions[id] = conn
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.sessions, id)
		r.mu.Unlock()
	}()
	go func() {
		<-req.Context().Done()
		conn.Close()
	}()

	w.Header().Set("Content-Type", "text/event-stream")
	flusher := w.(http.Flusher)
	fmt.Fprintf(w, "event: session\ndata: %s\n\n", id)
	flusher.Flush()
	lines := bufio.NewReader(conn)
	for {
		line, err := lines.ReadBytes('\n')
		if err != nil {
			return
		}
		fmt.Fprintf(w, "data: %s\n", line)
		flusher.Flush()
	}
}

// rpc writes one JSON-RPC request from the page to its session's connection.
// The response arrives on the session's event stream.
func (r *relay) rpc(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	conn := r.sessions[req.URL.Query().Get("session")]
	r.mu.Unlock()
	if conn == nil {
		http.Error(w, "unknown session", http.StatusNotFound)
		return
	}
	body, err := io.ReadAll(io.LimitReader(req.Body, 1<<20))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if _, err := conn.Write(append(body, '\n')); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
