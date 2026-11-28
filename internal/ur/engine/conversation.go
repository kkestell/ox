package engine

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// conversation is the file <dir>/<token>.jsonl, one message per line, whose
// token is the resume token, and its entries.
type conversation struct {
	dir     string
	token   string
	entries []message // exactly the file's entries; only append adds to them
}

// openConversation reads the conversation token names in dir. An empty token
// starts a new conversation, whose file is written with its first entry.
func openConversation(dir, token string) (*conversation, error) {
	if token == "" {
		return &conversation{dir: dir, token: rand.Text()}, nil
	}
	c := &conversation{dir: dir, token: token}
	f, err := os.Open(c.path())
	if err != nil {
		return nil, err
	}
	defer f.Close()
	decoder := json.NewDecoder(f)
	for {
		var m message
		err := decoder.Decode(&m)
		if errors.Is(err, io.EOF) {
			return c, nil
		}
		if err != nil {
			return nil, err
		}
		c.entries = append(c.entries, m)
	}
}

func (c *conversation) path() string {
	return filepath.Join(c.dir, c.token+".jsonl")
}

// append writes m to the file, then adds it to entries.
func (c *conversation) append(m message) error {
	if err := os.MkdirAll(c.dir, 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(c.path(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	data, _ := json.Marshal(m) // strings always marshal
	_, err = f.Write(append(data, '\n'))
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	c.entries = append(c.entries, m)
	return nil
}

// stopped answers a call whose command did not finish: an interrupt, a stop,
// or oxd exiting during it.
const stopped = "The user stopped this command before it finished."

// messages is the conversation as a request sends it. Each run of assistant
// entries, the deltas, reasoning, and tool calls of one response, joins into
// one message, and each call left without a tool entry before the next user
// entry is answered with stopped. Reasoning models need the current turn's
// reasoning back in its tool loop, so the reasoning of the entries from
// turnStart on stays, and earlier turns' reasoning is left out. The messages'
// reasoning details and tool calls are fresh slices, so the merge never writes
// into entries.
func (c *conversation) messages(turnStart int) []message {
	var merged []message
	var open []string // IDs of the latest response's calls without a tool entry
	for i, m := range c.entries {
		var reasoning []reasoningDetail
		if i >= turnStart {
			reasoning = m.ReasoningDetails
		}
		m.ReasoningDetails = appendReasoning(nil, reasoning)
		m.ToolCalls = slices.Clone(m.ToolCalls)
		last := len(merged) - 1
		switch {
		case m.Role == "assistant" && last >= 0 && merged[last].Role == "assistant":
			merged[last].Content.Text += m.Content.Text
			merged[last].ReasoningDetails = appendReasoning(merged[last].ReasoningDetails, m.ReasoningDetails)
			merged[last].ToolCalls = append(merged[last].ToolCalls, m.ToolCalls...)
		case m.Role == "tool":
			open = slices.DeleteFunc(open, func(id string) bool { return id == m.ToolCallID })
			merged = append(merged, m)
		default:
			if m.Role == "user" {
				for _, id := range open {
					merged = append(merged, message{Role: "tool", ToolCallID: id, Content: content{Text: stopped}})
				}
				open = nil
			}
			merged = append(merged, m)
		}
		for _, call := range m.ToolCalls {
			open = append(open, call.ID)
		}
	}
	return merged
}

// reply is the text of the trailing run of assistant entries, the last
// response, or empty when the conversation does not end with one.
func (c *conversation) reply() string {
	start := len(c.entries)
	for start > 0 && c.entries[start-1].Role == "assistant" {
		start--
	}
	var text strings.Builder
	for _, m := range c.entries[start:] {
		text.WriteString(m.Content.Text)
	}
	return text.String()
}
