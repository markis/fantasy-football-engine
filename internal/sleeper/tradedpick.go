package sleeper

import "ff-engine/internal/util"

// TradedPick models one entry from /league/<id>/traded_picks, produced by
// ParseTradedPick. Season/Round/RosterID/PreviousOwnerID/OwnerID are coerced
// to int via util.ToInt (handles the string season "2019" and int roster ids),
// with 0 for absent/unparseable.
//
// RosterID is the original owner's roster id; PreviousOwnerID is the previous
// owner's roster id (in the trade); OwnerID is the current owner's roster id
// (all are roster ids, NOT user ids, per the Sleeper docs). JSON tags mirror
// Sleeper's snake_case keys for faithful MCP passthrough marshaling.
type TradedPick struct {
	Season          int `json:"season"`
	Round           int `json:"round"`
	RosterID        int `json:"roster_id"`
	PreviousOwnerID int `json:"previous_owner_id"`
	OwnerID         int `json:"owner_id"`
}

// ParseTradedPick builds a TradedPick from one raw traded-picks entry.
func ParseTradedPick(m map[string]any) TradedPick {
	return TradedPick{
		Season:          util.ToInt(m["season"]),
		Round:           util.ToInt(m["round"]),
		RosterID:        util.ToInt(m["roster_id"]),
		PreviousOwnerID: util.ToInt(m["previous_owner_id"]),
		OwnerID:         util.ToInt(m["owner_id"]),
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
