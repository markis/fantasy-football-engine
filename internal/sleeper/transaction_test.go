package sleeper

import (
	"encoding/json"
	"testing"
	"time"
)

// transactionDocsSample mirrors the /league/<id>/transactions docs trade
// example, trimmed to the fields the codebase consumes (adds/drops null on
// this trade; the struct must tolerate their absence as nil maps).
const transactionDocsSample = `[
  {
    "type": "trade",
    "transaction_id": "434852362033561600",
    "status_updated": 1558039402803,
    "status": "complete",
    "settings": null,
    "roster_ids": [2, 1],
    "metadata": null,
    "leg": 1,
    "drops": null,
    "draft_picks": [
      {
        "season": "2019", "round": 5, "roster_id": 1,
        "previous_owner_id": 1, "owner_id": 2
      },
      {
        "season": "2019", "round": 3, "roster_id": 2,
        "previous_owner_id": 2, "owner_id": 1
      }
    ],
    "creator": "160000000000000000",
    "created": 1558039391576,
    "consenter_ids": [2, 1],
    "adds": null,
    "waiver_budget": [ { "sender": 2, "receiver": 3, "amount": 55 } ]
  },
  {
    "type": "free_agent",
    "transaction_id": "434890120798142464",
    "status": "complete",
    "leg": 1,
    "roster_ids": [1],
    "consenter_ids": [1],
    "adds": { "2315": 1 },
    "drops": { "1736": 1 },
    "draft_picks": [],
    "creator": null,
    "created": 1558048393967,
    "status_updated": 1558048393967
  }
]`

func eqStr(t *testing.T, name, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("%s: got %q, want %q", name, got, want)
	}
}

func eqTime(t *testing.T, name string, got *EpochMs, wantMs int64) {
	t.Helper()
	if got == nil {
		t.Errorf("%s: got nil, want %d", name, wantMs)
		return
	}
	if !time.Time(*got).Equal(time.UnixMilli(wantMs).UTC()) {
		t.Errorf("%s: got %v, want %v", name, time.Time(*got), time.UnixMilli(wantMs).UTC())
	}
}

func TestParseTransactionsDocsSample(t *testing.T) {
	var txns []Transaction
	if err := json.Unmarshal([]byte(transactionDocsSample), &txns); err != nil {
		t.Fatalf("decode: %v", err)
	}
	eqInt(t, "len", len(txns), 2)

	t0 := txns[0]
	eqStr(t, "TxnID", string(t0.TxnID), "434852362033561600")
	eqStr(t, "Type", string(t0.Type), "trade")
	eqStr(t, "Status", string(t0.Status), "complete")
	eqStr(t, "Creator", string(t0.Creator), "160000000000000000")
	eqInt(t, "Leg", int(t0.Leg), 1)
	eqInt(t, "RosterIDs[0]", t0.RosterIDs[0], 2)
	eqInt(t, "RosterIDs[1]", t0.RosterIDs[1], 1)
	eqInt(t, "ConsenterIDs[1]", t0.ConsenterIDs[1], 1)
	// adds/drops null -> nil maps (not empty maps).
	if t0.Adds != nil {
		t.Errorf("Adds: want nil for null, got %#v", t0.Adds)
	}
	if t0.Drops != nil {
		t.Errorf("Drops: want nil for null, got %#v", t0.Drops)
	}
	eqInt(t, "DraftPicks len", len(t0.DraftPicks), 2)
	eqStr(t, "DraftPicks[0].Season", string(t0.DraftPicks[0].Season), "2019")
	eqInt(t, "DraftPicks[0].Round", int(t0.DraftPicks[0].Round), 5)
	eqInt(t, "DraftPicks[0].RosterID", int(t0.DraftPicks[0].RosterID), 1)
	eqInt(t, "DraftPicks[0].OwnerID", int(t0.DraftPicks[0].OwnerID), 2)
	eqTime(t, "Created", t0.Created, 1558039391576)
	eqTime(t, "StatusUpdated", t0.StatusUpdated, 1558039402803)

	// Second txn: adds/drops present -> map[string]any; creator null -> "".
	t1 := txns[1]
	if v, ok := t1.Adds["2315"]; !ok || v != float64(1) {
		t.Errorf("Adds[2315]: got %#v, want float64(1)", t1.Adds)
	}
	if v, ok := t1.Drops["1736"]; !ok || v != float64(1) {
		t.Errorf("Drops[1736]: got %#v, want float64(1)", t1.Drops)
	}
	eqStr(t, "Creator[1]", string(t1.Creator), "")
	eqInt(t, "DraftPicks[1] len", len(t1.DraftPicks), 0)
}

// TestEpochMsRoundtrip guards the client cache path: get() re-marshals a
// decoded struct to store it, then re-decodes on cache hit. EpochMs.MarshalJSON
// emits RFC3339, so UnmarshalJSON must accept RFC3339 (in addition to epoch-ms)
// or cached timestamps would be lost on the second access.
func TestEpochMsRoundtrip(t *testing.T) {
	var txn Transaction
	if err := json.Unmarshal([]byte(`{"created": 1558039391576}`), &txn); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := time.UnixMilli(1558039391576).UTC()
	if txn.Created == nil || !time.Time(*txn.Created).Equal(want) {
		t.Fatalf("decode: got %#v", txn.Created)
	}
	raw, err := json.Marshal(txn)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var again Transaction
	if err := json.Unmarshal(raw, &again); err != nil {
		t.Fatalf("re-decode: %v", err)
	}
	if again.Created == nil || !time.Time(*again.Created).Equal(want) {
		t.Errorf("roundtrip: got %#v, want %v", again.Created, want)
	}
}
