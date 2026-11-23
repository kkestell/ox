// Package children is the set of processes oxd waits for before it exits and
// kills on a second stop signal.
package children

import (
	"errors"
	"os/exec"
	"sync"
)

var set struct {
	sync.Mutex
	live   map[*exec.Cmd]func() // each running process's kill
	killed bool
}

// exited is signalled each time a process is removed from the set.
var exited = sync.NewCond(&set.Mutex)

// Start starts cmd and adds it to the set, with kill, which ends it and
// whatever it started. It refuses once KillAll has run. The caller calls Done
// after cmd.Wait.
func Start(cmd *exec.Cmd, kill func()) error {
	set.Lock()
	defer set.Unlock()
	if set.killed {
		return errors.New("daemon is shutting down")
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	if set.live == nil {
		set.live = make(map[*exec.Cmd]func())
	}
	set.live[cmd] = kill
	return nil
}

// Done removes a process that has been reaped.
func Done(cmd *exec.Cmd) {
	set.Lock()
	delete(set.live, cmd)
	exited.Broadcast()
	set.Unlock()
}

// WaitAll returns once every process in the set has been reaped. The daemon
// calls it after its sessions have ended, which asked each process to end.
func WaitAll() {
	set.Lock()
	defer set.Unlock()
	for len(set.live) != 0 {
		exited.Wait()
	}
}

// KillAll kills every process in the set and prevents new ones from starting.
// The daemon calls it before exiting on a second stop signal.
func KillAll() {
	set.Lock()
	defer set.Unlock()
	set.killed = true
	for _, kill := range set.live {
		kill()
	}
}
