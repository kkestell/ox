package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"sync"

	"github.com/kkestell/ox/internal/config"
	"github.com/kkestell/ox/internal/jsonrpc"
	"github.com/kkestell/ox/internal/paths"
)

//go:embed web/dist
var dist embed.FS

// tailnetPort is where ox serves the web UI on the configured tailscale_ip,
// whatever -addr's port.
const tailnetPort = 17840

func main() {
	addr := flag.String("addr", "localhost:8080", "address to serve the web UI on")
	socketPath := flag.String("socket", "", "override the oxd socket in the user runtime directory")
	configPath := flag.String("config", "", "override the configuration in the user config directory")
	flag.Parse()
	if *socketPath == "" {
		var err error
		if *socketPath, err = paths.Socket(); err != nil {
			log.Fatal(err)
		}
	}
	if *configPath == "" {
		var err error
		if *configPath, err = paths.Config(); err != nil {
			log.Fatal(err)
		}
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatal(err)
	}
	web, err := fs.Sub(dist, "web/dist")
	if err != nil {
		log.Fatal(err)
	}

	addrs := webAddrs(*addr, cfg.TailscaleIP)
	r, err := newRelay(*socketPath, addrs)
	if err != nil {
		log.Fatal(err)
	}
	listeners := make([]net.Listener, len(addrs))
	for i, a := range addrs {
		if listeners[i], err = net.Listen("tcp", a); err != nil {
			log.Fatal(err)
		}
	}
	for _, a := range addrs[1:] {
		fmt.Printf("http://%s\n", a)
	}
	handler := r.handler(web)
	for _, l := range listeners[1:] {
		go func() { log.Fatal(http.Serve(l, handler)) }()
	}
	log.Fatal(http.Serve(listeners[0], handler))
}

// webAddrs returns the addresses ox serves the web UI on: addr, then the
// tailnet address when tailscaleIP is set.
func webAddrs(addr string, tailscaleIP netip.Addr) []string {
	if !tailscaleIP.IsValid() {
		return []string{addr}
	}
	return []string{addr, netip.AddrPortFrom(tailscaleIP, tailnetPort).String()}
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
	hosts  []string // the hosts of the addresses ox serves on
	mu     sync.Mutex
	conns  map[string]net.Conn
}

// newRelay returns a relay to the oxd socket that accepts requests addressed
// to the hosts of addrs. Host names are case-insensitive, and the relay keeps
// and compares them in lowercase.
func newRelay(socket string, addrs []string) (*relay, error) {
	r := &relay{socket: socket, conns: make(map[string]net.Conn)}
	for _, a := range addrs {
		host, err := serveHost(a)
		if err != nil {
			return nil, err
		}
		r.hosts = append(r.hosts, strings.ToLower(host))
	}
	return r, nil
}

// handler serves the web UI, /events, /rpc, and /images.
func (r *relay) handler(web fs.FS) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /", http.FileServerFS(web))
	mux.Handle("GET /events", r.checkHost(http.HandlerFunc(r.events)))
	mux.Handle("GET /images/{session_id}/{image_id}", r.checkHost(http.HandlerFunc(r.image)))
	mux.Handle("POST /rpc", r.checkHost(http.NewCrossOriginProtection().Handler(http.HandlerFunc(r.rpc))))
	return mux
}

// checkHost refuses a request not addressed to a loopback name or a host ox
// serves on. A page whose domain rebinds to one of these addresses is
// same-origin to the browser, but its requests still name that domain in Host.
func (r *relay) checkHost(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		host, _, err := net.SplitHostPort(req.Host)
		if err != nil {
			host = req.Host
		}
		host = strings.ToLower(strings.Trim(host, "[]"))
		if !slices.Contains([]string{"localhost", "127.0.0.1", "::1"}, host) && !slices.Contains(r.hosts, host) {
			http.Error(w, "unknown host", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, req)
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
// response arrives on the connection's event stream. A connection that is
// unknown or fails to write is gone, which the page reads from 503 as a lost
// connection.
func (r *relay) rpc(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	conn := r.conns[req.URL.Query().Get("connection")]
	r.mu.Unlock()
	if conn == nil {
		http.Error(w, "unknown connection", http.StatusServiceUnavailable)
		return
	}
	// A 1 MiB prompt can take six times that as JSON escapes, and 10 MiB of
	// images about 13.3 MiB as base64, plus the envelope.
	body, err := io.ReadAll(http.MaxBytesReader(w, req.Body, 24<<20))
	if _, tooLarge := errors.AsType[*http.MaxBytesError](err); tooLarge {
		http.Error(w, "request is larger than 24 MiB", http.StatusRequestEntityTooLarge)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if _, err := conn.Write(append(body, '\n')); err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// image serves an image attached to a prompt, which it asks oxd for over a
// connection of its own, so image data never travels a page's event stream.
// The type is the one oxd took from the image's bytes, and an image ID never
// names other bytes, so the browser may keep it.
func (r *relay) image(w http.ResponseWriter, req *http.Request) {
	conn, err := net.Dial("unix", r.socket)
	if err != nil {
		http.Error(w, fmt.Sprintf("connect to oxd: %v", err), http.StatusBadGateway)
		return
	}
	defer conn.Close()
	stop := context.AfterFunc(req.Context(), func() { conn.Close() })
	defer stop()

	request, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "session/image",
		"params": map[string]string{"session_id": req.PathValue("session_id"), "image_id": req.PathValue("image_id")},
	})
	if _, err := conn.Write(append(request, '\n')); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	type answer struct {
		ID     json.RawMessage `json:"id"`
		Result *struct {
			MimeType string `json:"mime_type"`
			Data     []byte `json:"data"`
		} `json:"result"`
		Error *jsonrpc.Error `json:"error"`
	}
	var response answer
	lines := bufio.NewReader(conn)
	// Notifications oxd broadcasts to every connection can come before the
	// answer.
	for string(response.ID) != "1" {
		line, err := lines.ReadBytes('\n')
		if err == nil {
			response = answer{}
			err = json.Unmarshal(line, &response)
		}
		if err != nil {
			http.Error(w, fmt.Sprintf("read from oxd: %v", err), http.StatusBadGateway)
			return
		}
	}
	switch {
	case response.Error != nil && response.Error.Code == notFound:
		http.Error(w, response.Error.Message, http.StatusNotFound)
	case response.Error != nil:
		http.Error(w, response.Error.Message, http.StatusBadGateway)
	case response.Result == nil:
		http.Error(w, "oxd answered without an image", http.StatusBadGateway)
	default:
		w.Header().Set("Content-Type", response.Result.MimeType)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
		w.Write(response.Result.Data)
	}
}

// notFound is oxd's error code for a session or image that does not exist.
const notFound = -32001
