package sleeper

import "ff-engine/internal/util"

// DraftPick models one entry from /draft/<id>/picks, produced by ParseDraftPick.
//
// PlayerID is *string (nil for null; the selected player/team-defense id).
// PickedBy is the destination user_id (coerced to string; may be "" for
// leagues without a user in every slot). RosterID is the destination roster
// id coerced to int via util.ToInt — Sleeper sends it as a string ("1") in
// some payloads and a number in others, and omits it entirely in some; absent
// or null yields 0. Round/DraftSlot/PickNo are int. IsKeeper is *bool (nil
// for null). Metadata is the opaque point-in-time player snapshot (first_name,
// last_name, team, position, number, news_updated, ...). DraftID is the
// parent draft id. JSON tags mirror Sleeper's snake_case keys.
type DraftPick struct {
	DraftID   string         `json:"draft_id"`
	PlayerID  *string        `json:"player_id"`
	PickedBy  string         `json:"picked_by"`
	RosterID  int            `json:"roster_id"`
	Round     int            `json:"round"`
	DraftSlot int            `json:"draft_slot"`
	PickNo    int            `json:"pick_no"`
	IsKeeper  *bool          `json:"is_keeper"`
	Metadata  map[string]any `json:"metadata"`
}

// ParseDraftPick builds a DraftPick from one raw draft-picks entry.
func ParseDraftPick(m map[string]any) DraftPick {
	return DraftPick{
		DraftID:   flexStr(m["draft_id"]),
		PlayerID:  strPtr(m["player_id"]),
		PickedBy:  flexStr(m["picked_by"]),
		RosterID:  util.ToInt(m["roster_id"]),
		Round:     util.ToInt(m["round"]),
		DraftSlot: util.ToInt(m["draft_slot"]),
		PickNo:    util.ToInt(m["pick_no"]),
		IsKeeper:  boolPtr(m["is_keeper"]),
		Metadata:  util.AsMap(m["metadata"]),
	}
}

// ParseDraftPicks builds a typed slice from the raw draft-picks payload.
func ParseDraftPicks(ms []map[string]any) []DraftPick {
	out := make([]DraftPick, len(ms))
	for i, m := range ms {
		out[i] = ParseDraftPick(m)
	}
	return out
}
