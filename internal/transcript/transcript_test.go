package transcript

import (
	"reflect"
	"testing"
)

func TestUsageSummaryKeepsLatestTokensAndTotalReportedCost(t *testing.T) {
	firstCost, secondCost, zeroCost := 0.25, 0.5, 0.0
	first := &AssistantBatch{Message: AssistantMessage{Usage: &Usage{InputTokens: 10, OutputTokens: 5, Cost: &firstCost}}}
	second := &AssistantBatch{Message: AssistantMessage{Usage: &Usage{InputTokens: 20, OutputTokens: 7, Cost: &secondCost}}}
	unreported := &AssistantBatch{}
	unpriced := &AssistantBatch{Message: AssistantMessage{Usage: &Usage{InputTokens: 30, OutputTokens: 9}}}
	free := &AssistantBatch{Message: AssistantMessage{Usage: &Usage{Cost: &zeroCost}}}
	for _, test := range []struct {
		name    string
		entries []Entry
		used    uint64
		cost    *float64
		ok      bool
	}{
		{"empty", nil, 0, nil, false},
		{"no assistant", []Entry{&TurnStart{}, TurnError("failed")}, 0, nil, false},
		{"no usage", []Entry{unreported}, 0, nil, true},
		{"no price", []Entry{unpriced}, 39, nil, true},
		{"zero price", []Entry{free}, 0, &zeroCost, true},
		{"latest tokens and total cost", []Entry{first, &TurnStart{}, second, TurnError("failed")}, 27, new(0.75), true},
		{"latest lacks usage", []Entry{first, second, unreported}, 0, new(0.75), true},
		{"latest lacks price", []Entry{first, unpriced}, 39, &firstCost, true},
		{"compaction after the latest", []Entry{first, &Compaction{Summary: "s", Usage: &Usage{InputTokens: 50, OutputTokens: 5, Cost: &secondCost}}}, 0, new(0.75), true},
		{"response after a compaction", []Entry{first, &Compaction{Summary: "s"}, second}, 27, new(0.75), true},
	} {
		t.Run(test.name, func(t *testing.T) {
			used, cost, ok := UsageSummary(test.entries)
			if used != test.used || ok != test.ok || (cost == nil) != (test.cost == nil) {
				t.Fatalf("usage = %d, %v, %v; want %d, %v, %v", used, cost, ok, test.used, test.cost, test.ok)
			}
			if cost != nil && *cost != *test.cost {
				t.Errorf("cost = %v, want %v", *cost, *test.cost)
			}
		})
	}
}

func TestSkillMessagePlacesArguments(t *testing.T) {
	for _, test := range []struct {
		name, instructions, want string
	}{
		{"placeholder", "Plan <task>$ARGUMENTS</task>.", "Skill /plan invoked.\n\nInstructions:\nPlan <task>mode switching</task>."},
		{"no placeholder", "Plan it.", "Skill /plan invoked.\n\nInstructions:\nPlan it.\n\nArguments:\nmode switching"},
	} {
		t.Run(test.name, func(t *testing.T) {
			skill := SkillInvocation{Name: "plan", Arguments: "mode switching", Instructions: test.instructions}
			if text := skill.Message().Text(); text != test.want {
				t.Errorf("text = %q, want %q", text, test.want)
			}
		})
	}
}

func TestACompactionEncodesAndValidates(t *testing.T) {
	cost := 0.5
	compaction := &Compaction{Summary: "Fixed the parser.", Usage: &Usage{InputTokens: 900, OutputTokens: 40, Cost: &cost}}
	kind, data, err := Encode(compaction)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(kind, data)
	if err != nil || kind != "compaction" || !reflect.DeepEqual(decoded, compaction) {
		t.Errorf("decoded %s %s = %+v, %v", kind, data, decoded, err)
	}
	start := &TurnStart{Model: "openrouter:a/b", Effort: "default", Mode: ModeAsk, Input: TurnInput{Message: new(TextMessage("hi"))}}
	if err := Validate([]Entry{start, compaction}); err != nil {
		t.Error(err)
	}
	if err := Validate([]Entry{start, &Compaction{Summary: " \n"}}); err == nil {
		t.Error("an empty summary was valid")
	}
}

func TestRequestEntriesKeepTheLatestTurnStartAfterTheLatestCompaction(t *testing.T) {
	start, later, batch := &TurnStart{Effort: "low"}, &TurnStart{Effort: "high"}, &AssistantBatch{}
	first, second := &Compaction{Summary: "first"}, &Compaction{Summary: "second"}
	for _, test := range []struct {
		name          string
		entries, want []Entry
	}{
		{"none", []Entry{start, batch}, []Entry{start, batch}},
		{"one", []Entry{start, batch, first, batch}, []Entry{first, start, batch}},
		{"two", []Entry{start, first, later, batch, second, batch}, []Entry{second, later, batch}},
		{"two in one turn", []Entry{start, first, batch, second, batch}, []Entry{second, start, batch}},
	} {
		if got := RequestEntries(test.entries); !reflect.DeepEqual(got, test.want) {
			t.Errorf("%s: entries = %v", test.name, got)
		}
	}
}
