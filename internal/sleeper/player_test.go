package sleeper

import (
	"encoding/json"
	"testing"
)

// bradySample mirrors the /players/nfl example object from docs.sleeper.com,
// including its type quirks: rotoworld_id=8356 (int), espn_id="" (string),
// rotowire_id/yahoo_id=null, weight="220" (string), number/age/depth_chart_*
// as ints. It is the primary fixture proving Player decodes real API shapes.
const bradySample = `{
  "hashtag": "#TomBrady-NFL-NE-12",
  "depth_chart_position": 1,
  "status": "Active",
  "sport": "nfl",
  "fantasy_positions": ["QB"],
  "number": 12,
  "search_last_name": "brady",
  "injury_start_date": null,
  "weight": "220",
  "position": "QB",
  "practice_participation": null,
  "sportradar_id": "",
  "team": "NE",
  "last_name": "Brady",
  "college": "Michigan",
  "fantasy_data_id": 17836,
  "injury_status": null,
  "player_id": "3086",
  "height": "6'4\"",
  "search_full_name": "tombrady",
  "age": 40,
  "stats_id": "",
  "birth_country": "United States",
  "espn_id": "",
  "search_rank": 24,
  "first_name": "Tom",
  "depth_chart_order": 1,
  "years_exp": 14,
  "rotowire_id": null,
  "rotoworld_id": 8356,
  "search_first_name": "tom",
  "yahoo_id": null,
  "active": true,
  "news_updated": 1513007102037
}`

func checkStr(t *testing.T, name string, got *string, want string) {
	t.Helper()
	switch {
	case got == nil:
		t.Errorf("%s: nil, want %q", name, want)
	case *got != want:
		t.Errorf("%s: got %q, want %q", name, *got, want)
	}
}

func checkInt(t *testing.T, name string, got *int, want int) {
	t.Helper()
	switch {
	case got == nil:
		t.Errorf("%s: nil, want %d", name, want)
	case *got != want:
		t.Errorf("%s: got %d, want %d", name, *got, want)
	}
}

func TestPlayerDecodeDocsSample(t *testing.T) {
	var p Player
	if err := json.Unmarshal([]byte(bradySample), &p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	checkStr(t, "first_name", p.FirstName, "Tom")
	checkStr(t, "last_name", p.LastName, "Brady")
	checkStr(t, "position", p.Position, "QB")
	checkStr(t, "team", p.Team, "NE")
	checkStr(t, "weight", p.Weight, "220")
	checkStr(t, "height", p.Height, "6'4\"")
	checkInt(t, "age", p.Age, 40)
	checkInt(t, "number", p.Number, 12)
	checkInt(t, "depth_chart_order", p.DepthChartOrder, 1)
	// depth_chart_position is int here but the column is TEXT, so it stays any.
	if p.DepthChartPosition != float64(1) {
		t.Errorf("depth_chart_position: got %#v", p.DepthChartPosition)
	}
	// fantasy_positions decodes straight to []string.
	if len(p.FantasyPositions) != 1 || p.FantasyPositions[0] != "QB" {
		t.Errorf("fantasy_positions: got %#v", p.FantasyPositions)
	}
	// active bool and news_updated epoch ms pass through as any.
	if p.Active != true {
		t.Errorf("active: got %#v", p.Active)
	}
	if p.NewsUpdated != float64(1513007102037) {
		t.Errorf("news_updated: got %#v", p.NewsUpdated)
	}
}

func TestPlayerFlexStringIDs(t *testing.T) {
	var p Player
	if err := json.Unmarshal([]byte(bradySample), &p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	cases := []struct {
		name    string
		got     *FlexString
		want    string // expected string form when non-nil
		wantNil bool   // expect nil pointer
	}{
		{"espn_id", p.EspnID, "", false}, // "" string
		{"sportradar_id", p.SportradarID, "", false},
		{"stats_id", p.StatsID, "", false},
		{"rotoworld_id", p.RotoworldID, "8356", false}, // int -> "8356"
		{"rotowire_id", p.RotowireID, "", true},        // null -> nil
		{"yahoo_id", p.YahooID, "", true},              // null -> nil
	}
	for _, c := range cases {
		switch {
		case c.wantNil && c.got != nil:
			t.Errorf("%s: want nil, got %q", c.name, string(*c.got))
		case c.wantNil:
			// ok
		case c.got == nil:
			t.Errorf("%s: want %q, got nil", c.name, c.want)
		case string(*c.got) != c.want:
			t.Errorf("%s: want %q, got %q", c.name, c.want, string(*c.got))
		}
	}
	// Ptr() preserves nil vs non-nil and the string value.
	if p.RotoworldID.Ptr() == nil || *p.RotoworldID.Ptr() != "8356" {
		t.Fatalf("rotoworld_id.Ptr(): got %#v", p.RotoworldID.Ptr())
	}
	if p.RotowireID.Ptr() != nil {
		t.Fatalf("rotowire_id.Ptr(): want nil, got %#v", p.RotowireID.Ptr())
	}
}

func TestFlexStringDecodeVariants(t *testing.T) {
	cases := []struct {
		in      string
		want    string // expected FlexString value when non-nil
		wantNil bool   // expect nil pointer
	}{
		{`"abc"`, "abc", false},
		{`123`, "123", false},
		{`8356`, "8356", false},
		{`""`, "", false},
		{`null`, "", true},
	}
	for _, c := range cases {
		var f *FlexString
		if err := json.Unmarshal([]byte(c.in), &f); err != nil {
			t.Errorf("decode %s: %v", c.in, err)
			continue
		}
		switch {
		case c.wantNil && f != nil:
			t.Errorf("decode %s: want nil, got %q", c.in, string(*f))
		case c.wantNil:
			// ok
		case f == nil:
			t.Errorf("decode %s: want %q, got nil", c.in, c.want)
		case string(*f) != c.want:
			t.Errorf("decode %s: want %q, got %q", c.in, c.want, string(*f))
		}
	}
}

// TestPlayerNullNullableStrings ensures a JSON null on a nullable string
// field decodes to nil (so it persists as SQL NULL, not "").
func TestPlayerNullNullableStrings(t *testing.T) {
	const in = `{"injury_status": null, "first_name": null, "gsis_id": null}`
	var p Player
	if err := json.Unmarshal([]byte(in), &p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, f := range []*string{p.InjuryStatus, p.FirstName, p.GsisID} {
		if f != nil {
			t.Errorf("nullable field: want nil, got %q", *f)
		}
	}
}

// TestPlayerActiveCoercion covers the active field's documented polymorphism
// (bool/number/string/null) at the decode boundary; the ->bool coercion lives
// in sync.projectPlayer, exercised via the field values asserted here.
func TestPlayerActiveCoercion(t *testing.T) {
	cases := []struct {
		in   string
		want any
	}{
		{`true`, true},
		{`false`, false},
		{`1`, float64(1)},
		{`0`, float64(0)},
		{`"true"`, "true"},
		{`null`, nil},
	}
	for _, c := range cases {
		var p Player
		if err := json.Unmarshal([]byte(`{"active":`+c.in+`}`), &p); err != nil {
			t.Errorf("active %s: %v", c.in, err)
			continue
		}
		if p.Active != c.want {
			t.Errorf("active %s: want %#v, got %#v", c.in, c.want, p.Active)
		}
	}
}
