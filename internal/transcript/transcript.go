// Package transcript defines a session transcript: the turn starts, assistant
// batches, compactions, and turn errors that make up a conversation, and their
// stored JSON.
package transcript

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"ox/internal/catalog"
)

// Mode is whether shell calls and input sent to shell processes require
// approval from the ACP client.
type Mode string

const (
	ModeAsk  Mode = "ask"
	ModeAuto Mode = "auto"
)

// Modes lists every session mode.
var Modes = []Mode{ModeAsk, ModeAuto}

// ParseMode returns the session mode with the given ID.
func ParseMode(id string) (Mode, bool) {
	for _, mode := range Modes {
		if string(mode) == id {
			return mode, true
		}
	}
	return "", false
}

// Name is the mode's display name.
func (m Mode) Name() string {
	if m == ModeAuto {
		return "Auto"
	}
	return "Ask"
}

// Description explains the mode to the user.
func (m Mode) Description() string {
	if m == ModeAuto {
		return "Run shell commands and send them input without asking."
	}
	return "Ask before running each shell command or sending input to one."
}

// Entry is one transcript entry: a *TurnStart, an *AssistantBatch, a
// *Compaction, or a TurnError. Every nonempty transcript opens with a turn
// start.
type Entry interface{ entry() }

// TurnStart is the input that starts a turn, saved with the model and effort
// level captured for that turn and the session mode when the turn started.
type TurnStart struct {
	Model  string         `json:"model"`
	Effort catalog.Effort `json:"effort"`
	Mode   Mode           `json:"mode"`
	Input  TurnInput      `json:"input"`
}

// TurnError is why a turn ended with an error. It ends the turn's entries.
type TurnError string

// Compaction is a summary that replaces the entries before it in model
// requests, except the latest turn start. The entries it replaces stay saved
// and are still replayed.
type Compaction struct {
	Summary string `json:"summary"`
	// Usage is what the provider reported for the request that produced the
	// summary, or nil when it reported none.
	Usage *Usage `json:"usage"`
}

func (*TurnStart) entry()      {}
func (*AssistantBatch) entry() {}
func (*Compaction) entry()     {}
func (TurnError) entry()       {}

// Validate rejects an empty summary.
func (c *Compaction) Validate() error {
	if strings.TrimSpace(c.Summary) == "" {
		return errors.New("compaction summary is empty")
	}
	return nil
}

// Message is the user message that gives the model the summary.
func (c *Compaction) Message() UserMessage {
	return TextMessage("The earlier part of this session was replaced by this summary:\n\n" + c.Summary)
}

// Validate rejects a turn start whose model, effort, or mode is not one Ox
// knows.
func (t *TurnStart) Validate() error {
	if _, ok := catalog.Split(t.Model); !ok {
		return errors.New("turn start model is not a qualified model ID with a known provider")
	}
	if _, ok := catalog.ParseEffort(string(t.Effort)); !ok {
		return fmt.Errorf("unknown effort %q", t.Effort)
	}
	if _, ok := ParseMode(string(t.Mode)); !ok {
		return fmt.Errorf("unknown mode %q", t.Mode)
	}
	if (t.Input.Message == nil) == (t.Input.Skill == nil) {
		return errors.New("turn input must be one user message or skill invocation")
	}
	return nil
}

// TurnInput is the user message or skill invocation that starts a turn.
// Exactly one is set.
type TurnInput struct {
	Message *UserMessage
	Skill   *SkillInvocation
}

// HasImages reports whether the input carries an image.
func (in TurnInput) HasImages() bool {
	if in.Skill != nil {
		return len(in.Skill.Images) > 0
	}
	return in.Message.HasImages()
}

// ModelMessage is the user message the model reads for this input.
func (in TurnInput) ModelMessage() UserMessage {
	if in.Skill != nil {
		return in.Skill.Message()
	}
	return *in.Message
}

func (in TurnInput) MarshalJSON() ([]byte, error) {
	if in.Skill != nil {
		return json.Marshal(tagged{"skill_invocation", in.Skill})
	}
	return json.Marshal(tagged{"user_message", in.Message})
}

func (in *TurnInput) UnmarshalJSON(data []byte) error {
	var raw rawTagged
	if err := strictUnmarshal(data, &raw); err != nil {
		return err
	}
	switch raw.Type {
	case "user_message":
		in.Message = new(UserMessage)
		return strictUnmarshal(raw.Content, in.Message)
	case "skill_invocation":
		in.Skill = new(SkillInvocation)
		return strictUnmarshal(raw.Content, in.Skill)
	}
	return fmt.Errorf("unknown turn input %q", raw.Type)
}

// UserMessage is the text and images of one user prompt, in order.
type UserMessage struct {
	Parts []UserMessagePart `json:"parts"`
}

// TextMessage returns a user message of one text part.
func TextMessage(text string) UserMessage {
	return UserMessage{Parts: []UserMessagePart{{Text: text}}}
}

// Text joins the message's text parts with newlines.
func (m UserMessage) Text() string {
	var texts []string
	for _, part := range m.Parts {
		if part.Image == nil {
			texts = append(texts, part.Text)
		}
	}
	return strings.Join(texts, "\n")
}

// HasImages reports whether the message has an image part.
func (m UserMessage) HasImages() bool {
	for _, part := range m.Parts {
		if part.Image != nil {
			return true
		}
	}
	return false
}

// UserMessagePart is a text part, or an image part when Image is set.
type UserMessagePart struct {
	Text  string
	Image *ImageAttachment
}

func (p UserMessagePart) MarshalJSON() ([]byte, error) {
	if p.Image != nil {
		return json.Marshal(tagged{"image", p.Image})
	}
	return json.Marshal(tagged{"text", p.Text})
}

func (p *UserMessagePart) UnmarshalJSON(data []byte) error {
	var raw rawTagged
	if err := strictUnmarshal(data, &raw); err != nil {
		return err
	}
	switch raw.Type {
	case "text":
		return strictUnmarshal(raw.Content, &p.Text)
	case "image":
		p.Image = new(ImageAttachment)
		return strictUnmarshal(raw.Content, p.Image)
	}
	return fmt.Errorf("unknown user message part %q", raw.Type)
}

// ImageAttachment is a base64-encoded image.
type ImageAttachment struct {
	Data     string `json:"data"`
	MimeType string `json:"mime_type"`
}

// SkillInvocation is a skill invoked as a slash command, saved in place of the
// user message for its turn. The instructions are copied from the skill when
// it is invoked.
type SkillInvocation struct {
	Name         string            `json:"name"`
	Arguments    string            `json:"arguments"`
	Instructions string            `json:"instructions"`
	Images       []ImageAttachment `json:"images"`
}

// CommandText is the slash command as typed, used for the session title and
// replay.
func (s *SkillInvocation) CommandText() string {
	if s.Arguments == "" {
		return "/" + s.Name
	}
	return "/" + s.Name + " " + s.Arguments
}

// Message is the user message that gives the model the invocation: its text
// followed by its images. Skills written for Claude Code mark where arguments
// go with `$ARGUMENTS`; instructions without it get the arguments appended.
func (s *SkillInvocation) Message() UserMessage {
	var text string
	if strings.Contains(s.Instructions, "$ARGUMENTS") {
		text = fmt.Sprintf("Skill /%s invoked.\n\nInstructions:\n%s", s.Name, strings.ReplaceAll(s.Instructions, "$ARGUMENTS", s.Arguments))
	} else {
		text = fmt.Sprintf("Skill /%s invoked.\n\nInstructions:\n%s\n\nArguments:\n%s", s.Name, s.Instructions, s.Arguments)
	}
	message := TextMessage(text)
	for i := range s.Images {
		message.Parts = append(message.Parts, UserMessagePart{Image: &s.Images[i]})
	}
	return message
}

// AssistantMessage is the content of one validated model completion.
type AssistantMessage struct {
	Text      string     `json:"text"`
	Reasoning string     `json:"reasoning"`
	ToolCalls []ToolCall `json:"tool_calls"`
	// ContinuationMetadata is opaque provider reasoning state sent back with
	// the next request.
	ContinuationMetadata []json.RawMessage `json:"continuation_metadata"`
	// Usage is what the provider reported for the request that produced this
	// message, or nil when it reported none.
	Usage *Usage `json:"usage"`
}

// Validate requires nonempty, unique call IDs and nonempty tool names.
func (m *AssistantMessage) Validate() error {
	seen := map[string]bool{}
	for i, call := range m.ToolCalls {
		switch {
		case call.CallID == "":
			return fmt.Errorf("tool call %d has an empty call ID", i)
		case call.Name == "":
			return fmt.Errorf("tool call %s has an empty tool name", call.CallID)
		case seen[call.CallID]:
			return fmt.Errorf("tool call ID %s is repeated within one assistant message", call.CallID)
		}
		seen[call.CallID] = true
	}
	return nil
}

// Usage is the token counts and cost reported for one model request. Cached
// tokens are part of the input tokens and reasoning tokens part of the output
// tokens. Cost is in OpenRouter credits, whose base currency is the US dollar.
type Usage struct {
	InputTokens     uint64   `json:"input_tokens"`
	CachedTokens    uint64   `json:"cached_tokens"`
	OutputTokens    uint64   `json:"output_tokens"`
	ReasoningTokens uint64   `json:"reasoning_tokens"`
	Cost            *float64 `json:"cost"`
}

// ToolCall is one call the model made. Arguments is the complete string the
// model produced, kept verbatim so invalid JSON still gets an ordinary failed
// result.
type ToolCall struct {
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// ToolStatus is how a tool call ended.
type ToolStatus string

const (
	ToolCompleted ToolStatus = "completed"
	ToolFailed    ToolStatus = "failed"
	ToolCancelled ToolStatus = "cancelled"
)

// ToolOutcome is what happened to a tool call: Text is what the model reads,
// and Content is what the ACP client shows. Empty content means the client
// shows the text.
type ToolOutcome struct {
	Status  ToolStatus    `json:"status"`
	Text    string        `json:"text"`
	Content []ToolContent `json:"content"`
}

// Completed returns a completed outcome with the given text.
func Completed(text string) ToolOutcome { return ToolOutcome{Status: ToolCompleted, Text: text} }

// Failed returns a failed outcome with the given text.
func Failed(text string) ToolOutcome { return ToolOutcome{Status: ToolFailed, Text: text} }

// Cancelled returns a cancelled outcome with the given text.
func Cancelled(text string) ToolOutcome { return ToolOutcome{Status: ToolCancelled, Text: text} }

// ToolContent is one block the client shows: text, or a diff when Diff is set.
type ToolContent struct {
	Text string
	Diff *Diff
}

// Diff is a file change. Path is absolute, and OldText is nil for a new file.
type Diff struct {
	Path    string  `json:"path"`
	OldText *string `json:"old_text"`
	NewText string  `json:"new_text"`
}

func (c ToolContent) MarshalJSON() ([]byte, error) {
	if c.Diff != nil {
		return json.Marshal(tagged{"diff", c.Diff})
	}
	return json.Marshal(tagged{"text", c.Text})
}

func (c *ToolContent) UnmarshalJSON(data []byte) error {
	var raw rawTagged
	if err := strictUnmarshal(data, &raw); err != nil {
		return err
	}
	switch raw.Type {
	case "text":
		return strictUnmarshal(raw.Content, &c.Text)
	case "diff":
		c.Diff = new(Diff)
		return strictUnmarshal(raw.Content, c.Diff)
	}
	return fmt.Errorf("unknown tool content %q", raw.Type)
}

// AssistantBatch is one validated assistant message with a final outcome for
// each tool call. Outcome i belongs to tool call i.
type AssistantBatch struct {
	Message  AssistantMessage `json:"message"`
	Outcomes []ToolOutcome    `json:"outcomes"`
}

// Validate checks the message and requires one outcome per call.
func (b *AssistantBatch) Validate() error {
	if err := b.Message.Validate(); err != nil {
		return err
	}
	if calls, outcomes := len(b.Message.ToolCalls), len(b.Outcomes); calls != outcomes {
		return fmt.Errorf("an assistant message with %d tool calls has %d tool outcomes", calls, outcomes)
	}
	return nil
}

// Validate checks a whole transcript.
func Validate(entries []Entry) error {
	if len(entries) == 0 {
		return nil
	}
	if _, ok := entries[0].(*TurnStart); !ok {
		return errors.New("transcript does not open with a turn start")
	}
	for _, entry := range entries {
		var err error
		switch entry := entry.(type) {
		case *TurnStart:
			err = entry.Validate()
		case *AssistantBatch:
			err = entry.Validate()
		case *Compaction:
			err = entry.Validate()
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// LatestTurnStart returns the last turn start, or nil.
func LatestTurnStart(entries []Entry) *TurnStart {
	for i := len(entries) - 1; i >= 0; i-- {
		if start, ok := entries[i].(*TurnStart); ok {
			return start
		}
	}
	return nil
}

// RequestEntries returns the entries model requests read: every entry, or,
// after a compaction, the compaction, the latest turn start before it, and the
// entries after it. The turn start is kept so the model reads the user's latest
// message word for word instead of through the summary.
func RequestEntries(entries []Entry) []Entry {
	for i := len(entries) - 1; i >= 0; i-- {
		if _, ok := entries[i].(*Compaction); !ok {
			continue
		}
		if start := LatestTurnStart(entries[:i]); start != nil {
			return slices.Concat(entries[i:i+1], []Entry{start}, entries[i+1:])
		}
		return entries[i:]
	}
	return entries
}

// UsageSummary returns the context tokens of the latest assistant message (0
// when it reported no usage or a compaction followed it) and the sum of every
// reported cost, compactions included (nil when none reported one). ok is
// false before the first assistant message.
func UsageSummary(entries []Entry) (used uint64, cost *float64, ok bool) {
	var total float64
	for _, entry := range entries {
		var usage *Usage
		switch entry := entry.(type) {
		case *AssistantBatch:
			usage, used, ok = entry.Message.Usage, 0, true
			if usage != nil {
				used = usage.InputTokens + usage.OutputTokens
			}
		case *Compaction:
			// The summary request's tokens describe the replaced context, and
			// the next response reports the new one.
			usage, used = entry.Usage, 0
		}
		if usage != nil && usage.Cost != nil {
			total += *usage.Cost
			cost = &total
		}
	}
	return used, cost, ok
}

// Encode returns an entry's stored kind and JSON.
func Encode(entry Entry) (string, string, error) {
	var kind string
	switch entry.(type) {
	case *TurnStart:
		kind = "turn_start"
	case *AssistantBatch:
		kind = "assistant_batch"
	case *Compaction:
		kind = "compaction"
	case TurnError:
		kind = "turn_error"
	}
	data, err := json.Marshal(entry)
	return kind, string(data), err
}

// Decode parses one stored entry, rejecting unknown fields.
func Decode(kind, data string) (Entry, error) {
	var entry Entry
	switch kind {
	case "turn_start":
		entry = new(TurnStart)
	case "assistant_batch":
		entry = new(AssistantBatch)
	case "compaction":
		entry = new(Compaction)
	case "turn_error":
		var text TurnError
		if err := strictUnmarshal([]byte(data), &text); err != nil {
			return nil, fmt.Errorf("%s entry: %w", kind, err)
		}
		return text, nil
	default:
		return nil, fmt.Errorf("unknown transcript entry kind %q", kind)
	}
	if err := strictUnmarshal([]byte(data), entry); err != nil {
		return nil, fmt.Errorf("%s entry: %w", kind, err)
	}
	return entry, nil
}

type tagged struct {
	Type    string `json:"type"`
	Content any    `json:"content"`
}

type rawTagged struct {
	Type    string          `json:"type"`
	Content json.RawMessage `json:"content"`
}

func strictUnmarshal(data []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	return decoder.Decode(value)
}

// The stored JSON writes empty lists as [], never null, for the scripts that
// read the database.

func (m UserMessage) MarshalJSON() ([]byte, error) {
	type plain UserMessage
	return json.Marshal(plain{Parts: orEmpty(m.Parts)})
}

func (s SkillInvocation) MarshalJSON() ([]byte, error) {
	type plain SkillInvocation
	s.Images = orEmpty(s.Images)
	return json.Marshal(plain(s))
}

func (m AssistantMessage) MarshalJSON() ([]byte, error) {
	type plain AssistantMessage
	m.ToolCalls = orEmpty(m.ToolCalls)
	m.ContinuationMetadata = orEmpty(m.ContinuationMetadata)
	return json.Marshal(plain(m))
}

func (o ToolOutcome) MarshalJSON() ([]byte, error) {
	type plain ToolOutcome
	o.Content = orEmpty(o.Content)
	return json.Marshal(plain(o))
}

func (b AssistantBatch) MarshalJSON() ([]byte, error) {
	type plain AssistantBatch
	b.Outcomes = orEmpty(b.Outcomes)
	return json.Marshal(plain(b))
}

func orEmpty[T any](items []T) []T {
	if items == nil {
		return []T{}
	}
	return items
}
