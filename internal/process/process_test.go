package process

import (
	"testing"
	"time"

	"github.com/kkestell/ox/internal/agent"
)

// Once the agent has ended, delivery discards the process's output, so the
// reader and delivery end with the process.
func TestDeliveryEndsWithTheAgent(t *testing.T) {
	captured, events, done := make(chan agent.Event), make(chan agent.Event), make(chan struct{})
	returned := make(chan struct{})
	go func() {
		deliverOutput(captured, events, done)
		close(returned)
	}()
	captured <- "a line"
	close(done)
	captured <- Exit{}
	close(captured)
	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("delivery waited for an agent that has ended")
	}
}
