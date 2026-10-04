package shellproc

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCaptureKeepsTheStartAndLatestEnd(t *testing.T) {
	capture := NewCapture(8)
	for _, chunk := range []string{"ab", "cdef", "ghij", "kl"} {
		capture.Write([]byte(chunk))
	}
	if head, tail := capture.Text(); head != "abcd" || tail != "ijkl" || !capture.Omitted || capture.Total != 12 {
		t.Errorf("head %q tail %q omitted %v total %d", head, tail, capture.Omitted, capture.Total)
	}
	small := NewCapture(8)
	small.Write([]byte("abc\xff"))
	if head, tail := small.Text(); head != "abc�" || tail != "" || small.Omitted {
		t.Errorf("small: %q %q", head, tail)
	}
	oneRead := NewCapture(4)
	oneRead.Write([]byte("abcdefgh"))
	if head, tail := oneRead.Text(); head != "ab" || tail != "gh" {
		t.Errorf("a limit smaller than one read: %q %q", head, tail)
	}
}

func shell(dir, command string) *exec.Cmd {
	cmd := exec.Command("/bin/sh", "-c", command)
	cmd.Dir = dir
	return cmd
}

func waitFor(t *testing.T, condition func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal(what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func stdout(p *Process) string {
	head, tail := p.Output().Stdout.Text()
	return head + tail
}

// gone reports whether the process whose PID is in file no longer runs.
func gone(file string) bool {
	pid, _ := os.ReadFile(file)
	output, _ := exec.Command("ps", "-o", "stat=", "-p", strings.TrimSpace(string(pid))).Output()
	state := strings.TrimSpace(string(output))
	return state == "" || strings.HasPrefix(state, "Z")
}

func TestStopSignalsTheGroupAndShutdownKillsAtOnce(t *testing.T) {
	dir := t.TempDir()
	processes := &Processes{}
	ignoring, err := processes.Start(shell(dir, "trap '' TERM; sleep 60 & echo $! > child; printf up; wait"), "ignoring", 1024)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return stdout(ignoring) == "up" }, "never started")
	stopped := make(chan Output)
	began := time.Now()
	go func() { stopped <- ignoring.Stop() }()
	time.Sleep(200 * time.Millisecond)
	processes.Shutdown()
	output := <-stopped
	if time.Since(began) > stopGrace || !output.State.Stopped || output.State.Status == nil {
		t.Errorf("shutdown during the grace period: %+v after %v", output.State, time.Since(began))
	}
	waitFor(t, func() bool { return gone(filepath.Join(dir, "child")) }, "the child survived shutdown")
	if _, err := processes.Start(shell(dir, "true"), "true", 1024); err == nil {
		t.Error("a process started after shutdown")
	}
}

func TestTheLimitRemovesTheOldestFinishedProcessAndNeverARunningOne(t *testing.T) {
	dir := t.TempDir()
	processes := &Processes{}
	defer processes.Shutdown()
	var started []*Process
	for i := range maxProcesses {
		command := "sleep 60"
		if i == 1 {
			command = "true"
		}
		process, err := processes.Start(shell(dir, command), fmt.Sprint(i), 1024)
		if err != nil {
			t.Fatal(err)
		}
		started = append(started, process)
	}
	started[1].Wait(context.Background(), 5*time.Second)
	if _, err := processes.Start(shell(dir, "sleep 60"), "new", 1024); err != nil {
		t.Fatal(err)
	}
	if processes.Get(started[1].ID) != nil || processes.Get(started[0].ID) == nil {
		t.Error("the finished process was not the one removed")
	}
	if _, err := processes.Start(shell(dir, "true"), "over", 1024); err == nil || !strings.Contains(err.Error(), "16 shell processes are running") {
		t.Errorf("over the limit: %v", err)
	}
}

func TestWritesReportTimeoutsCancellationAndClosedStdin(t *testing.T) {
	dir := t.TempDir()
	processes := &Processes{}
	defer processes.Shutdown()
	// The command never reads, so a large write fills the pipe.
	stuck, _ := processes.Start(shell(dir, "sleep 60"), "stuck", 1024)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	written := stuck.Write(ctx, make([]byte, 1<<20), false)
	if written.Interruption != WriteCancelled || written.StdinClosed || written.Bytes == 0 || written.Bytes == 1<<20 {
		t.Errorf("cancelled write = %+v", written)
	}
	cat, _ := processes.Start(shell(dir, "cat"), "cat", 1024)
	if written := cat.Write(context.Background(), nil, true); written.Interruption != NotInterrupted || !written.StdinClosed {
		t.Errorf("closing = %+v", written)
	}
	if written := cat.Write(context.Background(), []byte("x"), false); written.Interruption != StdinClosed && written.Interruption != Finished {
		t.Errorf("after closing = %+v", written)
	}
}
