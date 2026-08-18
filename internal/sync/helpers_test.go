package sync

import (
	"encoding/json"
	"testing"

	"ff-engine/internal/sleeper"
)

// jsonUnmarshal is a thin alias keeping helper tests readable.
func jsonUnmarshal(data string, v any) error { return json.Unmarshal([]byte(data), v) }

// --- leaguemates helpers ---

func TestRosterSlot(t *testing.T) {
	starters := []string{"1"}
	taxi := []string{"2"}
	reserve := []string{"3"}
	if got := rosterSlot("1", starters, taxi, reserve); got != "starter" {
		t.Fatalf("starter: %q", got)
	}
	if got := rosterSlot("2", starters, taxi, reserve); got != "taxi" {
		t.Fatalf("taxi: %q", got)
	}
	if got := rosterSlot("3", starters, taxi, reserve); got != "reserve" {
		t.Fatalf("reserve: %q", got)
	}
	if got := rosterSlot("9", starters, taxi, reserve); got != "bench" {
		t.Fatalf("bench: %q", got)
	}
}

func TestNilIfEmpty(t *testing.T) {
	if got := nilIfEmpty(""); got != nil {
		t.Fatalf("empty: %v", got)
	}
	if got := nilIfEmpty("<nil>"); got != nil {
		t.Fatalf("sentinel: %v", got)
	}
	if got := nilIfEmpty("value"); got != "value" {
		t.Fatalf("value: %v", got)
	}
}

func TestHasSuperflexAndBestBall(t *testing.T) {
	sf := &sleeper.League{RosterPositions: []string{"QB", "super_flex"}}
	if !hasSuperflex(sf) {
		t.Fatal("super_flex must match case-insensitively")
	}
	if hasSuperflex(&sleeper.League{RosterPositions: []string{"QB", "FLEX"}}) {
		t.Fatal("no superflex must be false")
	}
	if isBestBall(&sleeper.League{}) {
		t.Fatal("nil settings must be false")
	}
	bb := &sleeper.League{}
	var settings sleeper.Settings
	if err := jsonUnmarshal(`{"best_ball":1}`, &settings); err != nil {
		t.Fatal(err)
	}
	bb.Settings = &settings
	if !isBestBall(bb) {
		t.Fatal("best_ball=1 must be true")
	}
}

// --- trades helpers ---

func TestScanWeeksFor(t *testing.T) {
	// Known current week: most recent maxWeeks weeks, descending.
	got := scanWeeksFor(8, 3)
	if len(got) != 3 || got[0] != 8 || got[1] != 7 || got[2] != 6 {
		t.Fatalf("weeks wrong: %v", got)
	}
	// Clamped at week 1.
	got = scanWeeksFor(2, 5)
	if len(got) != 2 || got[0] != 2 || got[1] != 1 {
		t.Fatalf("clamp wrong: %v", got)
	}
	// Unknown week: scan from the start.
	got = scanWeeksFor(0, 4)
	if len(got) != 4 || got[0] != 1 || got[3] != 4 {
		t.Fatalf("default scan wrong: %v", got)
	}
}

func TestTradeInvolvesWatchSet(t *testing.T) {
	watch := map[string]bool{"1": true}
	if !tradeInvolvesWatchSet(map[string]any{"1": nil, "2": nil}, nil, watch) {
		t.Fatal("add hit must match")
	}
	if !tradeInvolvesWatchSet(nil, map[string]any{"9": nil, "1": nil}, watch) {
		t.Fatal("drop hit must match")
	}
	if tradeInvolvesWatchSet(map[string]any{"7": nil}, map[string]any{"8": nil}, watch) {
		t.Fatal("no overlap must not match")
	}
}

// --- injuries helpers ---

func TestMatchInjuryPlayer(t *testing.T) {
	byYahoo := map[string]any{"Y1": "player-one"}
	byNameTeam := map[string]any{"player two|SF": "player-two"}
	if v, ok := matchInjuryPlayer(map[string]any{"yahoo_id": "Y1"}, byYahoo, byNameTeam); !ok || v != "player-one" {
		t.Fatalf("yahoo match wrong: %v %v", v, ok)
	}
	// yahoo_id absent (<nil> sentinel) falls back to name|team.
	if v, ok := matchInjuryPlayer(map[string]any{"name": "Player Two", "team_id": "SF"}, byYahoo, byNameTeam); !ok || v != "player-two" {
		t.Fatalf("name match wrong: %v %v", v, ok)
	}
	if _, ok := matchInjuryPlayer(map[string]any{"name": "Nobody"}, byYahoo, byNameTeam); ok {
		t.Fatal("unknown player must not match")
	}
}

func TestLatestPractice(t *testing.T) {
	item := map[string]any{
		"practice_1": "LP",
		"practice_2": "DNP",
		"practice_3": nil,
	}
	if got := latestPractice(item); got != "DNP" {
		t.Fatalf("latest non-nil practice must win: %q", got)
	}
	if got := latestPractice(map[string]any{}); got != "" {
		t.Fatalf("no practice must be empty: %q", got)
	}
	if got := latestPractice(map[string]any{"practice_3": "<nil>"}); got != "" {
		t.Fatalf("sentinel must be skipped: %q", got)
	}
}

// --- team assessment ---

func TestClassifyZone(t *testing.T) {
	cases := []struct {
		winNow, future float64
		want           string
	}{
		{60000, 50000, "CONTENDER"},
		{55000, 41000, "CONTENDER"},
		{45000, 20000, "WIN-NOW-AGING"},
		{20000, 50000, "REBUILDING"},
		{10000, 10000, "TANKING"},
		{35000, 35000, "FRINGE/RETOOL"},
	}
	for _, tc := range cases {
		if got := classifyZone(tc.winNow, tc.future); got != tc.want {
			t.Errorf("classifyZone(%v, %v) = %q, want %q", tc.winNow, tc.future, got, tc.want)
		}
	}
}
