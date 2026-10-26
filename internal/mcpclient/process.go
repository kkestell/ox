package mcpclient

import (
	"context"
	"errors"
	"maps"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"ox/internal/settings"
)

func command(workspace string, server settings.MCPServer) *exec.Cmd {
	cmd := exec.Command(server.Command, server.Args...)
	cmd.Dir, cmd.Stderr = workspace, os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if key == "OPENROUTER_API_KEY" || key == "OX_SETTINGS" {
			continue
		}
		if _, overridden := server.Env[key]; !overridden {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	for _, key := range slices.Sorted(maps.Keys(server.Env)) {
		cmd.Env = append(cmd.Env, key+"="+server.Env[key])
	}
	return cmd
}

// groupTransport leaves protocol traffic and the single Wait to the SDK, but
// also terminates descendants that keep stdout open or outlive their parent.
type groupTransport struct{ cmd *exec.Cmd }

func (t *groupTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	conn, err := (&mcp.CommandTransport{Command: t.cmd, TerminateDuration: 2 * time.Second}).Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &groupConnection{Connection: conn, group: t.cmd.Process.Pid}, nil
}

type groupConnection struct {
	mcp.Connection
	group int
	once  sync.Once
	err   error
}

func (c *groupConnection) Write(ctx context.Context, message jsonrpc.Message) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- c.Connection.Write(ctx, message) }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		// A partial JSON frame cannot be resumed safely. Closing also unblocks
		// the SDK's pipe write, which does not observe context cancellation.
		_ = c.Close()
		<-done
		return ctx.Err()
	}
}

func (c *groupConnection) Close() error {
	c.once.Do(func() {
		done := make(chan error, 1)
		go func() { done <- c.Connection.Close() }()
		_ = syscall.Kill(-c.group, syscall.SIGTERM)
		deadline := time.NewTimer(2 * time.Second)
		defer deadline.Stop()
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for !errors.Is(syscall.Kill(-c.group, 0), syscall.ESRCH) {
			select {
			case <-deadline.C:
				_ = syscall.Kill(-c.group, syscall.SIGKILL)
				c.err = <-done
				return
			case <-ticker.C:
			}
		}
		c.err = <-done
	})
	return c.err
}
