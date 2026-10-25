// Package provider holds what the agent needs from a model provider: the model
// request, the streamed completion, and how a request failed.
package provider

import (
	"context"
	"encoding/json"
	"errors"

	"ox/internal/catalog"
	"ox/internal/transcript"
)

// Provider fetches a model catalog and streams completions from it.
type Provider interface {
	FetchCatalog(ctx context.Context) (catalog.Catalog, error)
	Stream(ctx context.Context, r Request) (Stream, error)
}

// Stream is one streamed response. Closing it stops reading and discards text
// received before a complete message was validated.
type Stream interface {
	// Next returns the next delta or the one validated completion. A stream
	// that ends before its completion is an error.
	Next() (Item, error)
	Close()
}

// Request is one model request: the model, effort, system prompt, tool
// definitions in chat-completions form, and the saved transcript, whose
// request entries are sent. SessionID names the session the request belongs
// to.
type Request struct {
	Model        *catalog.Model
	Effort       catalog.Effort
	SystemPrompt string
	Tools        []json.RawMessage
	Transcript   []transcript.Entry
	SessionID    string
}

// Stop is why a completion ended.
type Stop int

const (
	StopFinished Stop = iota
	StopToolCalls
	StopTokenLimit
	StopRefused
)

// Completion is one validated assistant message and why it ended.
type Completion struct {
	Message transcript.AssistantMessage
	Stop    Stop
}

// Item is one stream item: a text delta, a reasoning delta, or the completion.
type Item struct {
	Text       string
	Reasoning  string
	Completion *Completion
}

// ErrContextOverflow means the provider rejected the input as too large for the
// model context. Every later request in the session fails the same way.
var ErrContextOverflow = errors.New("the model provider rejected the input as too large for the model context")

// TemporaryError is a request failure a later attempt may not repeat, such as
// a stall or an overloaded service.
type TemporaryError struct{ Message string }

func (e *TemporaryError) Error() string { return e.Message }

// IsTemporary reports whether retrying the request may succeed.
func IsTemporary(err error) bool {
	var temporary *TemporaryError
	return errors.As(err, &temporary)
}
