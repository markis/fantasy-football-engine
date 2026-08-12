package sleeper

import (
	"encoding/json"
	"testing"
)

const userDocsSample = `[
  {
    "user_id": "12345678",
    "username": "sleeperuser",
    "display_name": "SleeperUser",
    "avatar": "cc12ec49965eb7856f84d71cf85306af",
    "metadata": { "team_name": "Dezpacito" },
    "is_owner": true
  },
  {
    "user_id": "87654321",
    "username": null,
    "display_name": null,
    "avatar": null,
    "metadata": null
  }
]`

func TestParseUsersDocsSample(t *testing.T) {
	var users []User
	if err := json.Unmarshal([]byte(userDocsSample), &users); err != nil {
		t.Fatalf("decode: %v", err)
	}
	eqInt(t, "len", len(users), 2)

	u0 := users[0]
	eqFlexStr(t, "user_id", u0.UserID, "12345678")
	eqFlexStr(t, "username", u0.Username, "sleeperuser")
	eqFlexStr(t, "display_name", u0.DisplayName, "SleeperUser")
	eqFlexStr(t, "avatar", u0.Avatar, "cc12ec49965eb7856f84d71cf85306af")
	eqMapKey(t, "metadata", u0.Metadata, "team_name", "Dezpacito")

	// Second user: null string fields -> nil pointers; null metadata -> nil.
	u1 := users[1]
	eqFlexStr(t, "user_id[1]", u1.UserID, "87654321")
	if u1.Username != nil {
		t.Errorf("username[1]: want nil for null, got %#v", u1.Username)
	}
	if u1.DisplayName != nil {
		t.Errorf("display_name[1]: want nil for null, got %#v", u1.DisplayName)
	}
	if u1.Avatar != nil {
		t.Errorf("avatar[1]: want nil for null, got %#v", u1.Avatar)
	}
	if u1.Metadata != nil {
		t.Errorf("metadata[1]: want nil for null, got %#v", u1.Metadata)
	}
}

const tradedPicksDocsSample = `[
  { "season": "2019", "round": 5, "roster_id": 1, "previous_owner_id": 1, "owner_id": 2 },
  { "season": "2020", "round": 3, "roster_id": 2, "previous_owner_id": 2, "owner_id": 1 }
]`

func TestParseTradedPicksDocsSample(t *testing.T) {
	var picks []TradedPick
	if err := json.Unmarshal([]byte(tradedPicksDocsSample), &picks); err != nil {
		t.Fatalf("decode: %v", err)
	}
	eqInt(t, "len", len(picks), 2)

	p0 := picks[0]
	eqInt(t, "Season", int(p0.Season), 2019) // string "2019" coerced to int
	eqInt(t, "Round", int(p0.Round), 5)
	eqInt(t, "RosterID", int(p0.RosterID), 1)
	eqInt(t, "OwnerID", int(p0.OwnerID), 2)

	p1 := picks[1]
	eqInt(t, "Season[1]", int(p1.Season), 2020)
	eqInt(t, "RosterID[1]", int(p1.RosterID), 2)
	eqInt(t, "OwnerID[1]", int(p1.OwnerID), 1)
}

// TestParseTradedPickNull asserts null pick fields coerce to 0 (the FlexInt
// default), so downstream int usage never panics.
func TestParseTradedPickNull(t *testing.T) {
	const in = `{"season": null, "round": null, "roster_id": null, "owner_id": null}`
	var p TradedPick
	if err := json.Unmarshal([]byte(in), &p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	eqInt(t, "Season", int(p.Season), 0)
	eqInt(t, "Round", int(p.Round), 0)
	eqInt(t, "RosterID", int(p.RosterID), 0)
	eqInt(t, "OwnerID", int(p.OwnerID), 0)
}
