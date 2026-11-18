// Command oxd is the Ox daemon. It serves the protocol in docs/protocol.md on
// a Unix socket until interrupted, then stops every Claude process. Workspaces,
// sessions, and session events are stored in a SQLite database and outlive the
// daemon.
package main

import (
	"context"
	"flag"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kkestell/ox/internal/server"
	"github.com/kkestell/ox/internal/socket"
	"github.com/kkestell/ox/internal/store"
)

func main() {
	socketPath := flag.String("socket", "", "override the socket in the user cache directory")
	dbPath := flag.String("db", "", "override the database in the user state directory")
	parkAfter := flag.Duration("park-after", 5*time.Minute, "stop a session's Claude process after it has been unused this long")
	flag.Parse()
	path := *socketPath
	if path == "" {
		var err error
		if path, err = socket.Path(); err != nil {
			log.Fatal(err)
		}
	}
	if *dbPath == "" {
		var err error
		if *dbPath, err = store.Path(); err != nil {
			log.Fatal(err)
		}
	}
	st, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer st.Close()
	s, err := server.New(st, *parkAfter)
	if err != nil {
		log.Fatalf("load database: %v", err)
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
