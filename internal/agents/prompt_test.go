package agents

import (
	"bytes"
	"errors"
	"testing"
)

func TestPromptReleasedAfterClaudeTakesIt(t *testing.T) {
	var updates []Update
	p := &process{stdin: new(bytes.Buffer), phase: available, claudeState: "idle"}
	id, err := p.submit("client", "hello", func(update Update) { updates = append(updates, update) })
	if err != nil {
		t.Fatal(err)
	}

	p.sessionState("idle")
	if _, err := p.submit("client", "again", nil); !errors.Is(err, ErrBusy) || len(updates) != 0 {
		t.Fatalf("idle before Claude took the prompt released it: %v, %+v", err, updates)
	}

	p.commandStarted(id)
	p.sessionState("running")
	p.sessionState("idle")
	if len(updates) != 2 || updates[0].State != "running" || updates[1].State != "idle" || updates[1].MessageID != id {
		t.Fatalf("unexpected prompt updates: %+v", updates)
	}
	if !p.idle() || p.prompt != nil {
		t.Fatal("idle after Claude took the prompt did not release it")
	}
}
