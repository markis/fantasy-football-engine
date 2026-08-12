package sleeper

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// Player models one entry in the /players/nfl dump (the value object; the
// player_id is the map key returned by FetchPlayerDump). Fields are typed
// where the Sleeper API is consistent, and use *FlexString for the external
// ID fields that arrive as numbers in some records and strings in others.
// Nullable fields are pointers so a JSON null decodes to nil (and persists as
// SQL NULL) rather than a zero value.
//
// The three any-typed fields are deliberately untyped because the API is
// genuinely polymorphic for them and the ingest coerces at project time:
//   - Active: bool, number, string, or null -> bool (see sync.projectPlayer)
//   - DepthChartPosition: int or string; the player column is TEXT and pgx
//     coerces either form, so the raw value is passed through.
//   - NewsUpdated: epoch-ms number or null; converted to a timestamp.
//
// JSON tags are snake_case to match the Sleeper API; the package is exempted
// from the repo's camelCase tagliatelle rule (see .golangci.yml exclusions).
type Player struct {
	FirstName             *string     `json:"first_name"`
	LastName              *string     `json:"last_name"`
	FullName              *string     `json:"full_name"`
	SearchFullName        *string     `json:"search_full_name"`
	Position              *string     `json:"position"`
	FantasyPositions      []string    `json:"fantasy_positions"`
	Team                  *string     `json:"team"`
	TeamAbbr              *string     `json:"team_abbr"`
	Status                *string     `json:"status"`
	Active                any         `json:"active"`
	InjuryStatus          *string     `json:"injury_status"`
	InjuryBodyPart        *string     `json:"injury_body_part"`
	InjuryNotes           *string     `json:"injury_notes"`
	InjuryStartDate       *string     `json:"injury_start_date"`
	Age                   *int        `json:"age"`
	YearsExp              *int        `json:"years_exp"`
	BirthDate             *string     `json:"birth_date"`
	Height                *string     `json:"height"`
	Weight                *string     `json:"weight"`
	College               *string     `json:"college"`
	Number                *int        `json:"number"`
	DepthChartPosition    any         `json:"depth_chart_position"`
	DepthChartOrder       *int        `json:"depth_chart_order"`
	PracticeParticipation *string     `json:"practice_participation"`
	PracticeDescription   *string     `json:"practice_description"`
	GsisID                *string     `json:"gsis_id"`
	EspnID                *FlexString `json:"espn_id"`
	RotowireID            *FlexString `json:"rotowire_id"`
	RotoworldID           *FlexString `json:"rotoworld_id"`
	YahooID               *FlexString `json:"yahoo_id"`
	SportradarID          *FlexString `json:"sportradar_id"`
	StatsID               *FlexString `json:"stats_id"`
	NewsUpdated           any         `json:"news_updated"`
}

// errFlexStringDecode is returned by FlexString.UnmarshalJSON when the JSON
// value is neither a string nor a number.
var errFlexStringDecode = errors.New("FlexString: cannot decode JSON value")

// FlexString is a string that unmarshals from a JSON string OR number,
// coercing the number to its decimal string form. A null produces a nil
// pointer when used as *FlexString (encoding/json sets the pointer to nil
// without calling UnmarshalJSON). It exists because Sleeper encodes several
// external ID fields as numbers in some player records and strings in others
// (e.g. rotoworld_id arrives as 8356, espn_id as "").
type FlexString string

// UnmarshalJSON accepts a JSON string or number, coercing to FlexString.
func (f *FlexString) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 {
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		*f = FlexString(s)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(b, &n); err == nil {
		*f = FlexString(string(n))
		return nil
	}
	return fmt.Errorf("%w: %s", errFlexStringDecode, b)
}

// Ptr returns a *string copy of f, or nil if f is nil. Used to feed the
// external ID columns (TEXT, nullable) when persisting.
func (f *FlexString) Ptr() *string {
	if f == nil {
		return nil
	}
	s := string(*f)
	return &s
}
