package sleeper

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

// FlexString and its UnmarshalJSON/Ptr live in helpers.go alongside the other
// lenient types; Player is decoded directly via encoding/json.
