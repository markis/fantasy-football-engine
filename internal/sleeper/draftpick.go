package sleeper

// DraftPick models one entry from /draft/<id>/picks. Decoded directly via
// encoding/json.
//
// PlayerID is *FlexString (nil for null; the selected player/team-defense id).
// PickedBy is the destination user_id (coerced to string; may be "" for
// leagues without a user in every slot). RosterID is the destination roster
// id coerced to int via FlexInt — Sleeper sends it as a string ("1") in some
// payloads and a number in others, and omits it entirely in some; absent or
// null yields 0. Round/DraftSlot/PickNo are int (FlexInt). IsKeeper is
// *FlexBool (nil for null). Metadata is the opaque point-in-time player
// snapshot (first_name, last_name, team, position, number, news_updated, ...).
// DraftID is the parent draft id. JSON tags mirror Sleeper's snake_case keys.
type DraftPick struct {
	DraftID   FlexString     `json:"draft_id"`
	PlayerID  *FlexString    `json:"player_id"`
	PickedBy  FlexString     `json:"picked_by"`
	RosterID  FlexInt        `json:"roster_id"`
	Round     FlexInt        `json:"round"`
	DraftSlot FlexInt        `json:"draft_slot"`
	PickNo    FlexInt        `json:"pick_no"`
	IsKeeper  *FlexBool      `json:"is_keeper"`
	Metadata  map[string]any `json:"metadata"`
}
