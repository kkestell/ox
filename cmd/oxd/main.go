package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/kkestell/ox/internal/agent"
	"github.com/kkestell/ox/internal/claude"
	"github.com/kkestell/ox/internal/config"
	"github.com/kkestell/ox/internal/fake"
	"github.com/kkestell/ox/internal/paths"
	"github.com/kkestell/ox/internal/server"
	"github.com/kkestell/ox/internal/store"
)

func main() {
	socketPath := flag.String("socket", "", "override the socket in the user runtime directory")
	dbPath := flag.String("db", "", "override the database in the user state directory")
	configPath := flag.String("config", "", "override the configuration in the user config directory")
	parkAfter := flag.Duration("park-after", 5*time.Minute, "stop a session's harness process after it has been unused this long")
	flag.Parse()
	defaultPath(socketPath, paths.Socket)
	defaultPath(dbPath, paths.Database)
	defaultPath(configPath, paths.Config)
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatal(err)
	}
	harnesses := map[string]agent.Start{"claude": claude.Start, "fake": fake.Start}
	for name := range cfg.Harnesses {
		if harnesses[name] == nil {
			log.Fatalf("%s: unknown harness %q", *configPath, name)
		}
	}

	st, err := store.Open(*dbPath)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer st.Close()
	s, err := server.New(st, cfg, *parkAfter, harnesses)
	if err != nil {
		log.Fatalf("load database: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	listener, err := listen(*socketPath)
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

// defaultPath sets an empty path flag to its default.
func defaultPath(flag *string, path func() (string, error)) {
	if *flag != "" {
		return
	}
	var err error
	if *flag, err = path(); err != nil {
		log.Fatal(err)
	}
}

// listen serves the socket at path. A socket file that refuses connections
// was left by an oxd that exited without closing its listener, as a race
// report or a closed terminal does, and is replaced.
func listen(path string) (net.Listener, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	listener, err := net.Listen("unix", path)
	if !errors.Is(err, syscall.EADDRINUSE) {
		return listener, err
	}
	conn, dialErr := net.Dial("unix", path)
	if dialErr == nil {
		conn.Close()
		return nil, fmt.Errorf("another oxd is serving %s", path)
	}
	if !errors.Is(dialErr, syscall.ECONNREFUSED) {
		return nil, err
	}
	if err := os.Remove(path); err != nil {
		return nil, err
	}
	return net.Listen("unix", path)
}
