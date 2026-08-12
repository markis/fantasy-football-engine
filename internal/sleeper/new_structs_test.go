package sleeper

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

// matchupDocsSample mirrors the /league/<id>/matchups/<week> docs example,
// extended with a null matchup_id (bye/unpaired) and null custom_points.
const matchupDocsSample = `[
  {
    "starters": ["421", "4035", "3242", "2133", "2449", "4531", "2257", "788", "PHI"],
    "roster_id": 1,
    "players": ["1352", "1387", "2118", "2133", "2182", "223", "2319", "2449", "3208", "4035", "421", "4881", "4892", "788", "CLE"],
    "matchup_id": 2,
    "points": 20.0,
    "custom_points": null
  },
  {
    "starters": [],
    "roster_id": 2,
    "players": [],
    "matchup_id": null,
    "points": null,
    "custom_points": null
  }
]`

func TestParseMatchupsDocsSample(t *testing.T) {
	var ms []Matchup
	if err := json.Unmarshal([]byte(matchupDocsSample), &ms); err != nil {
		t.Fatalf("decode: %v", err)
	}
	eqInt(t, "len", len(ms), 2)

	m0 := ms[0]
	eqInt(t, "RosterID", int(m0.RosterID), 1)
	if m0.MatchupID == nil || int(*m0.MatchupID) != 2 {
		t.Errorf("MatchupID: got %#v", m0.MatchupID)
	}
	if m0.Points == nil || float64(*m0.Points) != 20.0 {
		t.Errorf("Points: got %#v", m0.Points)
	}
	if m0.CustomPoints != nil {
		t.Errorf("CustomPoints: want nil for null, got %#v", m0.CustomPoints)
	}
	eqLen(t, "Starters", m0.Starters, 9)
	eqLen(t, "Players", m0.Players, 15)

	// Second: null matchup_id/points -> nil pointers.
	m1 := ms[1]
	eqInt(t, "RosterID[1]", int(m1.RosterID), 2)
	if m1.MatchupID != nil {
		t.Errorf("MatchupID[1]: want nil for null, got %#v", m1.MatchupID)
	}
	if m1.Points != nil {
		t.Errorf("Points[1]: want nil for null, got %#v", m1.Points)
	}
}

// draftDocsSample mirrors the /league/<id>/drafts and /draft/<id> docs
// examples. The single-draft shape adds slot_to_roster_id; the list shape
// omits it (both decode the same struct).
const draftDocsSample = `[
  {
    "type": "snake",
    "status": "complete",
    "start_time": 1515700800000,
    "sport": "nfl",
    "settings": { "teams": 6, "rounds": 15, "pick_timer": 120 },
    "season_type": "regular",
    "season": "2017",
    "metadata": { "scoring_type": "ppr", "name": "My Dynasty", "description": "" },
    "league_id": "257270637750382592",
    "last_picked": 1515700871182,
    "last_message_time": 1515700942674,
    "last_message_id": "257272036450111488",
    "draft_order": null,
    "draft_id": "257270643320426496",
    "creators": null,
    "created": 1515700610526
  }
]`

func TestParseDraftsDocsSample(t *testing.T) {
	var ds []Draft
	if err := json.Unmarshal([]byte(draftDocsSample), &ds); err != nil {
		t.Fatalf("decode: %v", err)
	}
	eqInt(t, "len", len(ds), 1)

	d := ds[0]
	eqStr(t, "DraftID", string(d.DraftID), "257270643320426496")
	eqStr(t, "Type", string(d.Type), "snake")
	eqStr(t, "Status", string(d.Status), "complete")
	eqStr(t, "Sport", string(d.Sport), "nfl")
	eqStr(t, "Season", string(d.Season), "2017")
	eqStr(t, "SeasonType", string(d.SeasonType), "regular")
	if d.LeagueID == nil || string(*d.LeagueID) != "257270637750382592" {
		t.Errorf("LeagueID: got %#v", d.LeagueID)
	}
	if d.LastMessageID == nil || string(*d.LastMessageID) != "257272036450111488" {
		t.Errorf("LastMessageID: got %#v", d.LastMessageID)
	}
	eqMapKey(t, "Settings", d.Settings, "teams", float64(6))
	eqMapKey(t, "Metadata", d.Metadata, "scoring_type", "ppr")
	if d.DraftOrder != nil {
		t.Errorf("DraftOrder: want nil for null, got %#v", d.DraftOrder)
	}
	if d.Creators != nil {
		t.Errorf("Creators: want nil for null, got %#v", d.Creators)
	}
	eqEpoch(t, "StartTime", d.StartTime, 1515700800000)
	eqEpoch(t, "LastPicked", d.LastPicked, 1515700871182)
	eqEpoch(t, "LastMessageTime", d.LastMessageTime, 1515700942674)
	eqEpoch(t, "Created", d.Created, 1515700610526)
}

// eqEpoch asserts an *EpochMs decodes from the given epoch-millis and is
// non-nil. (eqTime in transaction_test.go already covers *EpochMs; this is
// kept as a thin alias so this file's assertions read clearly.)
func eqEpoch(t *testing.T, name string, got *EpochMs, wantMs int64) {
	t.Helper()
	eqTime(t, name, got, wantMs)
}

// draftPicksDocsSample mirrors the /draft/<id>/picks docs example. The third
// pick omits roster_id/draft_slot to exercise the absent-field coercion.
const draftPicksDocsSample = `[
  {
    "player_id": "2391",
    "picked_by": "234343434",
    "roster_id": "1",
    "round": 5,
    "draft_slot": 5,
    "pick_no": 1,
    "metadata": { "first_name": "David", "last_name": "Johnson", "position": "RB", "team": "ARI" },
    "is_keeper": null,
    "draft_id": "257270643320426496"
  },
  {
    "player_id": "536",
    "picked_by": "667279356739584",
    "pick_no": 3,
    "metadata": { "first_name": "Antonio", "last_name": "Brown", "position": "WR", "team": "PIT" },
    "is_keeper": true,
    "draft_id": "257270643320426496"
  }
]`

func TestParseDraftPicksDocsSample(t *testing.T) {
	var ps []DraftPick
	if err := json.Unmarshal([]byte(draftPicksDocsSample), &ps); err != nil {
		t.Fatalf("decode: %v", err)
	}
	eqInt(t, "len", len(ps), 2)

	p0 := ps[0]
	eqStr(t, "DraftID", string(p0.DraftID), "257270643320426496")
	if p0.PlayerID == nil || string(*p0.PlayerID) != "2391" {
		t.Errorf("PlayerID: got %#v", p0.PlayerID)
	}
	eqStr(t, "PickedBy", string(p0.PickedBy), "234343434")
	eqInt(t, "RosterID", int(p0.RosterID), 1) // string "1" coerced to int
	eqInt(t, "Round", int(p0.Round), 5)
	eqInt(t, "DraftSlot", int(p0.DraftSlot), 5)
	eqInt(t, "PickNo", int(p0.PickNo), 1)
	if p0.IsKeeper != nil {
		t.Errorf("IsKeeper: want nil for null, got %#v", p0.IsKeeper)
	}
	eqMapKey(t, "Metadata", p0.Metadata, "position", "RB")

	// Second: roster_id/draft_slot absent -> 0; is_keeper true.
	p1 := ps[1]
	eqInt(t, "RosterID[1]", int(p1.RosterID), 0)
	eqInt(t, "DraftSlot[1]", int(p1.DraftSlot), 0)
	if p1.IsKeeper == nil || !bool(*p1.IsKeeper) {
		t.Errorf("IsKeeper[1]: got %#v", p1.IsKeeper)
	}
}

// trendingDocsSample mirrors the /players/nfl/trending/<type> docs example.
const trendingDocsSample = `[
  { "player_id": "1111", "count": 45 },
  { "player_id": "4046", "count": 12 }
]`

func TestParseTrendingPlayersDocsSample(t *testing.T) {
	var tp []TrendingPlayer
	if err := json.Unmarshal([]byte(trendingDocsSample), &tp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	eqInt(t, "len", len(tp), 2)
	eqStr(t, "PlayerID", string(tp[0].PlayerID), "1111")
	eqInt(t, "Count", int(tp[0].Count), 45)
	eqStr(t, "PlayerID[1]", string(tp[1].PlayerID), "4046")
	eqInt(t, "Count[1]", int(tp[1].Count), 12)
}

// TestTransactionMarshalRoundtrip ensures the Transaction struct (now used to
// archive the raw jsonb column) marshals with snake_case keys and preserves
// the adds/drops/waiver_budget fields the prior raw map carried, with
// timestamps serialized as RFC3339.
func TestTransactionMarshalRoundtrip(t *testing.T) {
	var txns []Transaction
	if err := json.Unmarshal([]byte(transactionDocsSample), &txns); err != nil {
		t.Fatalf("decode: %v", err)
	}
	out, err := json.Marshal(txns[0])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var check map[string]any
	if err := json.Unmarshal(out, &check); err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	eqStr(t, "roundtrip type", fmt.Sprint(check["type"]), "trade")
	eqStr(t, "roundtrip status", fmt.Sprint(check["status"]), "complete")
	if _, ok := check["waiver_budget"]; !ok {
		t.Errorf("waiver_budget missing from marshaled struct")
	}
	if _, ok := check["draft_picks"]; !ok {
		t.Errorf("draft_picks missing from marshaled struct")
	}
	if c, ok := check["created"].(string); !ok {
		t.Errorf("created should marshal as RFC3339 string, got %T", check["created"])
		_ = c
	}
	if _, err := time.Parse(time.RFC3339, fmt.Sprint(check["created"])); err != nil {
		t.Errorf("created not RFC3339: %v", err)
	}
}
