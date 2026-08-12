package sleeper

// Transaction models one entry from /league/<id>/transactions/<week>. It is
// decoded directly via encoding/json.
//
// String fields (TxnID/Type/Status/Creator) coerce via FlexString: a present
// value yields its string form and a null yields "". Adds/Drops are kept as
// map[string]any (player_id -> roster_id) because the roster_id values are
// coerced with util.ToInt at use, and an ABSENT key must stay nil (-> SQL NULL
// for from_roster_id on a free-agent add) rather than 0. RosterIDs/
// ConsenterIDs and Leg are int (FlexInt/FlexIntSlice). Created/StatusUpdated
// are *EpochMs (nil -> SQL NULL); when marshaled to JSON they serialize as
// RFC3339 (not the raw epoch-ms number). DraftPicks is a typed slice.
// WaiverBudget is the opaque array of {sender,receiver,amount} objects, kept
// as any. Settings/Metadata are the opaque nested objects.
type Transaction struct {
	TxnID         FlexString       `json:"transaction_id"`
	Type          FlexString       `json:"type"`
	Status        FlexString       `json:"status"`
	Creator       FlexString       `json:"creator"`
	Leg           FlexInt          `json:"leg"`
	RosterIDs     FlexIntSlice     `json:"roster_ids"`
	ConsenterIDs  FlexIntSlice     `json:"consenter_ids"`
	Adds          map[string]any   `json:"adds"`
	Drops         map[string]any   `json:"drops"`
	DraftPicks    []TradeDraftPick `json:"draft_picks"`
	WaiverBudget  any              `json:"waiver_budget"`
	Settings      map[string]any   `json:"settings"`
	Metadata      map[string]any   `json:"metadata"`
	Created       *EpochMs         `json:"created"`
	StatusUpdated *EpochMs         `json:"status_updated"`
}

// TradeDraftPick is one entry in a transaction's draft_picks array.
type TradeDraftPick struct {
	Season          FlexString `json:"season"`
	Round           FlexInt    `json:"round"`
	RosterID        FlexInt    `json:"roster_id"`
	PreviousOwnerID FlexInt    `json:"previous_owner_id"`
	OwnerID         FlexInt    `json:"owner_id"`
}
