package sleeper

import (
	"encoding/json"
	"fmt"
	"strconv"

	"ff-engine/internal/util"
)

// Roster is a typed view of one entry from /league/<id>/rosters, produced by
// ParseRoster. It covers exactly the fields the codebase consumes; the client
// (Client.GetLeagueRosters) still returns the raw map so the MCP passthrough
// keeps every field Sleeper sends — this struct is the opt-in typed path for
// internal callers.
//
// Field semantics:
//   - RosterID is the integer roster id (0 if absent/unparseable).
//   - OwnerID is the owner's user id as *FlexString: a present string/number
//     yields a non-nil pointer whose string form matches fmt.Sprint of the
//     raw value; a JSON null yields nil (so callers can distinguish "no
//     owner" from an empty id).
//   - Players/Starters/Taxi/Reserve are coerced []string via util.ToStringSlice
//     (nil/empty entries dropped).
//   - Settings/Metadata are the opaque nested objects (wins/losses/waiver
//     budget, team_name, ...); kept as map[string]any since only a few subkeys
//     are read and the shape is loose.
type Roster struct {
	RosterID int
	OwnerID  *FlexString
	Players  []string
	Starters []string
	Taxi     []string
	Reserve  []string
	Settings map[string]any
	Metadata map[string]any
}

// ParseRoster builds a Roster from one raw /league/<id>/rosters entry.
func ParseRoster(m map[string]any) Roster {
	return Roster{
		RosterID: util.ToInt(m["roster_id"]),
		OwnerID:  flexStrPtr(m["owner_id"]),
		Players:  util.ToStringSlice(m["players"]),
		Starters: util.ToStringSlice(m["starters"]),
		Taxi:     util.ToStringSlice(m["taxi"]),
		Reserve:  util.ToStringSlice(m["reserve"]),
		Settings: util.AsMap(m["settings"]),
		Metadata: util.AsMap(m["metadata"]),
	}
}

// ParseRosters builds a typed slice from the raw rosters payload.
func ParseRosters(ms []map[string]any) []Roster {
	out := make([]Roster, len(ms))
	for i, m := range ms {
		out[i] = ParseRoster(m)
	}
	return out
}

// flexStrPtr coerces a raw JSON value to *FlexString the way fmt.Sprint would
// for a present value (string stays itself; a number becomes its decimal
// string form), and returns nil for a JSON null. This preserves the existing
// fmt.Sprint(r["owner_id"]) semantics at call sites that compared or stored
// the string, while letting nil mean "no owner".
func flexStrPtr(v any) *FlexString {
	if v == nil {
		return nil
	}
	switch x := v.(type) {
	case string:
		s := FlexString(x)
		return &s
	case json.Number:
		s := FlexString(string(x))
		return &s
	case float64:
		s := FlexString(strconv.FormatFloat(x, 'f', -1, 64))
		return &s
	case int:
		s := FlexString(strconv.Itoa(x))
		return &s
	default:
		s := FlexString(fmt.Sprint(v))
		return &s
	}
}
