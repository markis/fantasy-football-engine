package sleeper

import (
	"encoding/json"
	"testing"
)

// leagueDocsSample mirrors the /league/<id> and /user/<id>/leagues docs
// example, including a null previous_league_id (first-year league case).
const leagueDocsSample = `[
  {
    "total_rosters": 12,
    "status": "in_season",
    "sport": "nfl",
    "settings": { "type": 2, "best_ball": 0, "waiver_budget": 100 },
    "season_type": "regular",
    "season": "2018",
    "scoring_settings": { "ppr": 1 },
    "roster_positions": ["QB", "RB", "RB", "WR", "WR", "TE", "FLEX", "SUPER_FLEX", "BN", "BN"],
    "previous_league_id": null,
    "name": "Sleeperbot Dynasty",
    "league_id": "289646328504385536",
    "draft_id": "289646328508579840",
    "avatar": "efaefa889ae24046a53265a3c71b8b64"
  },
  {
    "total_rosters": 10,
    "status": "complete",
    "season": "2017",
    "settings": null,
    "roster_positions": ["QB", "RB", "BN"],
    "previous_league_id": "198946952535085056",
    "name": "Prior Season League",
    "league_id": "111111111111111111"
  }
]`

func TestParseLeaguesDocsSample(t *testing.T) {
	var raw []map[string]any
	if err := json.Unmarshal([]byte(leagueDocsSample), &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	leagues := ParseLeagues(raw)
	eqInt(t, "len", len(leagues), 2)

	l0 := leagues[0]
	eqStr(t, "LeagueID", l0.LeagueID, "289646328504385536")
	if l0.Name == nil || *l0.Name != "Sleeperbot Dynasty" {
		t.Errorf("Name: got %#v", l0.Name)
	}
	if l0.Season == nil || *l0.Season != "2018" {
		t.Errorf("Season: got %#v", l0.Season)
	}
	if l0.Status == nil || *l0.Status != "in_season" {
		t.Errorf("Status: got %#v", l0.Status)
	}
	eqInt(t, "TotalRosters", l0.TotalRosters, 12)
	eqInt(t, "RosterPositions len", len(l0.RosterPositions), 10)
	if l0.RosterPositions[7] != "SUPER_FLEX" {
		t.Errorf("RosterPositions[7]: got %q", l0.RosterPositions[7])
	}
	eqMapKey(t, "Settings", l0.Settings, "type", float64(2))
	eqMapKey(t, "Settings", l0.Settings, "waiver_budget", float64(100))
	// previous_league_id null (first-year league) -> nil, not "".
	if l0.PreviousLeagueID != nil {
		t.Errorf("PreviousLeagueID: want nil for null, got %#v", l0.PreviousLeagueID)
	}

	// Second league: settings null -> nil map; previous_league_id present.
	l1 := leagues[1]
	if l1.Settings != nil {
		t.Errorf("Settings[1]: want nil for null, got %#v", l1.Settings)
	}
	if l1.PreviousLeagueID == nil || *l1.PreviousLeagueID != "198946952535085056" {
		t.Errorf("PreviousLeagueID[1]: got %#v", l1.PreviousLeagueID)
	}
}
