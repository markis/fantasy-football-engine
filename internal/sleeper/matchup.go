package sleeper

// Matchup models one entry from /league/<id>/matchups/<week>. Each object is
// one team (NOT a head-to-head pairing); pair rows by equal MatchupID. The
// bench is deduced by removing Starters from Players. Decoded directly via
// encoding/json.
//
// RosterID is the integer roster id. MatchupID is *FlexInt (nil for a null
// matchup_id, e.g. a bye/unpaired entry). Points/CustomPoints are *FlexFloat
// (nil for null; CustomPoints is a commissioner override). Starters/Players
// are coerced []string of player/team-defense ids via FlexStringSlice. JSON
// tags mirror Sleeper's snake_case keys for faithful MCP marshaling.
type Matchup struct {
	RosterID     FlexInt         `json:"roster_id"`
	MatchupID    *FlexInt        `json:"matchup_id"`
	Points       *FlexFloat      `json:"points"`
	CustomPoints *FlexFloat      `json:"custom_points"`
	Starters     FlexStringSlice `json:"starters"`
	Players      FlexStringSlice `json:"players"`
}
