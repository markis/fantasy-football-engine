package sleeper

// Draft models one draft object from /league/<id>/drafts, /user/<id>/drafts/...,
// and /draft/<id> (all the same shape; the single-draft response additionally
// populates SlotToRosterID). Decoded directly via encoding/json.
//
// Nullable id/text fields use *FlexString (null -> nil). Type/Status/Sport/
// Season/SeasonType coerce via FlexString (null -> ""). StartTime/LastPicked/
// LastMessageTime/Created are epoch-ms -> *EpochMs (nil for null); when
// marshaled to JSON they serialize as RFC3339 (not the raw epoch-ms number).
// Settings/Metadata are the opaque nested objects (teams, rounds, slots_* /
// name, description, scoring_type). DraftOrder maps user_id -> draft slot and
// SlotToRosterID maps draft-slot string -> roster_id; both are kept as
// map[string]any since the keys are ids and the values are numbers whose
// exact int/float shape varies. Creators is a coerced []string of user ids
// via FlexStringSlice (nil for null). JSON tags mirror Sleeper's snake_case
// keys.
type Draft struct {
	DraftID         FlexString      `json:"draft_id"`
	LeagueID        *FlexString     `json:"league_id"`
	Type            FlexString      `json:"type"`
	Status          FlexString      `json:"status"`
	Sport           FlexString      `json:"sport"`
	Season          FlexString      `json:"season"`
	SeasonType      FlexString      `json:"season_type"`
	Settings        map[string]any  `json:"settings"`
	Metadata        map[string]any  `json:"metadata"`
	DraftOrder      map[string]any  `json:"draft_order"`
	SlotToRosterID  map[string]any  `json:"slot_to_roster_id"`
	Creators        FlexStringSlice `json:"creators"`
	StartTime       *EpochMs        `json:"start_time"`
	LastPicked      *EpochMs        `json:"last_picked"`
	LastMessageTime *EpochMs        `json:"last_message_time"`
	LastMessageID   *FlexString     `json:"last_message_id"`
	Created         *EpochMs        `json:"created"`
}
