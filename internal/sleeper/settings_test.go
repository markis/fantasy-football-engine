package sleeper

import (
	"encoding/json"
	"testing"
)

// Settings must round-trip byte-faithfully: unknown keys and original number
// formatting survive a marshal/unmarshal cycle, and typed fields are populated
// for known keys.
func TestSettingsRoundTrip(t *testing.T) {
	const in = `{
		"type": 2,
		"best_ball": 0,
		"waiver_budget": 100,
		"playoff_week_start": 14,
		"future_field_we_dont_model_yet": {"nested": [1, 2, 3]},
		"numeric_string_field": "42"
	}`
	var s Settings
	if err := json.Unmarshal([]byte(in), &s); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Typed fields populated for known keys.
	eqInt(t, "Type", int(s.Type), 2)
	eqInt(t, "BestBall", int(s.BestBall), 0)
	eqInt(t, "WaiverBudget", int(s.WaiverBudget), 100)
	eqInt(t, "PlayoffWeekStart", int(s.PlayoffWeekStart), 14)

	// Presence distinguishes absent from present-zero.
	if !s.Has("best_ball") {
		t.Errorf("Has(best_ball): want true (present, value 0)")
	}
	if s.Has("type") != true {
		t.Errorf("Has(type): want true")
	}
	if s.Has("nope") {
		t.Errorf("Has(nope): want false")
	}

	// Unknown keys survive into the re-marshaled output verbatim.
	out, err := json.Marshal(&s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var round map[string]any
	if err := json.Unmarshal(out, &round); err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	if v, ok := round["future_field_we_dont_model_yet"]; !ok || v == nil {
		t.Errorf("unknown key not preserved: %#v", round)
	}
	if v, ok := round["numeric_string_field"].(string); !ok || v != "42" {
		t.Errorf("numeric_string_field not preserved verbatim: got %#v", round["numeric_string_field"])
	}
	// Known values preserved.
	if v, ok := round["type"].(float64); !ok || v != 2 {
		t.Errorf("type not preserved: got %#v", round["type"])
	}
}

// A null settings object decodes to a nil *Settings on the League, and a nil
// *Settings marshals to JSON null.
func TestSettingsNullRoundTrip(t *testing.T) {
	const in = `{"settings": null, "league_id": "1"}`
	var l League
	if err := json.Unmarshal([]byte(in), &l); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if l.Settings != nil {
		t.Errorf("Settings: want nil for null, got %#v", l.Settings)
	}
	// A nil *Settings marshals to JSON null (encoding/json handles the nil
	// pointer without calling MarshalJSON).
	out, err := json.Marshal(l.Settings)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(out) != "null" {
		t.Errorf("nil Settings marshal: got %s, want null", out)
	}
}
