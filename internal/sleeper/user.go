package sleeper

// User models one entry from /league/<id>/users (and the /user/<id> object).
// It is decoded directly via encoding/json; the lenient field types handle
// Sleeper's null/string/number variance (see internal/sleeper/helpers.go).
//
// The ID/display fields use *FlexString so a JSON null decodes to nil
// (distinct from an empty string) while a present string/number yields its
// string form. IsOwner is *FlexBool (nil for null). Metadata is the opaque
// nested object (team_name, ...).
type User struct {
	UserID      *FlexString    `json:"user_id"`
	Username    *FlexString    `json:"username"`
	DisplayName *FlexString    `json:"display_name"`
	Avatar      *FlexString    `json:"avatar"`
	IsOwner     *FlexBool      `json:"is_owner"`
	Metadata    map[string]any `json:"metadata"`
}
