package sleeper

import (
	"fmt"
	"time"

	"ff-engine/internal/util"
)

// Transaction is a typed view of one entry from
// /league/<id>/transactions/<week>, produced by ParseTransaction. The client
// method stays map-typed to preserve the MCP passthrough; callers that need
// the full original blob (e.g. to store it in a raw jsonb column) keep the
// raw map and use this struct for field access.
//
// String fields (TxnID/Type/Status/Creator) coerce via flexStr: a present
// value yields its fmt.Sprint form and a null yields "". Adds/Drops are kept
// as map[string]any (player_id -> roster_id) because the roster_id values are
// coerced with toInt at use, and an ABSENT key must stay nil (-> SQL NULL for
// from_roster_id on a free-agent add) rather than 0. RosterIDs/ConsenterIDs
// and Leg are int. Created/StatusUpdated are *time.Time (nil -> SQL NULL).
// DraftPicks is a typed slice; TradeDraftPick.Season is a string and the id
// fields are int.
type Transaction struct {
	TxnID         string
	Type          string
	Status        string
	Creator       string
	Leg           int
	RosterIDs     []int
	ConsenterIDs  []int
	Adds          map[string]any
	Drops         map[string]any
	DraftPicks    []TradeDraftPick
	Created       *time.Time
	StatusUpdated *time.Time
}

// TradeDraftPick is one entry in a transaction's draft_picks array.
type TradeDraftPick struct {
	Season          string
	Round           int
	RosterID        int
	PreviousOwnerID int
	OwnerID         int
}

// ParseTransaction builds a Transaction from one raw transactions entry.
func ParseTransaction(m map[string]any) Transaction {
	return Transaction{
		TxnID:         flexStr(m["transaction_id"]),
		Type:          flexStr(m["type"]),
		Status:        flexStr(m["status"]),
		Creator:       flexStr(m["creator"]),
		Leg:           util.ToInt(m["leg"]),
		RosterIDs:     util.ToIntSlice(m["roster_ids"]),
		ConsenterIDs:  util.ToIntSlice(m["consenter_ids"]),
		Adds:          util.AsMap(m["adds"]),
		Drops:         util.AsMap(m["drops"]),
		DraftPicks:    parseTradeDraftPicks(m["draft_picks"]),
		Created:       util.EpochMsToTimePtr(m["created"]),
		StatusUpdated: util.EpochMsToTimePtr(m["status_updated"]),
	}
}

// ParseTransactions builds a typed slice from the raw transactions payload.
func ParseTransactions(ms []map[string]any) []Transaction {
	out := make([]Transaction, len(ms))
	for i, m := range ms {
		out[i] = ParseTransaction(m)
	}
	return out
}

// flexStr returns the string form of v: "" for nil, the string itself for a
// string, and fmt.Sprint(v) otherwise. It matches fmt.Sprint(v) for present
// values (so existing nilIfEmpty(fmt.Sprint(...)) sites behave the same) and
// returns "" (not "<nil>") for null.
func flexStr(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

// parseTradeDraftPicks coerces a decoded-JSON array of draft-pick objects.
func parseTradeDraftPicks(v any) []TradeDraftPick {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]TradeDraftPick, 0, len(arr))
	for _, item := range arr {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, TradeDraftPick{
			Season:          flexStr(m["season"]),
			Round:           util.ToInt(m["round"]),
			RosterID:        util.ToInt(m["roster_id"]),
			PreviousOwnerID: util.ToInt(m["previous_owner_id"]),
			OwnerID:         util.ToInt(m["owner_id"]),
		})
	}
	return out
}
