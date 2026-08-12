package sleeper

import (
	"time"

	"ff-engine/internal/util"
)

// Draft models one draft object from /league/<id>/drafts, /user/<id>/drafts/...,
// and /draft/<id> (all the same shape; the single-draft response additionally
// populates SlotToRosterID). Produced by ParseDraft.
//
// Nullable id/text fields use *string (null -> nil). Type/Status/Sport/Season/
// SeasonType coerce via flexStr (null -> ""). StartTime/LastPicked/
// LastMessageTime/Created are epoch-ms -> *time.Time (nil for null); when
// marshaled to JSON they serialize as RFC3339 (not the raw epoch-ms number).
// Settings/Metadata are the opaque nested objects (teams, rounds, slots_* /
// name, description, scoring_type). DraftOrder maps user_id -> draft slot and
// SlotToRosterID maps draft-slot string -> roster_id; both are kept as
// map[string]any since the keys are ids and the values are numbers whose
// exact int/float shape varies. Creators is a coerced []string of user ids
// (nil for null). JSON tags mirror Sleeper's snake_case keys.
type Draft struct {
	DraftID         string         `json:"draft_id"`
	LeagueID        *string        `json:"league_id"`
	Type            string         `json:"type"`
	Status          string         `json:"status"`
	Sport           string         `json:"sport"`
	Season          string         `json:"season"`
	SeasonType      string         `json:"season_type"`
	Settings        map[string]any `json:"settings"`
	Metadata        map[string]any `json:"metadata"`
	DraftOrder      map[string]any `json:"draft_order"`
	SlotToRosterID  map[string]any `json:"slot_to_roster_id"`
	Creators        []string       `json:"creators"`
	StartTime       *time.Time     `json:"start_time"`
	LastPicked      *time.Time     `json:"last_picked"`
	LastMessageTime *time.Time     `json:"last_message_time"`
	LastMessageID   *string        `json:"last_message_id"`
	Created         *time.Time     `json:"created"`
}

// ParseDraft builds a Draft from one raw draft object.
func ParseDraft(m map[string]any) Draft {
	return Draft{
		DraftID:         flexStr(m["draft_id"]),
		LeagueID:        strPtr(m["league_id"]),
		Type:            flexStr(m["type"]),
		Status:          flexStr(m["status"]),
		Sport:           flexStr(m["sport"]),
		Season:          flexStr(m["season"]),
		SeasonType:      flexStr(m["season_type"]),
		Settings:        util.AsMap(m["settings"]),
		Metadata:        util.AsMap(m["metadata"]),
		DraftOrder:      util.AsMap(m["draft_order"]),
		SlotToRosterID:  util.AsMap(m["slot_to_roster_id"]),
		Creators:        util.ToStringSlice(m["creators"]),
		StartTime:       util.EpochMsToTimePtr(m["start_time"]),
		LastPicked:      util.EpochMsToTimePtr(m["last_picked"]),
		LastMessageTime: util.EpochMsToTimePtr(m["last_message_time"]),
		LastMessageID:   strPtr(m["last_message_id"]),
		Created:         util.EpochMsToTimePtr(m["created"]),
	}
}

// ParseDrafts builds a typed slice from a raw drafts payload.
func ParseDrafts(ms []map[string]any) []Draft {
	out := make([]Draft, len(ms))
	for i, m := range ms {
		out[i] = ParseDraft(m)
	}
	return out
}
