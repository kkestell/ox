package main

import (
	"bufio"
	"crypto/rand"
	"embed"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"strings"
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

	host, err := serveHost(*addr)
	if err != nil {
		log.Fatal(err)
	}
	r := &relay{socket: *socketPath, host: host, conns: make(map[string]net.Conn)}
	log.Fatal(http.ListenAndServe(*addr, r.handler(web)))
}

// serveHost returns the host addr names. An address that names every
// interface would let any machine on the network reach oxd: checkHost only
// stops browsers, and other clients choose their own Host header.
func serveHost(addr string) (string, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return "", err
	}
	if ip := net.ParseIP(host); host == "" || ip != nil && ip.IsUnspecified() {
		return "", fmt.Errorf("-addr %q must name a host, not every interface", addr)
	}
	return host, nil
}

type relay struct {
	socket string
	host   string // the host given to -addr
	mu     sync.Mutex
	conns  map[string]net.Conn
}

// handler serves the web UI, /events, and /rpc.
func (r *relay) handler(web fs.FS) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /", http.FileServerFS(web))
	mux.Handle("GET /events", r.checkHost(http.HandlerFunc(r.events)))
	mux.Handle("POST /rpc", r.checkHost(http.NewCrossOriginProtection().Handler(http.HandlerFunc(r.rpc))))
	return mux
}

// checkHost refuses a request not addressed to a loopback name or the host
// given to -addr. A page whose domain rebinds to this address is same-origin
// to the browser, but its requests still name that domain in Host.
func (r *relay) checkHost(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		host, _, err := net.SplitHostPort(req.Host)
		if err != nil {
			host = req.Host
		}
		switch strings.Trim(host, "[]") {
		case "localhost", "127.0.0.1", "::1", r.host:
			next.ServeHTTP(w, req)
		default:
			http.Error(w, "unknown host", http.StatusForbidden)
		}
	})
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
	// A 1 MiB prompt can take six times that as JSON escapes, plus the envelope.
	body, err := io.ReadAll(http.MaxBytesReader(w, req.Body, 8<<20))
	if _, tooLarge := errors.AsType[*http.MaxBytesError](err); tooLarge {
		http.Error(w, "request is larger than 8 MiB", http.StatusRequestEntityTooLarge)
		return
	}
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
