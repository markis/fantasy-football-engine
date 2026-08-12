package sleeper

import "ff-engine/internal/util"

// League is a typed view of one entry from /league/<id> (league info) and
// /user/<id>/leagues/nfl/<season> (a user's leagues). Both endpoints return
// the same league-object shape. The client methods stay map-typed to
// preserve the MCP passthrough; this is the opt-in typed path.
//
// LeagueID is the snowflake string (always present). Name/Season/Status/
// PreviousLeagueID are *string so a JSON null decodes to nil and persists as
// SQL NULL (previous_league_id is genuinely null for first-year leagues);
// present values keep their string form. TotalRosters is int (always present
// for real leagues). RosterPositions is coerced []string. Settings is the
// opaque nested object (type, best_ball, waiver_budget, ...).
type League struct {
	LeagueID         string
	Name             *string
	Season           *string
	Status           *string
	TotalRosters     int
	RosterPositions  []string
	Settings         map[string]any
	PreviousLeagueID *string
}

// ParseLeague builds a League from one raw league object.
func ParseLeague(m map[string]any) League {
	return League{
		LeagueID:         flexStr(m["league_id"]),
		Name:             strPtr(m["name"]),
		Season:           strPtr(m["season"]),
		Status:           strPtr(m["status"]),
		TotalRosters:     util.ToInt(m["total_rosters"]),
		RosterPositions:  util.ToStringSlice(m["roster_positions"]),
		Settings:         util.AsMap(m["settings"]),
		PreviousLeagueID: strPtr(m["previous_league_id"]),
	}
}

// ParseLeagues builds a typed slice from a raw leagues payload (user leagues
// or any list of league objects).
func ParseLeagues(ms []map[string]any) []League {
	out := make([]League, len(ms))
	for i, m := range ms {
		out[i] = ParseLeague(m)
	}
	return out
}

// strPtr returns *string for v: nil for a JSON null, otherwise a pointer to
// v's string form (the string itself, or fmt.Sprint(v) for non-strings). Used
// for nullable text columns so null persists as SQL NULL rather than "".
func strPtr(v any) *string {
	if v == nil {
		return nil
	}
	s := flexStr(v)
	return &s
}
