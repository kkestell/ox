// Command ox is the Ox terminal client. Its run, acp, and auth commands run
// the same commands of ox-server, which is installed next to ox.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"ox/internal/client"
	"ox/internal/control"
	"ox/internal/settings"
	"ox/internal/tui"
)

const version = "0.1.0"

const usage = `Usage:
  ox [--dir DIR] [--server SERVER]  Start the terminal client.
  ox run ...                        Run one prompt and print the final answer.
  ox acp ...                        Serve the Ox server over stdin and stdout.
  ox auth ...                       Sign in to or out of OpenRouter.
  ox --version                      Print the version.`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "ox: %s\n", control.Escape(err.Error()))
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) > 0 && slices.Contains([]string{"run", "acp", "auth"}, args[0]) {
		server, err := serverBinary()
		if err != nil {
			return err
		}
		err = syscall.Exec(server, append([]string{server}, args...), os.Environ())
		return fmt.Errorf("running %s: %w", server, err)
	}
	options, err := parseOptions(args)
	if errors.Is(err, flag.ErrHelp) {
		fmt.Println(usage)
		return nil
	}
	if err != nil {
		return fmt.Errorf("%w\n%s", err, usage)
	}
	if options.version {
		fmt.Println("ox", version)
		return nil
	}
	directory, err := workspace(options.dir)
	if err != nil {
		return err
	}
	home, err := settings.Home()
	if err != nil {
		return err
	}
	path := settings.GlobalPath(home)
	config, err := settings.ReadClient(path)
	if err != nil {
		return err
	}
	if len(config.Servers) == 0 {
		bundled, err := serverBinary()
		if err != nil {
			return err
		}
		config.Servers = []settings.Server{{Name: "Ox", Command: bundled, Args: []string{"acp"}}}
	}
	server, err := selectServer(config.Servers, options.server)
	if err != nil {
		return err
	}
	conn, created, err := client.Start(server, directory, version)
	if err != nil {
		return err
	}
	defer conn.Close()
	return tui.Run(conn, client.NewSession(conn, created), config.Favorites, path)
}

type options struct {
	dir, server string
	version     bool
}

func parseOptions(args []string) (options, error) {
	var o options
	flags := flag.NewFlagSet("ox", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&o.dir, "dir", ".", "workspace directory")
	flags.StringVar(&o.server, "server", "", "server name")
	flags.BoolVar(&o.version, "version", false, "print the version")
	flags.BoolVar(&o.version, "V", false, "print the version")
	if err := flags.Parse(args); err != nil {
		return o, err
	}
	if flags.NArg() > 0 {
		return o, fmt.Errorf("unexpected argument %s", flags.Arg(0))
	}
	return o, nil
}

// workspace returns the directory at path with symbolic links resolved.
func workspace(path string) (string, error) {
	directory, err := filepath.Abs(path)
	if err == nil {
		directory, err = filepath.EvalSymlinks(directory)
	}
	if err != nil {
		return "", fmt.Errorf("opening %s: %w", path, err)
	}
	if info, err := os.Stat(directory); err != nil || !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", directory)
	}
	return directory, nil
}

// selectServer returns the server with the name, or the only server when name
// is empty.
func selectServer(servers []settings.Server, name string) (settings.Server, error) {
	var names []string
	for _, server := range servers {
		if server.Name == name {
			return server, nil
		}
		names = append(names, server.Name)
	}
	available := strings.Join(names, ", ")
	if available == "" {
		available = "none"
	}
	switch {
	case name != "":
		return settings.Server{}, fmt.Errorf("no server named %s; available: %s", name, available)
	case len(servers) == 1:
		return servers[0], nil
	}
	return settings.Server{}, fmt.Errorf("choose a server with --server; available: %s", available)
}

// serverBinary returns the bundled ox-server, installed next to ox.
func serverBinary() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(executable), "ox-server"), nil
}
