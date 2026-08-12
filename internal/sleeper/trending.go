package sleeper

import "ff-engine/internal/util"

// TrendingPlayer models one entry from
// /players/nfl/trending/<add|drop>?lookback_hours=&limit=. PlayerID is the
// Sleeper player/team/defense id; Count is the number of adds or drops in the
// lookback window. JSON tags mirror Sleeper's snake_case keys.
type TrendingPlayer struct {
	PlayerID string `json:"player_id"`
	Count    int    `json:"count"`
}

// ParseTrendingPlayer builds a TrendingPlayer from one raw trending entry.
func ParseTrendingPlayer(m map[string]any) TrendingPlayer {
	return TrendingPlayer{
		PlayerID: flexStr(m["player_id"]),
		Count:    util.ToInt(m["count"]),
	}
}

// ParseTrendingPlayers builds a typed slice from the raw trending payload.
func ParseTrendingPlayers(ms []map[string]any) []TrendingPlayer {
	out := make([]TrendingPlayer, len(ms))
	for i, m := range ms {
		out[i] = ParseTrendingPlayer(m)
	}
	return out
}
