package sleeper

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// Settings models the nested "settings" object on a Sleeper league (see
// /league/<id> and /user/<id>/leagues/<sport>/<season>). Sleeper documents it
// as an opaque object and adds fields over time, so the well-known keys are
// exposed as typed fields for convenient reading while the full decoded object
// is retained for faithful JSON round-tripping: the leaguemates sync archives
// the whole settings object into a jsonb column, so unknown keys and original
// number formatting must survive a marshal/unmarshal cycle.
//
// Typed fields are read-only views populated from the decoded object during
// UnmarshalJSON; mutating them does not affect MarshalJSON, which re-emits the
// decoded bytes verbatim. All known fields use FlexInt — Sleeper sends them as
// JSON numbers, but FlexInt also accepts numeric strings/null defensively,
// matching the rest of the package. A null "settings" object decodes to a nil
// *Settings on the League (encoding/json sets the pointer to nil without
// calling UnmarshalJSON), so callers nil-check before reading typed fields.
type Settings struct {
	Type                     FlexInt `json:"type"`
	BestBall                 FlexInt `json:"best_ball"`
	WaiverBudget             FlexInt `json:"waiver_budget"`
	WaiverBidMin             FlexInt `json:"waiver_bid_min"`
	WaiverClearDays          FlexInt `json:"waiver_clear_days"`
	WaiverDayOfWeek          FlexInt `json:"waiver_day_of_week"`
	WaiverType               FlexInt `json:"waiver_type"`
	DailyWaivers             FlexInt `json:"daily_waivers"`
	DailyWaiversHour         FlexInt `json:"daily_waivers_hour"`
	DisableAdds              FlexInt `json:"disable_adds"`
	NumTeams                 FlexInt `json:"num_teams"`
	MaxKeepers               FlexInt `json:"max_keepers"`
	DraftRounds              FlexInt `json:"draft_rounds"`
	PickTrading              FlexInt `json:"pick_trading"`
	PlayoffWeekStart         FlexInt `json:"playoff_week_start"`
	PlayoffTeams             FlexInt `json:"playoff_teams"`
	PlayoffType              FlexInt `json:"playoff_type"`
	PlayoffRoundType         FlexInt `json:"playoff_round_type"`
	PlayoffSeedType          FlexInt `json:"playoff_seed_type"`
	ReserveSlots             FlexInt `json:"reserve_slots"`
	ReserveAllowCov          FlexInt `json:"reserve_allow_cov"`
	ReserveAllowDnr          FlexInt `json:"reserve_allow_dnr"`
	ReserveAllowDoubtful     FlexInt `json:"reserve_allow_doubtful"`
	ReserveAllowNa           FlexInt `json:"reserve_allow_na"`
	ReserveAllowOut          FlexInt `json:"reserve_allow_out"`
	ReserveAllowSus          FlexInt `json:"reserve_allow_sus"`
	TaxiSlots                FlexInt `json:"taxi_slots"`
	TaxiYears                FlexInt `json:"taxi_years"`
	TaxiAllowVets            FlexInt `json:"taxi_allow_vets"`
	TaxiDeadline             FlexInt `json:"taxi_deadline"`
	TradeDeadline            FlexInt `json:"trade_deadline"`
	TradeReviewDays          FlexInt `json:"trade_review_days"`
	BenchLock                FlexInt `json:"bench_lock"`
	CapacityOverride         FlexInt `json:"capacity_override"`
	CommissionerDirectInvite FlexInt `json:"commissioner_direct_invite"`
	LeagueAverageMatch       FlexInt `json:"league_average_match"`
	OffseasonAdds            FlexInt `json:"offseason_adds"`
	Leg                      FlexInt `json:"leg"`

	// raw holds the full decoded object keyed by JSON name, verbatim, so
	// MarshalJSON can re-emit exactly what Sleeper sent — including keys not
	// modeled above and the original number formatting. It is unexported and
	// excluded from the json tag map below.
	raw map[string]json.RawMessage
}

// settingsJSONKeys maps each modeled JSON key to its struct field index, built
// once at init so UnmarshalJSON can populate typed fields without a hand-
// maintained switch (and without drift between the struct and the decoder).
var settingsJSONKeys = func() map[string]int {
	t := reflect.TypeFor[Settings]()
	m := make(map[string]int, t.NumField())
	for i := range t.NumField() {
		f := t.Field(i)
		name := strings.Split(f.Tag.Get("json"), ",")[0]
		if name == "" || name == "-" {
			continue
		}
		m[name] = i
	}
	return m
}()

// UnmarshalJSON decodes the settings object into the typed fields above and
// retains the full object in raw for faithful re-marshaling. A JSON null
// leaves *s as the zero value; on the League the field is *Settings, so null
// yields a nil pointer (encoding/json sets the pointer to nil without calling
// UnmarshalJSON).
func (s *Settings) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || string(b) == jsonNull {
		return nil
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return fmt.Errorf("decode league settings: %w", err)
	}
	s.raw = raw
	rv := reflect.ValueOf(s).Elem()
	for key, val := range raw {
		idx, ok := settingsJSONKeys[key]
		if !ok {
			continue // unknown key stays in raw for round-trip
		}
		fv := rv.Field(idx)
		if !fv.CanSet() {
			continue
		}
		// FlexInt.UnmarshalJSON never returns an error (it coerces to 0), and
		// val is already valid JSON from the decode above, so a failure here
		// just leaves the field at its zero value.
		if err := json.Unmarshal(val, fv.Addr().Interface()); err != nil {
			continue
		}
	}
	return nil
}

// MarshalJSON re-emits the decoded object verbatim. Because raw holds the
// original bytes, the round-trip is byte-faithful (unknown keys and number
// formatting preserved). A zero-value Settings (never decoded) marshals as {}.
// The receiver is a pointer to avoid copying the 300+ byte struct.
func (s *Settings) MarshalJSON() ([]byte, error) {
	if s == nil || len(s.raw) == 0 {
		return []byte("{}"), nil
	}
	out, err := json.Marshal(s.raw)
	if err != nil {
		return nil, fmt.Errorf("marshal league settings: %w", err)
	}
	return out, nil
}

// Has reports whether key was present in the decoded settings object, so
// callers can distinguish "absent" from a present zero (e.g. waiver_budget 0
// vs. a league that didn't set one). Always false for a nil Settings.
func (s *Settings) Has(key string) bool {
	if s == nil || s.raw == nil {
		return false
	}
	_, ok := s.raw[key]
	return ok
}
