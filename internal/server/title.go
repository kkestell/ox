package server

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"
	"unicode"
)

const titleRunes = 48

// firstLine is the line of a prompt that labels its session, shortened.
func firstLine(text string) string {
	return shorten(leadingLine(text))
}

// leadingLine is text's first line once surrounding space is trimmed.
func leadingLine(text string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	return line
}

// shorten cuts line to the length of a child's title, ending it with an
// ellipsis when it is cut.
func shorten(line string) string {
	if _, long := firstRunes(line, titleRunes); !long {
		return line
	}
	first, _ := firstRunes(line, titleRunes-1)
	return strings.TrimRightFunc(first, unicode.IsSpace) + "…"
}

// cleanTitle is the title in a titling model's reply: its first line, without
// enclosing quotes, shortened, or empty when it has none.
func cleanTitle(reply string) string {
	title := leadingLine(reply)
	if len(title) >= 2 && strings.ContainsRune("\"'`", rune(title[0])) && title[len(title)-1] == title[0] {
		title = title[1 : len(title)-1]
	}
	return shorten(strings.TrimSpace(title))
}

// labelLocked is the session's label: a child's title, or else the title it
// was started with or its generated title, or else the first nonblank first line of the prompts the user sent
// it, or the start of its ID before it has one. The caller holds Server.mu.
func (sess *session) labelLocked() string {
	switch {
	case sess.child != nil:
		return sess.child.Title
	case sess.title != "":
		return sess.title
	case sess.firstLine != "":
		return sess.firstLine
	}
	return sess.id[:8]
}

// broadcastLabelLocked tells every client the session's label. The caller
// holds Server.mu.
func (sess *session) broadcastLabelLocked() {
	sess.server.broadcast("session/labeled", struct {
		SessionID string `json:"session_id"`
		Label     string `json:"label"`
	}{sess.id, sess.labelLocked()})
}

// labelFrom labels a top-level session with the first prompt the user sent it
// whose first line is not blank, tells every client, and starts titling it.
// Later prompts, and any prompt to a session that already has a title, leave
// the label as it is.
func (sess *session) labelFrom(prompt string) {
	line := firstLine(prompt)
	s := sess.server
	s.mu.Lock()
	if sess.firstLine != "" || sess.title != "" || line == "" {
		s.mu.Unlock()
		return
	}
	sess.firstLine = line
	sess.broadcastLabelLocked()
	s.mu.Unlock()
	go sess.generateTitle(prompt)
}

const (
	// titleInstructions is the system prompt the titling model writes a
	// title with.
	titleInstructions = "Write a title of at most 48 characters for a coding session that begins with the user's message. " +
		"Reply with the title alone, in plain text, without quotes or a trailing period."
	// titleInputRunes is how much of the first prompt the titling model reads.
	titleInputRunes = 4000
	// titleLimit is how long titling may take.
	titleLimit = time.Minute
)

// generateTitle asks the titling model for a title from prompt and hands it
// to the session. It runs on its own goroutine and touches no session state.
// It gives up when the session ends.
func (sess *session) generateTitle(prompt string) {
	prompt, _ = firstRunes(prompt, titleInputRunes)
	ctx, cancel := context.WithTimeout(sess.ctx, titleLimit)
	defer cancel()
	titling := sess.server.config.Titling
	reply, err := sess.server.ask(ctx, titling.Model, titling.Effort, titleInstructions, prompt)
	title := cleanTitle(reply)
	switch {
	case err != nil:
	case title == "":
		err = errors.New("empty reply")
	case !sess.post(func() { sess.setTitle(title) }):
		err = errors.New("session ended")
	}
	if err != nil {
		log.Printf("session %s: title: %v", sess.id, err)
	}
}

// setTitle labels the session with its generated title, kept even when
// storing it fails, and tells every client.
func (sess *session) setTitle(title string) {
	if err := sess.server.store.SetTitle(sess.id, title); err != nil {
		log.Printf("session %s: store title: %v", sess.id, err)
	}
	s := sess.server
	s.mu.Lock()
	defer s.mu.Unlock()
	sess.title = title
	sess.broadcastLabelLocked()
}
