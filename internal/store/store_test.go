package store

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"ox/internal/catalog"
	"ox/internal/transcript"
)

const workspace = "/Users/kyle/projects/ox"

func memory(t *testing.T) *Store {
	t.Helper()
	s, err := OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func create(t *testing.T, s *Store) string {
	t.Helper()
	summary, err := s.Create(workspace)
	if err != nil {
		t.Fatal(err)
	}
	return summary.ID
}

func turn(text string) *transcript.TurnStart {
	message := transcript.TextMessage(text)
	return &transcript.TurnStart{Model: "openrouter:deepseek/deepseek-v4.1-flash", Effort: catalog.EffortDefault,
		Mode: transcript.ModeAsk, Input: transcript.TurnInput{Message: &message}}
}

func calls(ids ...string) []transcript.ToolCall {
	var calls []transcript.ToolCall
	for _, id := range ids {
		calls = append(calls, transcript.ToolCall{CallID: id, Name: "shell", Arguments: `{"command":"true"}`})
	}
	return calls
}

func TestABatchSurvivesReopenInOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ox.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	id := create(t, s)
	old := "Cloudy\n"
	cost := 0.5
	batch := &transcript.AssistantBatch{
		Message: transcript.AssistantMessage{
			Reasoning: "Two cities.", ToolCalls: calls("call-1", "call-2"),
			ContinuationMetadata: []json.RawMessage{json.RawMessage(`{"data":"opaque","type":"reasoning.encrypted"}`)},
			Usage:                &transcript.Usage{InputTokens: 100, CachedTokens: 20, OutputTokens: 30, ReasoningTokens: 10, Cost: &cost},
		},
		Outcomes: []transcript.ToolOutcome{
			{Status: transcript.ToolCompleted, Text: "Sunny.", Content: []transcript.ToolContent{
				{Text: "Modified forecast.txt"},
				{Diff: &transcript.Diff{Path: workspace + "/forecast.txt", OldText: &old, NewText: "Sunny\n"}},
			}},
			transcript.Failed("Denver is unavailable."),
		},
	}
	skill := &transcript.TurnStart{Model: "openrouter:z-ai/glm-5.3-flash", Effort: catalog.EffortLow, Mode: transcript.ModeAuto,
		Input: transcript.TurnInput{Skill: &transcript.SkillInvocation{Name: "goal", Arguments: "Pass the tests.", Instructions: "Complete."}}}
	first := turn("Weather in Chicago and Denver?")
	compaction := &transcript.Compaction{Summary: "Chicago is sunny.", Usage: &transcript.Usage{InputTokens: 200, OutputTokens: 20, Cost: &cost}}
	for _, step := range []func() error{
		func() error { _, err := s.AppendTurnStart(id, first); return err },
		func() error { return s.AppendBatch(id, batch) },
		func() error { return s.AppendCompaction(id, compaction) },
		func() error { _, err := s.AppendTurnStart(id, skill); return err },
		func() error { return s.AppendTurnError(id, "the model request failed: OpenRouter returned 503") },
	} {
		if err := step(); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()

	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	session, err := s.Read(id)
	if err != nil {
		t.Fatal(err)
	}
	if session.Summary.Workspace != workspace || session.Summary.Title != "Weather in Chicago and Denver?" {
		t.Errorf("summary = %+v", session.Summary)
	}
	want := []transcript.Entry{first, batch, compaction, skill, transcript.TurnError("the model request failed: OpenRouter returned 503")}
	// Decoding writes empty lists where the originals had none, so compare
	// the stored form.
	var got, expected [][2]string
	for i := range want {
		got, expected = append(got, row(session.Transcript[i])), append(expected, row(want[i]))
	}
	if len(session.Transcript) != len(want) || !reflect.DeepEqual(got, expected) {
		t.Errorf("transcript = %v", got)
	}
	if latest := transcript.LatestTurnStart(session.Transcript); latest.Model != skill.Model || latest.Effort != catalog.EffortLow {
		t.Errorf("latest turn start = %+v", latest)
	}
}

func TestStoredJSONKeepsEmptyListsForScripts(t *testing.T) {
	_, data, err := transcript.Encode(&transcript.AssistantBatch{Message: transcript.AssistantMessage{Text: "hi"}})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"message":{"text":"hi","reasoning":"","tool_calls":[],"continuation_metadata":[],"usage":null},"outcomes":[]}`
	if data != want {
		t.Errorf("data = %s", data)
	}
}

// readError returns the error from reading a session whose stored rows are
// rows.
func readError(t *testing.T, rows ...[2]string) string {
	t.Helper()
	s := memory(t)
	id := create(t, s)
	for _, row := range rows {
		if _, err := s.db.Exec("INSERT INTO transcript_entries (session_id, kind, data) VALUES (?, ?, ?)", id, row[0], row[1]); err != nil {
			t.Fatal(err)
		}
	}
	_, err := s.Read(id)
	if err == nil {
		t.Fatalf("rows %v were read", rows)
	}
	return err.Error()
}

func row(entry transcript.Entry) [2]string {
	kind, data, err := transcript.Encode(entry)
	if err != nil {
		panic(err)
	}
	return [2]string{kind, data}
}

func TestMalformedTranscriptsFailTheRead(t *testing.T) {
	hello := `{"type":"user_message","content":{"parts":[{"type":"text","content":"hello"}]}}`
	unnamed := &transcript.AssistantBatch{Message: transcript.AssistantMessage{ToolCalls: []transcript.ToolCall{{CallID: "a"}}},
		Outcomes: []transcript.ToolOutcome{transcript.Completed("ok")}}
	missing := &transcript.AssistantBatch{Message: transcript.AssistantMessage{ToolCalls: calls("a", "b")},
		Outcomes: []transcript.ToolOutcome{transcript.Completed("ok")}}
	repeated := &transcript.AssistantBatch{Message: transcript.AssistantMessage{ToolCalls: calls("a", "a")},
		Outcomes: []transcript.ToolOutcome{transcript.Completed("ok"), transcript.Completed("ok")}}
	unqualified := turn("hello")
	unqualified.Model = "deepseek/deepseek-v4.1-flash"
	for _, test := range []struct {
		rows [][2]string
		want string
	}{
		{[][2]string{row(&transcript.AssistantBatch{})}, "transcript does not open with a turn start"},
		{[][2]string{row(turn("hello")), {"mystery", "{}"}}, `unknown transcript entry kind "mystery"`},
		{[][2]string{row(turn("hello")), {"assistant_batch", `{"message":{"text":"","reasoning":"","tool_calls":[],"continuation_metadata":[],"usage":null},"outcomes":[],"extra":true}`}}, "assistant_batch entry"},
		{[][2]string{{"turn_start", `{"model":"openrouter:a/b","effort":"low","mode":"ask","input":` + hello + `,"extra":true}`}}, "turn_start entry"},
		{[][2]string{{"turn_start", `{"model":"openrouter:a/b","mode":"ask","input":` + hello + `}`}}, "unknown effort"},
		{[][2]string{{"turn_start", `{"model":"openrouter:a/b","effort":"loud","mode":"ask","input":` + hello + `}`}}, "unknown effort"},
		{[][2]string{{"turn_start", `{"model":"openrouter:a/b","effort":"low","mode":"ask","input":{"type":"voice_note","content":"hi"}}`}}, "unknown turn input"},
		{[][2]string{row(unqualified)}, "not a qualified model ID"},
		{[][2]string{row(turn("hello")), row(unnamed)}, "empty tool name"},
		{[][2]string{row(turn("hello")), row(missing)}, "2 tool calls has 1 tool outcomes"},
		{[][2]string{row(turn("hello")), row(repeated)}, "repeated"},
	} {
		if err := readError(t, test.rows...); !strings.Contains(err, test.want) {
			t.Errorf("%v: %s", test.rows, err)
		}
	}
}

func TestAppendsValidateAndNeverCreateASession(t *testing.T) {
	s := memory(t)
	id := create(t, s)
	unqualified := turn("hello")
	unqualified.Model = "unknown:model"
	if _, err := s.AppendTurnStart(id, unqualified); err == nil {
		t.Error("an unqualified model was appended")
	}
	if err := s.AppendBatch(id, &transcript.AssistantBatch{Message: transcript.AssistantMessage{ToolCalls: calls("a")}}); err == nil {
		t.Error("a batch without its outcome was appended")
	}
	if err := s.AppendCompaction(id, &transcript.Compaction{}); err == nil {
		t.Error("an empty compaction was appended")
	}
	s.AppendTurnStart(id, turn("hello"))
	s.db.Exec("UPDATE sessions SET updated_at = '2026-01-01T00:00:00.000Z' WHERE id = ?", id)
	if err := s.AppendCompaction(id, &transcript.Compaction{Summary: "Said hello."}); err != nil {
		t.Fatal(err)
	}
	if session, _ := s.Read(id); session.Summary.UpdatedAt == "2026-01-01T00:00:00.000Z" {
		t.Error("a compaction did not update activity")
	}
	if _, err := s.AppendTurnStart("missing", turn("hello")); err == nil {
		t.Error("appending created a session")
	}
}

func TestTurnStartsAdoptATitleOnce(t *testing.T) {
	s := memory(t)
	for _, test := range []struct {
		input transcript.TurnInput
		want  string
	}{
		{turn("\n\nFirst line\nsecond line").Input, "First line"},
		{turn(strings.Repeat("x", maxTitleChars+10)).Input, strings.Repeat("x", maxTitleChars-1) + "…"},
		{transcript.TurnInput{Message: &transcript.UserMessage{Parts: []transcript.UserMessagePart{{Image: &transcript.ImageAttachment{Data: "aGk=", MimeType: "image/png"}}}}}, "Image"},
		{transcript.TurnInput{Skill: &transcript.SkillInvocation{Name: "goal", Arguments: "Pass the tests."}}, "/goal Pass the tests."},
	} {
		id := create(t, s)
		start := turn("")
		start.Input = test.input
		summary, err := s.AppendTurnStart(id, start)
		if err != nil {
			t.Fatal(err)
		}
		if summary.Title != test.want || utf8.RuneCountInString(summary.Title) > maxTitleChars {
			t.Errorf("title = %q, want %q", summary.Title, test.want)
		}
		later, _ := s.AppendTurnStart(id, turn("Something else"))
		if later.Title != test.want || later.UpdatedAt < summary.UpdatedAt {
			t.Errorf("later summary = %+v", later)
		}
	}
	blank := create(t, s)
	if summary, _ := s.AppendTurnStart(blank, turn("  \n")); summary.Title != "" {
		t.Errorf("a blank prompt took the title %q", summary.Title)
	}
}

func TestListOrdersByActivityFiltersByWorkspaceAndDeleteCascades(t *testing.T) {
	s := memory(t)
	first, second := create(t, s), create(t, s)
	other, err := s.Create("/Users/kyle/projects/other")
	if err != nil {
		t.Fatal(err)
	}
	for id, at := range map[string]string{first: "2026-09-18T10:00:00.000Z", second: "2026-09-18T10:00:00.000Z", other.ID: "2026-09-18T11:00:00.000Z"} {
		s.db.Exec("UPDATE sessions SET updated_at = ? WHERE id = ?", at, id)
	}
	tied := []string{first, second}
	if second < first {
		tied = []string{second, first}
	}
	ids := func(summaries []Summary) []string {
		var ids []string
		for _, summary := range summaries {
			ids = append(ids, summary.ID)
		}
		return ids
	}
	all, _ := s.List("")
	if want := append([]string{other.ID}, tied...); !reflect.DeepEqual(ids(all), want) {
		t.Errorf("all = %v", ids(all))
	}
	if inWorkspace, _ := s.List(workspace); !reflect.DeepEqual(ids(inWorkspace), tied) {
		t.Errorf("workspace = %v", ids(inWorkspace))
	}
	if _, err := s.Create("relative/path"); err == nil {
		t.Error("a relative workspace was accepted")
	}

	s.AppendTurnStart(first, turn("hello"))
	for range 2 {
		if err := s.Delete(first); err != nil {
			t.Fatal(err)
		}
	}
	var entries int
	s.db.QueryRow("SELECT count(*) FROM transcript_entries").Scan(&entries)
	if _, err := s.Read(first); err != ErrNotFound || entries != 0 {
		t.Errorf("after delete: %v, %d entries", err, entries)
	}
}
