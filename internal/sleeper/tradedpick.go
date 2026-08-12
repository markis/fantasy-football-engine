package sleeper

// TradedPick models one entry from /league/<id>/traded_picks. Season/Round/
// RosterID/PreviousOwnerID/OwnerID are coerced to int via FlexInt (handles
// the string season "2019" and int roster ids), with 0 for absent/unparseable.
//
// RosterID is the original owner's roster id; PreviousOwnerID is the previous
// owner's roster id (in the trade); OwnerID is the current owner's roster id
// (all are roster ids, NOT user ids, per the Sleeper docs). JSON tags mirror
// Sleeper's snake_case keys for faithful MCP passthrough marshaling.
type TradedPick struct {
	Season          FlexInt `json:"season"`
	Round           FlexInt `json:"round"`
	RosterID        FlexInt `json:"roster_id"`
	PreviousOwnerID FlexInt `json:"previous_owner_id"`
	OwnerID         FlexInt `json:"owner_id"`
}
