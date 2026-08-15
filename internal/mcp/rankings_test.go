package mcp

import (
	"testing"
)

func TestResolveRankingsSourceMarket(t *testing.T) {
	tests := []struct {
		name       string
		args       map[string]any
		wantSource string
		wantMarket int
		wantOK     bool
	}{
		{
			name:       "no args defaults to Dynasty Daddy 14",
			args:       map[string]any{},
			wantSource: "Dynasty Daddy",
			wantMarket: 14,
			wantOK:     true,
		},
		{
			name:       "source only - KeepTradeCut derives market 0",
			args:       map[string]any{"source": "KeepTradeCut"},
			wantSource: "KeepTradeCut",
			wantMarket: 0,
			wantOK:     true,
		},
		{
			name:       "source only - Dynasty Daddy derives market 14",
			args:       map[string]any{"source": "Dynasty Daddy"},
			wantSource: "Dynasty Daddy",
			wantMarket: 14,
			wantOK:     true,
		},
		{
			name:       "source only - FantasyCalc derives market 1",
			args:       map[string]any{"source": "FantasyCalc"},
			wantSource: "FantasyCalc",
			wantMarket: 1,
			wantOK:     true,
		},
		{
			name:       "source only - FantasyPros ECR derives market 1",
			args:       map[string]any{"source": "FantasyPros ECR"},
			wantSource: "FantasyPros ECR",
			wantMarket: 1,
			wantOK:     true,
		},
		{
			name:       "market only - 0 derives KeepTradeCut",
			args:       map[string]any{"market": 0},
			wantSource: "KeepTradeCut",
			wantMarket: 0,
			wantOK:     true,
		},
		{
			name:       "market only - 14 derives Dynasty Daddy",
			args:       map[string]any{"market": 14},
			wantSource: "Dynasty Daddy",
			wantMarket: 14,
			wantOK:     true,
		},
		{
			name:       "market only - 1 derives FantasyCalc",
			args:       map[string]any{"market": 1},
			wantSource: "FantasyCalc",
			wantMarket: 1,
			wantOK:     true,
		},
		{
			name:       "market only - 2 derives FantasyCalc",
			args:       map[string]any{"market": 2},
			wantSource: "FantasyCalc",
			wantMarket: 2,
			wantOK:     true,
		},
		{
			name:       "market only - 3 derives FantasyCalc",
			args:       map[string]any{"market": 3},
			wantSource: "FantasyCalc",
			wantMarket: 3,
			wantOK:     true,
		},
		{
			name:       "both given - valid combo",
			args:       map[string]any{"source": "FantasyCalc", "market": 2},
			wantSource: "FantasyCalc",
			wantMarket: 2,
			wantOK:     true,
		},
		{
			name:       "both given - KeepTradeCut market 0",
			args:       map[string]any{"source": "KeepTradeCut", "market": 0},
			wantSource: "KeepTradeCut",
			wantMarket: 0,
			wantOK:     true,
		},
		{
			name:       "unknown source",
			args:       map[string]any{"source": "UnknownSource"},
			wantSource: "",
			wantMarket: 0,
			wantOK:     false,
		},
		{
			name:       "unknown market",
			args:       map[string]any{"market": 99},
			wantSource: "",
			wantMarket: 0,
			wantOK:     false,
		},
		{
			name:       "source nil treated as absent",
			args:       map[string]any{"source": nil, "market": 14},
			wantSource: "Dynasty Daddy",
			wantMarket: 14,
			wantOK:     true,
		},
		{
			name:       "market nil treated as absent",
			args:       map[string]any{"source": "KeepTradeCut", "market": nil},
			wantSource: "KeepTradeCut",
			wantMarket: 0,
			wantOK:     true,
		},
		{
			name:       "market as float64 (JSON decode)",
			args:       map[string]any{"market": float64(0)},
			wantSource: "KeepTradeCut",
			wantMarket: 0,
			wantOK:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotSource, gotMarket, gotOK := resolveRankingsSourceMarket(tt.args)
			if gotOK != tt.wantOK {
				t.Fatalf("ok = %v, want %v (source=%q market=%d)", gotOK, tt.wantOK, gotSource, gotMarket)
			}
			if !tt.wantOK {
				return
			}
			if gotSource != tt.wantSource {
				t.Errorf("source = %q, want %q", gotSource, tt.wantSource)
			}
			if gotMarket != tt.wantMarket {
				t.Errorf("market = %d, want %d", gotMarket, tt.wantMarket)
			}
		})
	}
}
