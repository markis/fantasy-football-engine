package sleeper

import "ff-engine/internal/util"

// League models one league object from /league/<id> (league info) and
// /user/<id>/leagues/nfl/<season> (a user's leagues); both endpoints return
// the same shape. It is produced by ParseLeague from the raw decoded map —
// the client decodes into maps then parses, because Sleeper sends some scalar
// fields as strings in some records and numbers in others (and total_rosters
// is always a number).
//
// JSON tags mirror Sleeper's snake_case keys so the struct round-trips
// faithfully when marshaled for the MCP passthrough. Nullable text fields use
// *string so a JSON null decodes to nil (and persists as SQL NULL rather than
// ""); present values keep their string form. Settings/ScoringSettings are the
// opaque nested objects (type, best_ball, waiver_budget / ppr, ...) and stay
// map[string]any since only a few subkeys are read and the shape is loose.
type League struct {
	LeagueID         string         `json:"league_id"`
	Name             *string        `json:"name"`
	Season           *string        `json:"season"`
	Status           *string        `json:"status"`
	Sport            *string        `json:"sport"`
	SeasonType       *string        `json:"season_type"`
	TotalRosters     int            `json:"total_rosters"`
	RosterPositions  []string       `json:"roster_positions"`
	Settings         map[string]any `json:"settings"`
	ScoringSettings  map[string]any `json:"scoring_settings"`
	DraftID          *string        `json:"draft_id"`
	Avatar           *string        `json:"avatar"`
	PreviousLeagueID *string        `json:"previous_league_id"`
}

// ParseLeague builds a League from one raw league object.
func ParseLeague(m map[string]any) League {
	return League{
		LeagueID:         flexStr(m["league_id"]),
		Name:             strPtr(m["name"]),
		Season:           strPtr(m["season"]),
		Status:           strPtr(m["status"]),
		Sport:            strPtr(m["sport"]),
		SeasonType:       strPtr(m["season_type"]),
		TotalRosters:     util.ToInt(m["total_rosters"]),
		RosterPositions:  util.ToStringSlice(m["roster_positions"]),
		Settings:         util.AsMap(m["settings"]),
		ScoringSettings:  util.AsMap(m["scoring_settings"]),
		DraftID:          strPtr(m["draft_id"]),
		Avatar:           strPtr(m["avatar"]),
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
