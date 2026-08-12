package sync

import (
	"testing"
	"time"

	"ff-engine/internal/sleeper"
)

// boolAt asserts row[i] is the bool want.
func boolAt(t *testing.T, row []any, i int, want bool) {
	t.Helper()
	if got, ok := row[i].(bool); !ok || got != want {
		t.Fatalf("row[%d]: got %#v, want %v", i, row[i], want)
	}
}

func intPtrAt(t *testing.T, row []any, i, want int) {
	t.Helper()
	if got, ok := row[i].(*int); !ok || got == nil || *got != want {
		t.Fatalf("row[%d]: got %#v, want %d", i, row[i], want)
	}
}

func strPtrAt(t *testing.T, row []any, i int, want string) {
	t.Helper()
	if got, ok := row[i].(*string); !ok || got == nil || *got != want {
		t.Fatalf("row[%d]: got %#v, want %q", i, row[i], want)
	}
}

// nullAt asserts row[i] is null-equivalent: untyped nil or a typed-nil
// pointer (pgx encodes both to SQL NULL).
func nullAt(t *testing.T, row []any, i int) {
	t.Helper()
	switch v := row[i].(type) {
	case nil:
		// untyped nil
	case *string:
		if v != nil {
			t.Fatalf("row[%d]: want null-equivalent, got %q", i, *v)
		}
	default:
		t.Fatalf("row[%d]: unexpected type %T", i, v)
	}
}

// TestProjectPlayerCoercion verifies the ingest pipeline: a decoded
// sleeper.Player with mixed/edge-case field types projects to the exact
// []any the player upsert expects (active -> bool, external IDs -> *string,
// news_updated -> time.Time, depth_chart_position passed through raw).
func TestProjectPlayerCoercion(t *testing.T) {
	epoch := float64(1700000000000) // 2023-11-14T22:13:20Z
	wantTS := time.UnixMilli(int64(epoch)).UTC()
	p := &sleeper.Player{
		FirstName:          new("Tom"),
		LastName:           new("Brady"),
		Position:           new("QB"),
		Team:               new("NE"),
		Weight:             new("220"),
		FantasyPositions:   []string{"QB"},
		Age:                new(40),
		Number:             new(12),
		DepthChartOrder:    new(1),
		DepthChartPosition: float64(1),                      // int in API; passed through raw
		Active:             "true",                          // string form, coerced to bool
		EspnID:             new(sleeper.FlexString("8356")), // arrived as a number
		RotoworldID:        new(sleeper.FlexString("8356")),
		NewsUpdated:        epoch,
	}

	row := projectPlayer("3086", p)

	if got, ok := row[0].(string); !ok || got != "3086" {
		t.Fatalf("sleeper_player_id: got %#v", row[0])
	}
	boolAt(t, row, 10, true) // active string "true" -> bool true
	intPtrAt(t, row, 15, 40) // age *int
	if row[22] != float64(1) {
		t.Fatalf("depth_chart_position: got %#v", row[22]) // raw passthrough
	}
	strPtrAt(t, row, 27, "8356") // espn_id number -> *string "8356"
	nullAt(t, row, 28)           // rotowire_id null -> *string(nil) -> SQL NULL
	if got, ok := row[33].(time.Time); !ok || !got.Equal(wantTS) {
		t.Fatalf("news_updated: got %#v, want %v", row[33], wantTS)
	}
}

// TestProjectPlayerActiveVariants covers every active-coercion branch.
func TestProjectPlayerActiveVariants(t *testing.T) {
	cases := []struct {
		active any
		want   bool
	}{
		{true, true},
		{false, false},
		{float64(1), true},
		{float64(0), false},
		{"true", true},
		{"True", true},
		{"false", false},
		{nil, false},
	}
	for _, c := range cases {
		row := projectPlayer("1", &sleeper.Player{Active: c.active})
		boolAt(t, row, 10, c.want)
	}
}
