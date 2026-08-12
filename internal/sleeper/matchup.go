package sleeper

import "ff-engine/internal/util"

// Matchup models one entry from /league/<id>/matchups/<week>. Each object is
// one team (NOT a head-to-head pairing); pair rows by equal MatchupID. The
// bench is deduced by removing Starters from Players.
//
// RosterID is the integer roster id. MatchupID is *int (nil for a null
// matchup_id, e.g. a bye/unpaired entry). Points/CustomPoints are *float64
// (nil for null; CustomPoints is a commissioner override). Starters/Players
// are coerced []string of player/team-defense ids via util.ToStringSlice.
// JSON tags mirror Sleeper's snake_case keys for faithful MCP marshaling.
type Matchup struct {
	RosterID     int      `json:"roster_id"`
	MatchupID    *int     `json:"matchup_id"`
	Points       *float64 `json:"points"`
	CustomPoints *float64 `json:"custom_points"`
	Starters     []string `json:"starters"`
	Players      []string `json:"players"`
}

// ParseMatchup builds a Matchup from one raw matchups entry.
func ParseMatchup(m map[string]any) Matchup {
	return Matchup{
		RosterID:     util.ToInt(m["roster_id"]),
		MatchupID:    intPtr(m["matchup_id"]),
		Points:       floatPtr(m["points"]),
		CustomPoints: floatPtr(m["custom_points"]),
		Starters:     util.ToStringSlice(m["starters"]),
		Players:      util.ToStringSlice(m["players"]),
	}
}

// ParseMatchups builds a typed slice from the raw matchups payload.
func ParseMatchups(ms []map[string]any) []Matchup {
	out := make([]Matchup, len(ms))
	for i, m := range ms {
		out[i] = ParseMatchup(m)
	}
	return out
}
