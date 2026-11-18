// Command oxd is the Ox daemon. It serves the protocol in docs/protocol.md on
// a Unix socket until interrupted, then stops every Claude process. Workspaces
// and sessions are stored in a state file and outlive the daemon.
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
	"github.com/kkestell/ox/internal/store"
)

func main() {
	socketPath := flag.String("socket", "", "override the socket in the user cache directory")
	statePath := flag.String("state", "", "override the state file in the user state directory")
	flag.Parse()
	path := *socketPath
	if path == "" {
		var err error
		if path, err = socket.Path(); err != nil {
			log.Fatal(err)
		}
	}
	if *statePath == "" {
		var err error
		if *statePath, err = store.Path(); err != nil {
			log.Fatal(err)
		}
	}
	s, err := server.New(*statePath)
	if err != nil {
		log.Fatalf("load state: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	listener, err := net.Listen("unix", path)
	if err != nil {
		log.Fatal(err)
	}
	context.AfterFunc(ctx, func() { listener.Close() })

	err = s.Serve(listener)
	s.Close()
	if err != nil {
		log.Fatal(err)
	}
}
