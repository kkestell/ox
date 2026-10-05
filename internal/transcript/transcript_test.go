package transcript

import "testing"

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
