package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/kkestell/ox/internal/agents"
	"github.com/kkestell/ox/internal/protocol"
)

func main() {
	socket := flag.String("socket", "", "override the socket in the user cache directory")
	flag.Parse()
	path := *socket
	if path == "" {
		cache, err := os.UserCacheDir()
		if err != nil {
			log.Fatal(err)
		}
		dir := filepath.Join(cache, "ox")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			log.Fatal(err)
		}
		path = filepath.Join(dir, "oxd.sock")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, path); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, path string) error {
	listener, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	registry := agents.NewRegistry()
	defer registry.Close()
	defer listener.Close()
	stopClosing := context.AfterFunc(ctx, func() { listener.Close() })
	defer stopClosing()

	for {
		conn, err := listener.Accept()
		if errors.Is(err, net.ErrClosed) {
			return nil
		}
		if err != nil {
			return err
		}
		go func() {
			defer conn.Close()
			if err := protocol.Serve(conn, registry); err != nil {
				log.Printf("client: %v", err)
			}
		}()
	}
}
