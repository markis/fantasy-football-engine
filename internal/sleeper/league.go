package sleeper

// League models one league object from /league/<id> (league info) and
// /user/<id>/leagues/nfl/<season> (a user's leagues); both endpoints return
// the same shape. It is decoded directly via encoding/json.
//
// JSON tags mirror Sleeper's snake_case keys so the struct round-trips
// faithfully when marshaled for the MCP passthrough. Nullable text fields use
// *FlexString so a JSON null decodes to nil (and persists as SQL NULL rather
// than ""); present values keep their string form. LeagueID/TotalRosters use
// the lenient value types because Sleeper sends some scalars as strings in
// some records and numbers in others. Settings is the typed nested object
// (see settings.go); ScoringSettings stays map[string]any since only a few
// subkeys are read and the shape is loose.
type League struct {
	LeagueID         FlexString     `json:"league_id"`
	Name             *FlexString    `json:"name"`
	Season           *FlexString    `json:"season"`
	Status           *FlexString    `json:"status"`
	Sport            *FlexString    `json:"sport"`
	SeasonType       *FlexString    `json:"season_type"`
	TotalRosters     FlexInt        `json:"total_rosters"`
	RosterPositions  []string       `json:"roster_positions"`
	Settings         *Settings      `json:"settings"`
	ScoringSettings  map[string]any `json:"scoring_settings"`
	DraftID          *FlexString    `json:"draft_id"`
	Avatar           *FlexString    `json:"avatar"`
	PreviousLeagueID *FlexString    `json:"previous_league_id"`
}
