package sleeper

import "ff-engine/internal/util"

// User is a typed view of one entry from /league/<id>/users (and the
// /user/<id> object), produced by ParseUser. The client methods stay
// map-typed to preserve the MCP passthrough; this is the opt-in typed path.
//
// The ID/display fields use *FlexString so a JSON null decodes to nil
// (distinct from an empty string) while a present string/number yields its
// fmt.Sprint string form — matching the prior fmt.Sprint(u["..."]) access.
// Metadata is the opaque nested object (team_name, ...).
type User struct {
	UserID      *FlexString
	Username    *FlexString
	DisplayName *FlexString
	Avatar      *FlexString
	Metadata    map[string]any
}

// ParseUser builds a User from one raw /league/<id>/users entry.
func ParseUser(m map[string]any) User {
	return User{
		UserID:      flexStrPtr(m["user_id"]),
		Username:    flexStrPtr(m["username"]),
		DisplayName: flexStrPtr(m["display_name"]),
		Avatar:      flexStrPtr(m["avatar"]),
		Metadata:    util.AsMap(m["metadata"]),
	}
}

// ParseUsers builds a typed slice from the raw league-users payload.
func ParseUsers(ms []map[string]any) []User {
	out := make([]User, len(ms))
	for i, m := range ms {
		out[i] = ParseUser(m)
	}
	return out
}
