package server

import (
	"testing"
	"time"
)

func TestShutdownWaitsForClosingSessions(t *testing.T) {
	ops := newOperations()
	wait, release, ok := ops.beginClose("session")
	if !ok {
		t.Fatal("could not begin close")
	}
	wait()
	ops.beginShutdown()
	done := make(chan struct{})
	go func() {
		ops.shutdown()
		close(done)
	}()
	select {
	case <-done:
		t.Error("shutdown returned before the close finished")
	case <-time.After(50 * time.Millisecond):
	}
	release()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not return after the close finished")
	}
}
