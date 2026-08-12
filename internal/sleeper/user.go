package sleeper

import "ff-engine/internal/util"

// User models one entry from /league/<id>/users (and the /user/<id> object),
// produced by ParseUser. It covers the fields the codebase consumes plus
// is_owner (commissioner flag) so the struct round-trips faithfully when
// marshaled for the MCP passthrough.
//
// The ID/display fields use *FlexString so a JSON null decodes to nil
// (distinct from an empty string) while a present string/number yields its
// fmt.Sprint string form — matching the prior fmt.Sprint(u["..."]) access.
// IsOwner is *bool (nil for null). Metadata is the opaque nested object
// (team_name, ...).
type User struct {
	UserID      *FlexString    `json:"user_id"`
	Username    *FlexString    `json:"username"`
	DisplayName *FlexString    `json:"display_name"`
	Avatar      *FlexString    `json:"avatar"`
	IsOwner     *bool          `json:"is_owner"`
	Metadata    map[string]any `json:"metadata"`
}

// ParseUser builds a User from one raw /league/<id>/users entry.
func ParseUser(m map[string]any) User {
	return User{
		UserID:      flexStrPtr(m["user_id"]),
		Username:    flexStrPtr(m["username"]),
		DisplayName: flexStrPtr(m["display_name"]),
		Avatar:      flexStrPtr(m["avatar"]),
		IsOwner:     boolPtr(m["is_owner"]),
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
