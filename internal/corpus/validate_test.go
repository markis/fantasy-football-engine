package corpus

import "testing"

// TestSchemaCompilation guards against a schema file failing to parse/compile
// silently (Validate would then skip enforcement for that file type).
func TestSchemaCompilation(t *testing.T) {
	for _, rule := range schemaRules {
		if _, ok := compiledSchemas[rule.schemaFile]; !ok {
			t.Errorf("schema %s failed to compile or was not found", rule.schemaFile)
		}
	}
}

// TestTeamStateSample locks in the shape renderTeam must produce for
// team/leagues/*/team-state.json to satisfy team-state.schema.json.
func TestTeamStateSample(t *testing.T) {
	sch := compiledSchemas["team-state.schema.json"]
	if sch == nil {
		t.Fatal("team-state schema not compiled")
	}
	sample := map[string]any{
		"as_of": "2026-08-10T00:00:00Z",
		"league": map[string]any{
			"name": "Test League", "platform": "Sleeper", "league_id": "123",
			"format": "dynasty", "teams": 12, "scoring_summary": "1 QB, PPR=1, TEP=none",
			"roster_summary": "Starters: QB,RB,RB,WR,WR,TE,FLEX; Bench: 9",
			"trade_deadline": nil, "notes": "",
		},
		"team": map[string]any{
			"name": "My Team", "competitive_mode": "unknown", "target_contention_window": nil,
			"roster": []any{
				map[string]any{
					"sleeper_player_id": "123", "full_name": "Test Player", "position": "RB",
					"nfl_team": "SF", "slot": "BN", "trade_value": 100, "age": 25, "injury_status": nil,
				},
			},
			"taxi_squad": []any{}, "injured_reserve": []any{},
			"future_picks": []any{
				map[string]any{"season": 2027, "round": 1, "original_team": "3", "current_owner": "3"},
			},
			"faab_remaining": 80, "active_trade_discussions": []any{},
			"roster_constraints": []any{}, "current_priorities": []any{},
		},
		"data_freshness": map[string]any{
			"roster_updated_at": "2026-08-10T00:00:00Z", "league_updated_at": "2026-08-10T00:00:00Z",
			"transactions_updated_at": "2026-08-10T00:00:00Z",
		},
	}
	if err := sch.Validate(sample); err != nil {
		t.Errorf("sample team-state failed validation: %v", err)
	}
}

// TestPlayerSignalSample locks in the shape renderDatasets must produce for
// datasets/player-signals.jsonl records, including the source_url field.
func TestPlayerSignalSample(t *testing.T) {
	sch := compiledSchemas["player-signal.schema.json"]
	sample := map[string]any{
		"signal_id": "signal:0123456789abcdef", "player_id": "nfl:123", "signal_type": "injury",
		"value": map[string]any{"status": "Questionable"}, "source": "Sleeper",
		"source_url": nil, "observed_at": "2026-08-10T00:00:00Z", "published_at": nil,
		"confidence": "high", "status": "current", "evidence_record_id": nil,
	}
	if err := sch.Validate(sample); err != nil {
		t.Errorf("sample player-signal failed validation: %v", err)
	}
}

func TestValuationSample(t *testing.T) {
	sch := compiledSchemas["valuation.schema.json"]
	sample := map[string]any{
		"valuation_id": "valuation:0123456789abcdef", "player_id": "nfl:123", "source": "FantasyCalc",
		"valuation_type": "trade-value", "format_context": "12t-1QB-PPR", "value": 5000,
		"observed_at": "2026-08-10T00:00:00Z", "source_url": nil, "confidence": "medium",
		"evidence_record_id": nil,
	}
	if err := sch.Validate(sample); err != nil {
		t.Errorf("sample valuation failed validation: %v", err)
	}
}

// TestCorpusManifestSample locks in the shape renderManifest must produce,
// including a full 64-hex-char team_state_hash.
func TestCorpusManifestSample(t *testing.T) {
	sch := compiledSchemas["corpus-manifest.schema.json"]
	sample := map[string]any{
		"generated_at": "2026-08-10T00:00:00Z", "schema_version": 1,
		"change_log_cursor": nil, "as_of_window_hours": 336,
		"counts": map[string]any{"evidence": 1},
		"files": []any{
			map[string]any{"path": "a.json", "sha256": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"[:64], "bytes": 10},
		},
		"leagues":         []any{map[string]any{"league_id": "1", "name": "L"}},
		"team_state_hash": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"[:64],
	}
	if err := sch.Validate(sample); err != nil {
		t.Errorf("sample manifest failed validation: %v", err)
	}
}
