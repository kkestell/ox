// Command oxd is the Ox daemon. It serves the protocol in docs/protocol.md on
// a Unix socket until interrupted, then stops every agent.
package main

import (
	"context"
	"flag"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/kkestell/ox/internal/server"
	"github.com/kkestell/ox/internal/socket"
)

func main() {
	socketPath := flag.String("socket", "", "override the socket in the user cache directory")
	flag.Parse()
	path := *socketPath
	if path == "" {
		var err error
		if path, err = socket.Path(); err != nil {
			log.Fatal(err)
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	listener, err := net.Listen("unix", path)
	if err != nil {
		log.Fatal(err)
	}
	context.AfterFunc(ctx, func() { listener.Close() })

	s := server.New()
	err = s.Serve(listener)
	s.Close()
	if err != nil {
		log.Fatal(err)
	}
}
