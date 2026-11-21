package main

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"os"
	"os/exec"
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
	"github.com/kkestell/ox/internal/ur"
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
	// The forwarder runs in a session's workspace, so it needs an absolute
	// socket path.
	socket, err := filepath.Abs(*socketPath)
	if err != nil {
		log.Fatal(err)
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatal(err)
	}
	harnesses := map[string]agent.Harness{
		"claude": claude.Harness,
		"fake":   {Start: fake.Start},
		"ur":     ur.Harness(ur.Endpoint, cfg.Harnesses["ur"].Providers()),
	}
	if err := checkConfig(cfg, harnesses); err != nil {
		log.Fatalf("%s: %v", *configPath, err)
	}
	forwarder, err := findForwarder()
	if err != nil {
		log.Fatalf("ox-mcp not found beside oxd: %v", err)
	}

	if err := run(*dbPath, cfg, *parkAfter, harnesses, server.MCP{Forwarder: forwarder, Socket: socket}); err != nil {
		log.Fatal(err)
	}
}

func run(dbPath string, cfg *config.Config, parkAfter time.Duration, harnesses map[string]agent.Harness, mcp server.MCP) error {
	// Loading the server repairs the history of a stopped daemon. Hold the
	// socket from before that repair until every session has stopped and
	// every Claude process has exited.
	listener, err := listen(mcp.Socket)
	if err != nil {
		return err
	}
	defer listener.Close()
	st, err := store.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer st.Close()
	s, err := server.New(st, cfg, parkAfter, harnesses, mcp)
	if err != nil {
		return fmt.Errorf("load database: %w", err)
	}

	signals := make(chan os.Signal, 2)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	serving := make(chan error, 1)
	go func() { serving <- s.Serve(listener) }()
	select {
	case err = <-serving:
	case <-signals:
	}
	stopped := make(chan struct{})
	go func() {
		s.Close()
		claude.WaitAll()
		close(stopped)
	}()
	select {
	case <-signals:
		claude.KillAll()
		os.Exit(1)
	case <-stopped:
	}
	return err
}

// findForwarder returns the ox-mcp executable beside oxd's own. Without it,
// Claude would run with its ox server failed.
func findForwarder() (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	if self, err = filepath.EvalSymlinks(self); err != nil {
		return "", err
	}
	return exec.LookPath(filepath.Join(filepath.Dir(self), "ox-mcp"))
}

// checkConfig refuses a configuration that offers a harness oxd does not
// have, or a choice its harness does not take: a Claude effort level Claude
// does not take, Claude's alias for manual, an Ur effort level or permission
// mode Ur does not take, or a provider order for a harness other than Ur.
func checkConfig(cfg *config.Config, harnesses map[string]agent.Harness) error {
	for name, harness := range cfg.Harnesses {
		if harnesses[name].Start == nil {
			return fmt.Errorf("unknown harness %q", name)
		}
		checkEffort := func(string) error { return nil }
		switch name {
		case "claude":
			if harness.CheckPermissionMode("default") == nil {
				return errors.New("use permission mode \"manual\" instead of \"default\" for Claude")
			}
			checkEffort = claude.CheckEffort
		case "ur":
			for _, mode := range harness.PermissionModes {
				if err := ur.CheckPermissionMode(mode.ID); err != nil {
					return err
				}
			}
			checkEffort = ur.CheckEffort
		}
		for _, model := range harness.Models {
			if len(model.Providers) > 0 && name != "ur" {
				return fmt.Errorf("model %q: providers is only for ur", model.ID)
			}
			for _, effort := range model.Efforts {
				if err := checkEffort(effort.ID); err != nil {
					return fmt.Errorf("model %q: %w", model.ID, err)
				}
			}
		}
	}
	return nil
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
// report or a closed terminal does, and is replaced. Any other file at path
// is left alone: Linux also refuses connections to a regular file.
func listen(path string) (net.Listener, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	// Serialize the check and replacement among starters. The directory
	// survives replacement of the socket and needs no lock file cleanup.
	parent, err := os.Open(dir)
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	if err := syscall.Flock(int(parent.Fd()), syscall.LOCK_EX); err != nil {
		return nil, fmt.Errorf("lock socket directory: %w", err)
	}
	listener, err := net.Listen("unix", path)
	if !errors.Is(err, syscall.EADDRINUSE) {
		return listener, err
	}
	if info, statErr := os.Lstat(path); statErr != nil || info.Mode().Type() != fs.ModeSocket {
		return nil, fmt.Errorf("%s exists and is not a socket", path)
	}
	conn, dialErr := net.Dial("unix", path)
	if dialErr == nil {
		conn.Close()
		return nil, fmt.Errorf("another oxd is serving %s", path)
	}
	if !errors.Is(dialErr, syscall.ECONNREFUSED) {
		return nil, dialErr
	}
	if err := os.Remove(path); err != nil {
		return nil, err
	}
	return net.Listen("unix", path)
}
