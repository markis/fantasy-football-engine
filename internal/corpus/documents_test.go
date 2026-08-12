package corpus

import (
	"encoding/json"
	"testing"
)

// assertSchemaRoundTrip marshals v to JSON, decodes it back to a generic
// value, and validates it against the named compiled schema. It is the drift
// guard for the typed output structs: if a struct's shape diverges from its
// schema (missing/extra field, wrong null, bad enum), the schema validator
// catches it here.
func assertSchemaRoundTrip(t *testing.T, schemaFile string, v any) {
	t.Helper()
	sch := compiledSchemas[schemaFile]
	if sch == nil {
		t.Fatalf("schema %s not compiled", schemaFile)
	}
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal %s: %v", schemaFile, err)
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal %s: %v", schemaFile, err)
	}
	if err := sch.Validate(decoded); err != nil {
		t.Fatalf("%s struct does not validate against schema: %v\nJSON: %s", schemaFile, err, raw)
	}
}

func TestValuationStructRoundTrip(t *testing.T) {
	v := Valuation{
		ValuationID:      "valuation:0123456789abcdef", // matches ^valuation:[0-9a-f]{16,}$
		PlayerID:         "nfl:1234",                   // matches ^nfl:
		Source:           "Dynasty Daddy",
		ValuationType:    "trade-value", // enum
		FormatContext:    "Dynasty Daddy composite",
		Value:            100,
		ObservedAt:       "2026-08-10T00:00:00Z",
		SourceURL:        nil,
		Confidence:       "medium", // enum
		EvidenceRecordID: nil,
	}
	assertSchemaRoundTrip(t, "valuation.schema.json", v)
}

func TestPlayerSignalStructRoundTrip(t *testing.T) {
	sig := PlayerSignal{
		SignalID:   "signal:0123456789abcdef", // matches ^signal:[0-9a-f]{16,}$
		PlayerID:   "nfl:1234",
		SignalType: "injury", // enum
		Value: PlayerSignalInjuryValue{
			Status:   "Out",
			BodyPart: "knee",
			Notes:    "expected to miss 2 weeks",
		},
		Source:           "Sleeper",
		SourceURL:        nil, // nullable uri
		ObservedAt:       "2026-08-10T00:00:00Z",
		PublishedAt:      nil,       // nullable, optional
		Confidence:       "high",    // enum
		Status:           "current", // enum
		EvidenceRecordID: nil,
	}
	assertSchemaRoundTrip(t, "player-signal.schema.json", sig)
}

func TestManifestStructRoundTrip(t *testing.T) {
	sha := "0000000000000000000000000000000000000000000000000000000000000000" // 64 hex
	cursor := "change:0123456789abcdef"
	m := Manifest{
		GeneratedAt:     "2026-08-10T00:00:00Z",
		SchemaVersion:   1,
		ChangeLogCursor: &cursor, // required + nullable
		AsOfWindowHours: 168,
		Counts:          map[string]int{"evidence": 2, "valuation": 5},
		Files: []ManifestFile{
			{Path: "corpus-manifest.json", SHA256: sha, Bytes: 1024},
			{Path: "datasets/valuations.jsonl", SHA256: sha, Bytes: 512},
		},
		Leagues: []ManifestLeague{{LeagueID: "289646328504385536", Name: "Test League"}},
		// TeamStateHash omitted (optional).
	}
	assertSchemaRoundTrip(t, "corpus-manifest.schema.json", m)
}

func TestTeamStateStructRoundTrip(t *testing.T) {
	nilStr := (*string)(nil)
	nilInt := (*int)(nil)
	ts := TeamState{
		AsOf: "2026-08-10T00:00:00Z",
		League: TeamStateLeague{
			Name: "Test League", Platform: "Sleeper", LeagueID: "123", Format: "dynasty",
			Teams: 12, ScoringSummary: "1 QB", RosterSummary: "Starters: QB",
			TradeDeadline: nil, Notes: "",
		},
		Team: TeamStateTeam{
			Name: "My Team", CompetitiveMode: "unknown", TargetContentionWindow: nil,
			Roster: []RosterSlot{{
				SleeperPlayerID: "123", FullName: nilStr, Position: nilStr, NflTeam: nilStr,
				Slot: "BN", TradeValue: nilInt, Age: nilInt, InjuryStatus: nilStr,
			}},
			TaxiSquad:              []RosterSlot{},
			InjuredReserve:         []RosterSlot{},
			FuturePicks:            []FuturePick{{Season: 2027, Round: 1, OriginalTeam: "3", CurrentOwner: "3"}},
			FaabRemaining:          100,
			ActiveTradeDiscussions: []string{},
			RosterConstraints:      []string{},
			CurrentPriorities:      []string{},
		},
		DataFreshness: TeamStateDataFreshness{
			RosterUpdatedAt:       "2026-08-10T00:00:00Z",
			LeagueUpdatedAt:       "2026-08-10T00:00:00Z",
			TransactionsUpdatedAt: "2026-08-10T00:00:00Z",
		},
	}
	assertSchemaRoundTrip(t, "team-state.schema.json", ts)
}

func TestChangeLogEntryStructRoundTrip(t *testing.T) {
	entry := ChangeLogEntry{
		ChangeID:    "change:0123456789abcdef", // matches ^change:[0-9a-f]{16,}$
		Timestamp:   "2026-08-10T00:00:00Z",
		Operation:   "added",    // enum
		EntityType:  "evidence", // enum
		EntityID:    "sha256:0000000000000000000000000000000000000000000000000000000000000000",
		Paths:       []string{"evidence/records/0000000000000000000000000000000000000000000000000000000000000000.json"},
		ContentHash: "sha256:0000000000000000000000000000000000000000000000000000000000000000", // matches ^sha256:[0-9a-f]{64}$
		Summary:     "added evidence record sha256:0000000000000000000000000000000000000000000000000000000000000000",
	}
	assertSchemaRoundTrip(t, "change-log.schema.json", entry)
}
