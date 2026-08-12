package corpus

// This file holds typed structs for the corpus output documents
// (datasets/*.jsonl and the JSON files under team/ and the manifest). Each
// struct matches its JSON schema; documents_test.go round-trips every struct
// (marshal -> validate against the schema via compiledSchemas) so any drift
// between the struct and the schema fails the test, replacing the
// hand-maintenance risk of typing the output by hand.
//
// Nullable schema fields use *T so null marshals to JSON null (not the zero
// value), matching the prior map[string]any{"...": nil} output. Schemas use
// snake_case property names, so json tags are snake_case; these structs are
// corpus-internal output shapes, not the repo's camelCase API JSON.

// Valuation is one record in datasets/valuations.jsonl, matching
// valuation.schema.json. The schema's value field is type-less
// (source-specific: int trade-value, int rank, string adp, ...), so it stays
// any.
type Valuation struct {
	ValuationID      string  `json:"valuation_id"`
	PlayerID         string  `json:"player_id"`
	Source           string  `json:"source"`
	ValuationType    string  `json:"valuation_type"`
	FormatContext    string  `json:"format_context"`
	Value            any     `json:"value"`
	ObservedAt       string  `json:"observed_at"`
	SourceURL        *string `json:"source_url"`
	Confidence       string  `json:"confidence"`
	EvidenceRecordID *string `json:"evidence_record_id"`
}

// PlayerSignal is one record in datasets/player-signals.jsonl, matching
// player-signal.schema.json. The schema's value field is type-less (structured
// or scalar, varying by signal_type); PlayerSignalInjuryValue is the injury
// shape written by writeSignalsJSONL (the only signal type currently emitted).
type PlayerSignal struct {
	SignalID         string  `json:"signal_id"`
	PlayerID         string  `json:"player_id"`
	SignalType       string  `json:"signal_type"`
	Value            any     `json:"value"`
	Source           string  `json:"source"`
	SourceURL        *string `json:"source_url"`
	ObservedAt       string  `json:"observed_at"`
	PublishedAt      *string `json:"published_at"`
	Confidence       string  `json:"confidence"`
	Status           string  `json:"status"`
	EvidenceRecordID *string `json:"evidence_record_id"`
}

// PlayerSignalInjuryValue is the value object for an injury signal.
type PlayerSignalInjuryValue struct {
	Status   string `json:"status"`
	BodyPart string `json:"body_part"`
	Notes    string `json:"notes"`
}

// Manifest is the corpus-manifest.json document, matching
// corpus-manifest.schema.json. ChangeLogCursor is required+nullable (nil ->
// JSON null); TeamStateHash is optional (omitted when absent).
type Manifest struct {
	GeneratedAt     string           `json:"generated_at"`
	SchemaVersion   int              `json:"schema_version"`
	ChangeLogCursor *string          `json:"change_log_cursor"`
	AsOfWindowHours int              `json:"as_of_window_hours"`
	Counts          map[string]int   `json:"counts"`
	Files           []ManifestFile   `json:"files"`
	Leagues         []ManifestLeague `json:"leagues"`
	TeamStateHash   *string          `json:"team_state_hash,omitempty"`
}

// ManifestFile is one entry in the manifest files array.
type ManifestFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

// ManifestLeague is one entry in the manifest leagues array.
type ManifestLeague struct {
	LeagueID string `json:"league_id"`
	Name     string `json:"name"`
}

// TeamState is the team-state.json document, matching team-state.schema.json.
type TeamState struct {
	AsOf          string                 `json:"as_of"`
	League        TeamStateLeague        `json:"league"`
	Team          TeamStateTeam          `json:"team"`
	DataFreshness TeamStateDataFreshness `json:"data_freshness"`
}

// TeamStateLeague is the league block of team-state.json.
type TeamStateLeague struct {
	Name           string  `json:"name"`
	Platform       string  `json:"platform"`
	LeagueID       string  `json:"league_id"`
	Format         string  `json:"format"`
	Teams          int     `json:"teams"`
	ScoringSummary string  `json:"scoring_summary"`
	RosterSummary  string  `json:"roster_summary"`
	TradeDeadline  *string `json:"trade_deadline"` // required, nullable
	Notes          string  `json:"notes"`
}

// TeamStateTeam is the team block of team-state.json.
type TeamStateTeam struct {
	Name                   string       `json:"name"`
	CompetitiveMode        string       `json:"competitive_mode"`         // enum
	TargetContentionWindow *string      `json:"target_contention_window"` // required, nullable
	Roster                 []RosterSlot `json:"roster"`
	TaxiSquad              []RosterSlot `json:"taxi_squad"`
	InjuredReserve         []RosterSlot `json:"injured_reserve"`
	FuturePicks            []FuturePick `json:"future_picks"`
	FaabRemaining          int          `json:"faab_remaining"`
	ActiveTradeDiscussions []string     `json:"active_trade_discussions"`
	RosterConstraints      []string     `json:"roster_constraints"`
	CurrentPriorities      []string     `json:"current_priorities"`
}

// TeamStateDataFreshness is the data_freshness block of team-state.json.
type TeamStateDataFreshness struct {
	RosterUpdatedAt       string `json:"roster_updated_at"`
	LeagueUpdatedAt       string `json:"league_updated_at"`
	TransactionsUpdatedAt string `json:"transactions_updated_at"`
}

// RosterSlot is one entry in a team-state roster/taxi_squad/injured_reserve
// array, matching team-state.schema.json #/$defs/rosterSlot. All fields are
// always present (nullable pointers marshal null when nil), mirroring the
// prior map[string]any output which always included every key.
type RosterSlot struct {
	SleeperPlayerID string  `json:"sleeper_player_id"`
	FullName        *string `json:"full_name"`
	Position        *string `json:"position"`
	NflTeam         *string `json:"nfl_team"`
	Slot            string  `json:"slot"`
	TradeValue      *int    `json:"trade_value"`
	Age             *int    `json:"age"`
	InjuryStatus    *string `json:"injury_status"`
}

// FuturePick is one entry in a team-state future_picks array, matching
// team-state.schema.json #/$defs/futurePick. Acquired is optional and not
// currently set by the builder (left nil -> omitted).
type FuturePick struct {
	Season       int    `json:"season"`
	Round        int    `json:"round"`
	OriginalTeam string `json:"original_team"`
	CurrentOwner string `json:"current_owner"`
	Acquired     *bool  `json:"acquired,omitempty"`
}
