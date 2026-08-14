// Package sleeper provides a client for the Sleeper Fantasy Football REST API
package sleeper

// --- User / League / Roster (league-level objects) ---

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

// League models one league object from /league/<id> (league info) and
// /user/<id>/leagues/nfl/<season> (a user's leagues); both endpoints return
// the same shape. It is decoded directly via encoding/json.
//
// JSON tags mirror Sleeper's snake_case keys so the struct round-trips
// faithfully when marshaled for the MCP passthrough. Nullable text fields use
// *FlexString so a JSON null decodes to nil (and persists as SQL NULL rather
// than ""); present values keep their string form. LeagueID/TotalRosters use
// the lenient value types because Sleeper sends some scalars as strings in
// some records and numbers in others. Settings is the typed nested object
// (see settings.go); ScoringSettings stays map[string]any since only a few
// subkeys are read and the shape is loose.
type League struct {
	LeagueID         FlexString     `json:"league_id"`
	Name             *FlexString    `json:"name"`
	Season           *FlexString    `json:"season"`
	Status           *FlexString    `json:"status"`
	Sport            *FlexString    `json:"sport"`
	SeasonType       *FlexString    `json:"season_type"`
	TotalRosters     FlexInt        `json:"total_rosters"`
	RosterPositions  []string       `json:"roster_positions"`
	Settings         *Settings      `json:"settings"`
	ScoringSettings  map[string]any `json:"scoring_settings"`
	DraftID          *FlexString    `json:"draft_id"`
	Avatar           *FlexString    `json:"avatar"`
	PreviousLeagueID *FlexString    `json:"previous_league_id"`
}

// Roster models one entry from /league/<id>/rosters. It is decoded directly
// via encoding/json.
//
// Field semantics:
//   - RosterID is the integer roster id (FlexInt -> 0 if absent/unparseable).
//   - OwnerID/CoOwnerID are the owner/co-owner user ids as *FlexString: a
//     present string/number yields a non-nil pointer whose string form matches
//     the raw value; a JSON null yields nil (so callers can distinguish "no
//     owner" from an empty id).
//   - LeagueID is the league id (*FlexString; null -> nil).
//   - Players/Starters/Taxi/Reserve are coerced []string via FlexStringSlice
//     (nil/empty entries dropped).
//   - Settings/Metadata are the opaque nested objects (wins/losses/waiver
//     budget, team_name, ...); kept as map[string]any since only a few subkeys
//     are read and the shape is loose.
type Roster struct {
	RosterID  FlexInt         `json:"roster_id"`
	OwnerID   *FlexString     `json:"owner_id"`
	CoOwnerID *FlexString     `json:"co_owner_id"`
	LeagueID  *FlexString     `json:"league_id"`
	Players   FlexStringSlice `json:"players"`
	Starters  FlexStringSlice `json:"starters"`
	Taxi      FlexStringSlice `json:"taxi"`
	Reserve   FlexStringSlice `json:"reserve"`
	Settings  map[string]any  `json:"settings"`
	Metadata  map[string]any  `json:"metadata"`
}

// --- Matchup ---

// Matchup models one entry from /league/<id>/matchups/<week>. Each object is
// one team (NOT a head-to-head pairing); pair rows by equal MatchupID. The
// bench is deduced by removing Starters from Players. Decoded directly via
// encoding/json.
//
// RosterID is the integer roster id. MatchupID is *FlexInt (nil for a null
// matchup_id, e.g. a bye/unpaired entry). Points/CustomPoints are *FlexFloat
// (nil for null; CustomPoints is a commissioner override). Starters/Players
// are coerced []string of player/team-defense ids via FlexStringSlice. JSON
// tags mirror Sleeper's snake_case keys for faithful MCP marshaling.
type Matchup struct {
	RosterID     FlexInt         `json:"roster_id"`
	MatchupID    *FlexInt        `json:"matchup_id"`
	Points       *FlexFloat      `json:"points"`
	CustomPoints *FlexFloat      `json:"custom_points"`
	Starters     FlexStringSlice `json:"starters"`
	Players      FlexStringSlice `json:"players"`
}

// --- Transaction / TradedPick ---

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

// --- Draft / DraftPick ---

// Draft models one draft object from /league/<id>/drafts, /user/<id>/drafts/...,
// and /draft/<id> (all the same shape; the single-draft response additionally
// populates SlotToRosterID). Decoded directly via encoding/json.
//
// Nullable id/text fields use *FlexString (null -> nil). Type/Status/Sport/
// Season/SeasonType coerce via FlexString (null -> ""). StartTime/LastPicked/
// LastMessageTime/Created are epoch-ms -> *EpochMs (nil for null); when
// marshaled to JSON they serialize as RFC3339 (not the raw epoch-ms number).
// Settings/Metadata are the opaque nested objects (teams, rounds, slots_* /
// name, description, scoring_type). DraftOrder maps user_id -> draft slot and
// SlotToRosterID maps draft-slot string -> roster_id; both are kept as
// map[string]any since the keys are ids and the values are numbers whose
// exact int/float shape varies. Creators is a coerced []string of user ids
// via FlexStringSlice (nil for null). JSON tags mirror Sleeper's snake_case
// keys.
type Draft struct {
	DraftID         FlexString      `json:"draft_id"`
	LeagueID        *FlexString     `json:"league_id"`
	Type            FlexString      `json:"type"`
	Status          FlexString      `json:"status"`
	Sport           FlexString      `json:"sport"`
	Season          FlexString      `json:"season"`
	SeasonType      FlexString      `json:"season_type"`
	Settings        map[string]any  `json:"settings"`
	Metadata        map[string]any  `json:"metadata"`
	DraftOrder      map[string]any  `json:"draft_order"`
	SlotToRosterID  map[string]any  `json:"slot_to_roster_id"`
	Creators        FlexStringSlice `json:"creators"`
	StartTime       *EpochMs        `json:"start_time"`
	LastPicked      *EpochMs        `json:"last_picked"`
	LastMessageTime *EpochMs        `json:"last_message_time"`
	LastMessageID   *FlexString     `json:"last_message_id"`
	Created         *EpochMs        `json:"created"`
}

// DraftPick models one entry from /draft/<id>/picks. Decoded directly via
// encoding/json.
//
// PlayerID is *FlexString (nil for null; the selected player/team-defense id).
// PickedBy is the destination user_id (coerced to string; may be "" for
// leagues without a user in every slot). RosterID is the destination roster
// id coerced to int via FlexInt — Sleeper sends it as a string ("1") in some
// payloads and a number in others, and omits it entirely in some; absent or
// null yields 0. Round/DraftSlot/PickNo are int (FlexInt). IsKeeper is
// *FlexBool (nil for null). Metadata is the opaque point-in-time player
// snapshot (first_name, last_name, team, position, number, news_updated, ...).
// DraftID is the parent draft id. JSON tags mirror Sleeper's snake_case keys.
type DraftPick struct {
	DraftID   FlexString     `json:"draft_id"`
	PlayerID  *FlexString    `json:"player_id"`
	PickedBy  FlexString     `json:"picked_by"`
	RosterID  FlexInt        `json:"roster_id"`
	Round     FlexInt        `json:"round"`
	DraftSlot FlexInt        `json:"draft_slot"`
	PickNo    FlexInt        `json:"pick_no"`
	IsKeeper  *FlexBool      `json:"is_keeper"`
	Metadata  map[string]any `json:"metadata"`
}

// --- Player / TrendingPlayer ---

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

// TrendingPlayer models one entry from
// /players/nfl/trending/<add|drop>?lookback_hours=&limit=. PlayerID is the
// Sleeper player/team/defense id; Count is the number of adds or drops in the
// lookback window. JSON tags mirror Sleeper's snake_case keys. Decoded
// directly via encoding/json.
type TrendingPlayer struct {
	PlayerID FlexString `json:"player_id"`
	Count    FlexInt    `json:"count"`
}
