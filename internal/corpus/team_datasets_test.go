package corpus

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ff-engine/internal/models"
	"ff-engine/internal/sleeper"
)

// testTimeUTC builds a UTC timestamp for deterministic assertions.
func testTimeUTC(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// nonEmptyLines splits jsonl content into non-blank lines.
func nonEmptyLines(s string) []string {
	var out []string
	for line := range strings.SplitSeq(s, "\n") {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return out
}

// --- small team_dataset helpers ---

func TestIntPtrStrAndDflt(t *testing.T) {
	n := 5
	if intPtrStr(&n) != "5" || intPtrStr(nil) != "" {
		t.Fatal("intPtrStr wrong")
	}
	if dflt([]int(nil)) == nil || len(dflt([]int(nil))) != 0 {
		t.Fatal("dflt(nil) must return empty non-nil slice")
	}
	if got := dflt([]int{1}); len(got) != 1 {
		t.Fatal("dflt must pass through non-nil slices")
	}
}

func TestSortedStringSlice(t *testing.T) {
	in := []string{"c", "a", "b"}
	got := sortedStringSlice(in)
	if strings.Join(got, "") != "abc" {
		t.Fatalf("not sorted: %v", got)
	}
	in[0] = "z" // must not affect the copy
	if got[0] != "a" {
		t.Fatal("sortedStringSlice must copy its input")
	}
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Tight Ends Only!":   "tight-ends-only",
		"  Dynasty  League ": "dynasty-league",
		"???":                "league",
	}
	for in, want := range cases {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

// --- roster partitioning ---

func TestBuildRosterOwnerMap(t *testing.T) {
	owned := sleeper.FlexString("111")
	rosters := []sleeper.Roster{
		{RosterID: 1, OwnerID: &owned},
		{RosterID: 2}, // orphaned roster
	}
	m := buildRosterOwnerMap(rosters)
	if m[1] != "111" {
		t.Fatalf("owner wrong: %q", m[1])
	}
	if m[2] != "<nil>" {
		t.Fatalf("orphan must map to <nil> sentinel, got %q", m[2])
	}
}

func TestSlotHelpers(t *testing.T) {
	lg := &sleeper.League{RosterPositions: []string{"QB", "RB", "BN", "BN"}}
	if got := nonBenchSlots(lg); strings.Join(got, ",") != "QB,RB" {
		t.Fatalf("nonBenchSlots wrong: %v", got)
	}
	if countBenchSlots(lg) != 2 {
		t.Fatalf("countBenchSlots wrong: %d", countBenchSlots(lg))
	}
}

func TestComputeFaabRemaining(t *testing.T) {
	// League settings must be decoded via JSON: the raw map powering Has()
	// is unexported and only populated by UnmarshalJSON.
	var lgSettings sleeper.Settings
	if err := json.Unmarshal([]byte(`{"waiver_budget": 200}`), &lgSettings); err != nil {
		t.Fatal(err)
	}
	roster := &sleeper.Roster{Settings: map[string]any{"waiver_budget_used": float64(30)}}
	if got := computeFaabRemaining(roster, &sleeper.League{Settings: &lgSettings}); got != 170 {
		t.Fatalf("custom budget: want 170, got %d", got)
	}
	// Absent league setting falls back to Sleeper's default of 100.
	var noBudget sleeper.Settings
	if err := json.Unmarshal([]byte(`{"type": 2}`), &noBudget); err != nil {
		t.Fatal(err)
	}
	if got := computeFaabRemaining(&sleeper.Roster{}, &sleeper.League{Settings: &noBudget}); got != 100 {
		t.Fatalf("default budget: want 100, got %d", got)
	}
	// Overspend clamps at zero, and a nil Settings must not panic.
	var zeroBudget sleeper.Settings
	if err := json.Unmarshal([]byte(`{"waiver_budget": 50}`), &zeroBudget); err != nil {
		t.Fatal(err)
	}
	heavy := &sleeper.Roster{Settings: map[string]any{"waiver_budget_used": float64(80)}}
	if got := computeFaabRemaining(heavy, &sleeper.League{Settings: &zeroBudget}); got != 0 {
		t.Fatalf("clamp: want 0, got %d", got)
	}
	if got := computeFaabRemaining(&sleeper.Roster{}, &sleeper.League{}); got != 100 {
		t.Fatalf("nil settings: want 100, got %d", got)
	}
}

func TestResolveTeamName(t *testing.T) {
	lf := models.LeagueFormat{Name: "Test League"}
	custom := &sleeper.Roster{Metadata: map[string]any{"team_name": "My Squad"}}
	if got := resolveTeamName(&lf, custom); got != "My Squad" {
		t.Fatalf("custom name must win: %q", got)
	}
	if got := resolveTeamName(&lf, &sleeper.Roster{}); got != "Test League (Markis)" {
		t.Fatalf("default name wrong: %q", got)
	}
}

func TestResolvePlayerSlot(t *testing.T) {
	starterSlot := map[string]string{"1": "QB"}
	taxi := stringSet([]string{"2"})
	reserve := stringSet([]string{"3"})
	if got := resolvePlayerSlot("1", starterSlot, taxi, reserve); got.slot != "QB" || got.bucket != "" {
		t.Fatalf("starter wrong: %+v", got)
	}
	if got := resolvePlayerSlot("2", starterSlot, taxi, reserve); got.slot != "TAXI" || got.bucket != "taxi" {
		t.Fatalf("taxi wrong: %+v", got)
	}
	if got := resolvePlayerSlot("3", starterSlot, taxi, reserve); got.slot != "IR" || got.bucket != "reserve" {
		t.Fatalf("reserve wrong: %+v", got)
	}
	if got := resolvePlayerSlot("9", starterSlot, taxi, reserve); got.slot != "BN" || got.bucket != "" {
		t.Fatalf("bench wrong: %+v", got)
	}
}

func TestBuildPlayerSlotRecord(t *testing.T) {
	name, pos, team, inj := "Player One", "WR", "SF", "Q"
	age := 24
	tv := 1500
	pr := &PlayerRow{
		SleeperPlayerID: "1", FullName: &name, Position: &pos, TeamAbbr: &team, Age: &age, InjuryStatus: &inj,
	}
	rk := &RankingRow{TradeValue: &tv}
	rec := buildPlayerSlotRecord("1", pr, rk, "WR")
	if rec.FullName == nil || *rec.FullName != "Player One" || rec.Slot != "WR" {
		t.Fatalf("record wrong: %+v", rec)
	}
	if rec.TradeValue == nil || *rec.TradeValue != 1500 || rec.Age == nil || *rec.Age != 24 {
		t.Fatalf("nullable fields wrong: %+v", rec)
	}
	// No ranking row -> nil trade value; empty strings -> nil.
	rec = buildPlayerSlotRecord("1", &PlayerRow{SleeperPlayerID: "1"}, nil, "BN")
	if rec.TradeValue != nil || rec.FullName != nil || rec.InjuryStatus != nil {
		t.Fatalf("nil ranking/empty fields must stay nil: %+v", rec)
	}
}

// --- markdown / artifact rendering ---

func TestRenderRosterMarkdown(t *testing.T) {
	name, pos, team := "Player One", "WR", "SF"
	leagues := []TeamState{{
		League: TeamStateLeague{Name: "League A"},
		Team:   TeamStateTeam{Roster: []RosterSlot{{FullName: &name, Position: &pos, NflTeam: &team}}},
	}}
	md := renderRosterMarkdown(leagues, "2026-08-01T00:00:00Z")
	for _, want := range []string{"## League A", "- Player One (WR, SF)"} {
		if !strings.Contains(md, want) {
			t.Errorf("missing %q in:\n%s", want, md)
		}
	}
}

func TestWriteTeamArtifacts(t *testing.T) {
	dir := t.TempDir()
	if err := writeTeamArtifacts(dir, []TeamState{}, "2026-08-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"team-state.json", "roster.md", "future-picks.json", "league-settings.json", "transaction-history.jsonl"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("artifact %s missing: %v", f, err)
		}
	}
}

// --- dataset writers ---

func TestWritePlayersJSONL(t *testing.T) {
	dsDir := t.TempDir()
	name, pos, team, status := "Player One", "WR", "SF", "Active"
	pr := map[string]*PlayerRow{
		"1": {
			SleeperPlayerID: "1", FullName: &name, Position: &pos, TeamAbbr: &team,
			Status: &status, Active: true,
		},
		"2": {SleeperPlayerID: "2"},
	}
	watch := []string{"2", "1"}
	ownership := map[string][][2]string{"1": {{"League A", "owned"}}}

	writePlayersJSONL(dsDir, watch, pr, ownership)
	lines := nonEmptyLines(read(t, filepath.Join(dsDir, "players.jsonl")))
	if len(lines) != 2 {
		t.Fatalf("want 2 player rows, got %d", len(lines))
	}
	// Sorted output: player "1" comes first.
	var first map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatal(err)
	}
	if first["sleeper_player_id"] != "1" || first["nfl_id"] != "nfl:1" || first["full_name"] != "Player One" {
		t.Fatalf("first row wrong: %v", first)
	}
	leagues, ok := first["leagues"].([]any)
	if !ok || len(leagues) != 1 {
		t.Fatalf("leagues annotation wrong: %v", first["leagues"])
	}
	// Missing player rows still emit a record with empty strings; Active is
	// NOT NULL in the player table, so a zero row reports false, not null.
	var second map[string]any
	if err := json.Unmarshal([]byte(lines[1]), &second); err != nil {
		t.Fatal(err)
	}
	if second["full_name"] != "" || second["active"] != false {
		t.Fatalf("missing row must default: %v", second)
	}
}

func TestWriteSignalsJSONL(t *testing.T) {
	dsDir := t.TempDir()
	inj, body, notes := "Questionable", "ankle", "day-to-day"
	pr := map[string]*PlayerRow{
		"1": {
			SleeperPlayerID: "1", InjuryStatus: &inj, InjuryBodyPart: &body, InjuryNotes: &notes,
			LastSyncedAt: testTimeUTC(2026, 8, 1),
		},
		"2": {SleeperPlayerID: "2"},
	}
	watch := []string{"2", "1"}

	// Signals: only players with an injury status are emitted; observed_at
	// prefers the player's last sync time.
	sigCount := writeSignalsJSONL(dsDir, watch, pr, "2026-08-02T00:00:00Z")
	if sigCount != 1 {
		t.Fatalf("want 1 signal, got %d", sigCount)
	}
	var sig PlayerSignal
	if err := json.Unmarshal([]byte(nonEmptyLines(read(t, filepath.Join(dsDir, "player-signals.jsonl")))[0]), &sig); err != nil {
		t.Fatal(err)
	}
	if sig.PlayerID != "nfl:1" || sig.SignalType != "injury" || sig.Source != "Sleeper" {
		t.Fatalf("signal wrong: %+v", sig)
	}
	val, _ := sig.Value.(map[string]any)
	if val["status"] != "Questionable" || val["body_part"] != "ankle" {
		t.Fatalf("injury value wrong: %v", sig.Value)
	}
	if sig.ObservedAt != "2026-08-01T00:00:00Z" {
		t.Fatalf("observed_at must use LastSyncedAt: %q", sig.ObservedAt)
	}
}

func TestWriteValuationRecord(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "valuations.jsonl")
	tv := 5000
	dataDate := testTimeUTC(2026, 7, 15)
	snapDate := testTimeUTC(2026, 7, 1)
	// DataDate wins over SnapshotDate; both fall back to now.
	rk := &RankingRow{TradeValue: &tv, DataDate: &dataDate, SnapshotDate: snapDate}
	if got := writeValuationRecord(path, "1", rk, "FantasyCalc", "12t-1QB-PPR", "now"); got != 1 {
		t.Fatalf("want 1 record, got %d", got)
	}
	var val Valuation
	if err := json.Unmarshal([]byte(nonEmptyLines(read(t, path))[0]), &val); err != nil {
		t.Fatal(err)
	}
	// Value round-trips through JSON as float64.
	if val.ObservedAt != "2026-07-15T00:00:00Z" || val.Value != any(float64(5000)) || val.Source != "FantasyCalc" {
		t.Fatalf("valuation wrong: %+v", val)
	}
	// nil TradeValue -> nothing written.
	if got := writeValuationRecord(path, "2", &RankingRow{SnapshotDate: snapDate}, "s", "c", "now"); got != 0 {
		t.Fatalf("nil trade value must not write, got %d", got)
	}
}

func TestWriteNewsEventsJSONL(t *testing.T) {
	targetDir := t.TempDir()
	dsDir := filepath.Join(targetDir, "datasets")
	if err := os.MkdirAll(dsDir, 0o750); err != nil {
		t.Fatal(err)
	}
	pub := "2026-08-01T00:00:00Z"
	mustWrite(t, filepath.Join(targetDir, "evidence", "records", "a.json"),
		`{"id":"sha256:a","status":"current","canonical_url":"https://a","title":"A","published_at":"`+pub+`","player_ids":[],"team_ids":[]}`)
	mustWrite(t, filepath.Join(targetDir, "evidence", "records", "b.json"),
		`{"id":"sha256:b","status":"superseded"}`)
	got := writeNewsEventsJSONL(targetDir, dsDir)
	if got != 1 {
		t.Fatalf("want 1 news event, got %d", got)
	}
	var ev map[string]any
	if err := json.Unmarshal([]byte(nonEmptyLines(read(t, filepath.Join(dsDir, "news-events.jsonl")))[0]), &ev); err != nil {
		t.Fatal(err)
	}
	if ev["evidence_record_id"] != "sha256:a" {
		t.Fatalf("event wrong: %v", ev)
	}
	// Missing evidence dir -> zero events, no panic.
	if got := writeNewsEventsJSONL(t.TempDir(), filepath.Join(t.TempDir(), "ds")); got != 0 {
		t.Fatalf("missing dir must yield 0, got %d", got)
	}
}

func TestWriteEntitiesJSON(t *testing.T) {
	dsDir := t.TempDir()
	team := "SF"
	pr := map[string]*PlayerRow{
		"1": {SleeperPlayerID: "1", TeamAbbr: &team},
		"2": {SleeperPlayerID: "2", TeamAbbr: &team},
	}
	if err := writeEntitiesJSON(dsDir, pr, []string{"1", "2"}, "now"); err != nil {
		t.Fatal(err)
	}
	var ents map[string]any
	if err := json.Unmarshal([]byte(read(t, filepath.Join(dsDir, "entities.json"))), &ents); err != nil {
		t.Fatal(err)
	}
	if ents["players_count"] != any(float64(2)) {
		t.Fatalf("players_count wrong: %v", ents["players_count"])
	}
	teams, _ := ents["teams"].([]any)
	leagues, _ := ents["leagues"].([]any)
	if len(teams) != 1 || len(leagues) != len(models.LeagueFormats) {
		t.Fatalf("entities wrong: %v", ents)
	}
}
