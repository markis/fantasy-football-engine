package sleeper

import "ff-engine/internal/util"

// TradedPick is a typed view of one entry from /league/<id>/traded_picks,
// produced by ParseTradedPick. Season/Round/RosterID/OwnerID are coerced to
// int via util.ToInt (handles the string season "2019" and int roster ids),
// with 0 for absent/unparseable. The client method stays map-typed to
// preserve the MCP passthrough.
//
// RosterID is the original owner's roster id; OwnerID is the current
// owner's roster id (both are roster ids, NOT user ids, per the Sleeper docs).
type TradedPick struct {
	Season   int
	Round    int
	RosterID int
	OwnerID  int
}

// ParseTradedPick builds a TradedPick from one raw traded-picks entry.
func ParseTradedPick(m map[string]any) TradedPick {
	return TradedPick{
		Season:   util.ToInt(m["season"]),
		Round:    util.ToInt(m["round"]),
		RosterID: util.ToInt(m["roster_id"]),
		OwnerID:  util.ToInt(m["owner_id"]),
	}
}

// ParseTradedPicks builds a typed slice from the raw traded-picks payload.
func ParseTradedPicks(ms []map[string]any) []TradedPick {
	out := make([]TradedPick, len(ms))
	for i, m := range ms {
		out[i] = ParseTradedPick(m)
	}
	return out
}
