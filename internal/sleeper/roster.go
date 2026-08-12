package sleeper

// Roster models one entry from /league/<id>/rosters. It is decoded directly
// via encoding/json.
//
// Field semantics:
//   - RosterID is the integer roster id (FlexInt -> 0 if absent/unparseable).
//   - OwnerID/CoOwnerID are the owner/co-owner user ids as *FlexString: a
//     present string/number yields a non-nil pointer whose string form matches
//     the raw value; a JSON null yields nil (so callers can distinguish "no
//     owner" from an empty id).
//   - LeagueID is the league id (*FlexString; null -> nil).
//   - Players/Starters/Taxi/Reserve are coerced []string via FlexStringSlice
//     (nil/empty entries dropped).
//   - Settings/Metadata are the opaque nested objects (wins/losses/waiver
//     budget, team_name, ...); kept as map[string]any since only a few subkeys
//     are read and the shape is loose.
type Roster struct {
	RosterID  FlexInt         `json:"roster_id"`
	OwnerID   *FlexString     `json:"owner_id"`
	CoOwnerID *FlexString     `json:"co_owner_id"`
	LeagueID  *FlexString     `json:"league_id"`
	Players   FlexStringSlice `json:"players"`
	Starters  FlexStringSlice `json:"starters"`
	Taxi      FlexStringSlice `json:"taxi"`
	Reserve   FlexStringSlice `json:"reserve"`
	Settings  map[string]any  `json:"settings"`
	Metadata  map[string]any  `json:"metadata"`
}
