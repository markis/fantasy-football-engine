package query

import (
	"strings"
	"testing"
)

func TestRankingColumns(t *testing.T) {
	tests := []struct {
		name           string
		source         string
		superflex      bool
		wantValueCol   string
		wantOverallCol string
		wantPosRankCol string
	}{
		{
			name:           "standard - any source",
			source:         "Dynasty Daddy",
			superflex:      false,
			wantValueCol:   "r.trade_value",
			wantOverallCol: "r.overall_rank",
			wantPosRankCol: "r.position_rank",
		},
		{
			name:           "superflex - Dynasty Daddy uses sf columns",
			source:         "Dynasty Daddy",
			superflex:      true,
			wantValueCol:   "r.sf_trade_value",
			wantOverallCol: "r.sf_overall_rank",
			wantPosRankCol: "r.sf_position_rank",
		},
		{
			name:           "superflex - KeepTradeCut uses sf columns",
			source:         "KeepTradeCut",
			superflex:      true,
			wantValueCol:   "r.sf_trade_value",
			wantOverallCol: "r.sf_overall_rank",
			wantPosRankCol: "r.sf_position_rank",
		},
		{
			name:           "superflex - FantasyCalc keeps trade_value, COALESCE ranks",
			source:         srcFantasyCalc,
			superflex:      true,
			wantValueCol:   "r.trade_value",
			wantOverallCol: "COALESCE(r.sf_overall_rank, r.overall_rank)",
			wantPosRankCol: "COALESCE(r.sf_position_rank, r.position_rank)",
		},
		{
			name:           "standard - FantasyCalc",
			source:         srcFantasyCalc,
			superflex:      false,
			wantValueCol:   "r.trade_value",
			wantOverallCol: "r.overall_rank",
			wantPosRankCol: "r.position_rank",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotValue, gotOverall, gotPosRank := rankingColumns(tt.source, tt.superflex)
			if gotValue != tt.wantValueCol {
				t.Errorf("valueCol = %q, want %q", gotValue, tt.wantValueCol)
			}
			if gotOverall != tt.wantOverallCol {
				t.Errorf("overallCol = %q, want %q", gotOverall, tt.wantOverallCol)
			}
			if gotPosRank != tt.wantPosRankCol {
				t.Errorf("posRankCol = %q, want %q", gotPosRank, tt.wantPosRankCol)
			}
		})
	}
}

// TestRankingColumnsFantasyCalcSuperflexHasSfData verifies that the
// FantasyCalc superflex path uses COALESCE so rows with sf_overall_rank
// get the sf value while rows without it fall back to overall_rank.
// This is a structural assertion on the SQL expression, not a DB test.
func TestRankingColumnsFantasyCalcSuperflexHasSfData(t *testing.T) {
	_, overallCol, _ := rankingColumns(srcFantasyCalc, true)
	if !strings.Contains(overallCol, "COALESCE") {
		t.Fatalf("FantasyCalc superflex overallCol should use COALESCE, got %q", overallCol)
	}
	if !strings.Contains(overallCol, "sf_overall_rank") {
		t.Fatalf("FantasyCalc superflex overallCol should reference sf_overall_rank, got %q", overallCol)
	}
	if !strings.Contains(overallCol, "overall_rank") {
		t.Fatalf("FantasyCalc superflex overallCol should fall back to overall_rank, got %q", overallCol)
	}
}
