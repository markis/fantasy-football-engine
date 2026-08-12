package sleeper

// TrendingPlayer models one entry from
// /players/nfl/trending/<add|drop>?lookback_hours=&limit=. PlayerID is the
// Sleeper player/team/defense id; Count is the number of adds or drops in the
// lookback window. JSON tags mirror Sleeper's snake_case keys. Decoded
// directly via encoding/json.
type TrendingPlayer struct {
	PlayerID FlexString `json:"player_id"`
	Count    FlexInt    `json:"count"`
}
