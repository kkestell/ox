package main

import (
	"bufio"
	"crypto/rand"
	"embed"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"sync"

	"github.com/kkestell/ox/internal/paths"
)

//go:embed web/dist
var dist embed.FS

func main() {
	addr := flag.String("addr", "localhost:8080", "address to serve the web UI on")
	socketPath := flag.String("socket", "", "override the oxd socket in the user runtime directory")
	flag.Parse()
	if *socketPath == "" {
		var err error
		if *socketPath, err = paths.Socket(); err != nil {
			log.Fatal(err)
		}
	}
	web, err := fs.Sub(dist, "web/dist")
	if err != nil {
		log.Fatal(err)
	}

	r := &relay{socket: *socketPath, conns: make(map[string]net.Conn)}
	http.Handle("GET /", http.FileServerFS(web))
	http.HandleFunc("GET /events", r.events)
	http.HandleFunc("POST /rpc", r.rpc)
	log.Fatal(http.ListenAndServe(*addr, nil))
}

type relay struct {
	socket string
	mu     sync.Mutex
	conns  map[string]net.Conn
}

func (r *relay) events(w http.ResponseWriter, req *http.Request) {
	conn, err := net.Dial("unix", r.socket)
	if err != nil {
		http.Error(w, fmt.Sprintf("connect to oxd: %v", err), http.StatusBadGateway)
		return
	}
	defer conn.Close()
	id := rand.Text()
	r.mu.Lock()
	r.conns[id] = conn
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.conns, id)
		r.mu.Unlock()
	}()
	go func() {
		<-req.Context().Done()
		conn.Close()
	}()

	w.Header().Set("Content-Type", "text/event-stream")
	flusher := w.(http.Flusher)
	fmt.Fprintf(w, "event: connection\ndata: %s\n\n", id)
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

// rpc writes one JSON-RPC request from the page to its oxd connection. The
// response arrives on the connection's event stream.
func (r *relay) rpc(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	conn := r.conns[req.URL.Query().Get("connection")]
	r.mu.Unlock()
	if conn == nil {
		http.Error(w, "unknown connection", http.StatusNotFound)
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
