package sleeper

import (
	"encoding/json"
	"testing"
)

// rosterDocsSample mirrors the /league/<id>/rosters docs example, extended
// with the `taxi` and `metadata` fields the docs sample omits but the codebase
// reads, plus a null owner_id to exercise the nil path.
const rosterDocsSample = `[
  {
    "starters": ["2307", "2257", "4034", "147", "642", "4039", "515", "4149", "DET"],
    "settings": {
      "wins": 5, "waiver_position": 7, "waiver_budget_used": 0, "total_moves": 0,
      "ties": 0, "losses": 9, "fpts_decimal": 78, "fpts_against_decimal": 32,
      "fpts_against": 1670, "fpts": 1617
    },
    "roster_id": 1,
    "reserve": [],
    "players": ["1046", "138", "147", "2257", "2307", "DET"],
    "owner_id": "188815879448829952",
    "league_id": "206827432160788480",
    "taxi": ["9999"],
    "metadata": { "team_name": "Dezpacito" }
  },
  {
    "starters": [],
    "settings": { "wins": 0, "losses": 0, "ties": 0, "fpts": 0 },
    "roster_id": 2,
    "reserve": [],
    "players": [],
    "owner_id": null,
    "league_id": "206827432160788480",
    "taxi": [],
    "metadata": null
  }
]`

func eqInt(t *testing.T, name string, got, want int) {
	t.Helper()
	if got != want {
		t.Errorf("%s: got %d, want %d", name, got, want)
	}
}

func eqLen(t *testing.T, name string, got any, want int) {
	t.Helper()
	n := 0
	switch s := got.(type) {
	case []string:
		n = len(s)
	case FlexStringSlice:
		n = len(s)
	}
	if n != want {
		t.Errorf("%s len: got %d, want %d", name, n, want)
	}
}

func eqFlexStr(t *testing.T, name string, got *FlexString, want string) {
	t.Helper()
	switch {
	case got == nil:
		t.Errorf("%s: got nil, want %q", name, want)
	case string(*got) != want:
		t.Errorf("%s: got %q, want %q", name, string(*got), want)
	}
}

func eqMapKey(t *testing.T, name string, m map[string]any, key string, want any) {
	t.Helper()
	got, ok := m[key]
	if !ok {
		t.Errorf("%s[%s]: missing", name, key)
		return
	}
	if got != want {
		t.Errorf("%s[%s]: got %#v, want %#v", name, key, got, want)
	}
}

func TestParseRostersDocsSample(t *testing.T) {
	var ros []Roster
	if err := json.Unmarshal([]byte(rosterDocsSample), &ros); err != nil {
		t.Fatalf("decode: %v", err)
	}
	eqInt(t, "len", len(ros), 2)

	r0 := ros[0]
	eqInt(t, "RosterID", int(r0.RosterID), 1)
	eqFlexStr(t, "OwnerID", r0.OwnerID, "188815879448829952")
	eqLen(t, "Players", r0.Players, 6)
	if r0.Players[0] != "1046" {
		t.Errorf("Players[0]: got %q", r0.Players[0])
	}
	// taxi is read by the codebase but absent from the docs sample.
	eqLen(t, "Taxi", r0.Taxi, 1)
	if r0.Taxi[0] != "9999" {
		t.Errorf("Taxi[0]: got %q", r0.Taxi[0])
	}
	eqLen(t, "Starters", r0.Starters, 9)
	eqMapKey(t, "Settings", r0.Settings, "wins", float64(5))
	eqMapKey(t, "Metadata", r0.Metadata, "team_name", "Dezpacito")

	// Second roster: null owner_id -> nil OwnerID (not "" or "<nil>").
	r1 := ros[1]
	eqInt(t, "RosterID[1]", int(r1.RosterID), 2)
	if r1.OwnerID != nil {
		t.Errorf("OwnerID[1]: want nil for null, got %#v", r1.OwnerID)
	}
	if r1.Metadata != nil {
		t.Errorf("Metadata[1]: want nil for null, got %#v", r1.Metadata)
	}
}

func TestParseRosterNumericOwnerID(t *testing.T) {
	// If Sleeper ever sends owner_id as a number, it must coerce to the same
	// string form a present value would produce, preserving the existing
	// comparison sites. Real snowflake owner ids arrive as JSON strings; huge
	// numbers would be lossy, so this uses an exact integer literal.
	const in = `{"roster_id": 7, "owner_id": 42}`
	var r Roster
	if err := json.Unmarshal([]byte(in), &r); err != nil {
		t.Fatalf("decode: %v", err)
	}
	eqInt(t, "RosterID", int(r.RosterID), 7)
	eqFlexStr(t, "OwnerID", r.OwnerID, "42")
}
