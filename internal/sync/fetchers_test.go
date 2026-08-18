package sync

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ff-engine/internal/config"
	"ff-engine/internal/models"
)

// --- pure helpers ---

func TestToInt(t *testing.T) {
	f := 3.0
	i := 5
	if got := toInt(nil); got != nil {
		t.Fatalf("nil must stay nil: %v", got)
	}
	if got := toInt(f); got == nil || *got != 3 {
		t.Fatalf("float64: %v", got)
	}
	if got := toInt(i); got == nil || *got != 5 {
		t.Fatalf("int: %v", got)
	}
	if got := toInt("42"); got == nil || *got != 42 {
		t.Fatalf("string: %v", got)
	}
	if got := toInt("nope"); got != nil {
		t.Fatalf("unparseable string must be nil: %v", got)
	}
	if got := toInt(json.Number("7")); got == nil || *got != 7 {
		t.Fatalf("json.Number: %v", got)
	}
	if got := toInt(true); got != nil {
		t.Fatalf("unsupported type must be nil: %v", got)
	}
}

func TestGetStrFromMap(t *testing.T) {
	if got := getStrFromMap(nil, "k"); got != nil {
		t.Fatalf("nil map: %v", got)
	}
	if got := getStrFromMap(map[string]any{}, "missing"); got != nil {
		t.Fatalf("missing key: %v", got)
	}
	if got := getStrFromMap(map[string]any{"k": nil}, "k"); got != nil {
		t.Fatalf("nil value: %v", got)
	}
	got := getStrFromMap(map[string]any{"k": "WR"}, "k")
	if got == nil || *got != "WR" {
		t.Fatalf("string value: %v", got)
	}
}

func TestGetRankingStr(t *testing.T) {
	if got := getRankingStr(map[string]any{}, "k"); got != "" {
		t.Fatalf("missing: %q", got)
	}
	if got := getRankingStr(map[string]any{"k": nil}, "k"); got != "" {
		t.Fatalf("nil: %q", got)
	}
	if got := getRankingStr(map[string]any{"k": 123}, "k"); got != "123" {
		t.Fatalf("numeric: %q", got)
	}
}

func TestGetNested(t *testing.T) {
	m := map[string]any{"ok": map[string]any{"a": 1}, "bad": "str"}
	if v, ok := getNested(m, "ok"); !ok || v == nil {
		t.Fatalf("nested map must resolve: %v %v", v, ok)
	}
	if v, ok := getNested(m, "bad"); ok || v != nil {
		t.Fatalf("non-map must not resolve: %v %v", v, ok)
	}
	if _, ok := getNested(m, "missing"); ok {
		t.Fatal("missing key must not resolve")
	}
}

func TestBuildUpsertParts(t *testing.T) {
	up := buildUpsertParts([]string{"a", "b"}, 4)
	if up.colNames != "a, b" {
		t.Fatalf("colNames: %q", up.colNames)
	}
	if up.placeholders != "$4, $5" {
		t.Fatalf("placeholders: %q", up.placeholders)
	}
	if up.updates != "a = EXCLUDED.a, b = EXCLUDED.b" {
		t.Fatalf("updates: %q", up.updates)
	}
}

// --- httptest fetch paths ---

// stubJSONServer serves body with status for every request and records the
// last request it saw.
func stubJSONServer(t *testing.T, status int, body string, seen **http.Request) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if seen != nil {
			*seen = r
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
}

func TestFetchRankingsData(t *testing.T) {
	s := NewRankingsSyncer(nil)
	var req *http.Request
	srv := stubJSONServer(t, http.StatusOK, `[{"name_id":"x"},{"name_id":"y"}]`, &req)
	defer srv.Close()
	ddURL = srv.URL
	defer func() { ddURL = "https://dynasty-daddy.com/api/v1/player/all/today" }()

	data, err := s.fetchRankingsData(t.Context(), 2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(data) != 2 {
		t.Fatalf("want 2 records, got %d", len(data))
	}
	if got := req.URL.Query().Get("market"); got != "2" {
		t.Fatalf("market param missing: %q", got)
	}

	// Non-200 yields the sentinel-wrapped error.
	srv2 := stubJSONServer(t, http.StatusInternalServerError, "boom", nil)
	defer srv2.Close()
	ddURL = srv2.URL
	if _, err := s.fetchRankingsData(t.Context(), 1); !errors.Is(err, errRankingsHTTP) {
		t.Fatalf("want errRankingsHTTP, got %v", err)
	}

	// Malformed JSON yields a decode error.
	srv3 := stubJSONServer(t, http.StatusOK, `{`, nil)
	defer srv3.Close()
	ddURL = srv3.URL
	if _, err := s.fetchRankingsData(t.Context(), 1); err == nil || !strings.Contains(err.Error(), "decode rankings") {
		t.Fatalf("want decode error, got %v", err)
	}
}

func TestFetchFantasyCalcData(t *testing.T) {
	s := NewFantasyCalcSyncer(nil)
	var req *http.Request
	srv := stubJSONServer(t, http.StatusOK, `[]`, &req)
	defer srv.Close()
	fcBase = srv.URL
	defer func() { fcBase = "https://api.fantasycalc.com/values/current" }()

	combo := models.FormatCombos[0]
	data, err := s.fetchFantasyCalcData(t.Context(), combo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(data) != 0 {
		t.Fatalf("want empty slice, got %d", len(data))
	}
	q := req.URL.Query()
	if q.Get("isDynasty") != "true" || q.Get("numTeams") == "" || q.Get("ppr") == "" {
		t.Fatalf("format params missing: %s", req.URL.RawQuery)
	}

	srv2 := stubJSONServer(t, http.StatusForbidden, "denied", nil)
	defer srv2.Close()
	fcBase = srv2.URL
	if _, err := s.fetchFantasyCalcData(t.Context(), combo); !errors.Is(err, errFantasyCalcHTTP) {
		t.Fatalf("want errFantasyCalcHTTP, got %v", err)
	}
}

// fpTestConfig builds a config whose FP secret resolves from a temp secrets
// dir, so fetchFPJSON can run against a stub server.
func fpTestConfig(t *testing.T, secret string) *config.Config {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "fantasypros-api"), []byte(secret), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FF_SECRETS_DIR", dir)
	// Mirror the yaml default tag, which zero-value configs don't apply.
	return &config.Config{FantasyPros: config.FantasyProsConfig{APIKeySecret: "fantasypros-api"}}
}

func TestFetchFPRankingsData(t *testing.T) {
	cfg := fpTestConfig(t, "test-key")
	s := NewFPRankingsSyncer(nil, cfg)
	var req *http.Request
	srv := stubJSONServer(t, http.StatusOK, `{"players":[{"player_name":"A"}]}`, &req)
	defer srv.Close()
	fpRankingsURL = srv.URL
	defer func() { fpRankingsURL = "https://api.fantasypros.com/public/v2/json/nfl/2026/rankings" }()

	players, err := s.fetchFPRankingsData(t.Context())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(players) != 1 {
		t.Fatalf("want 1 player, got %d", len(players))
	}
	if got := req.Header.Get("X-Api-Key"); got != "test-key" {
		t.Fatalf("api key header missing: %q", got)
	}
	if got := req.URL.Query().Get("limit"); got != "500" {
		t.Fatalf("limit param missing: %q", got)
	}

	srv2 := stubJSONServer(t, http.StatusUnauthorized, "no key", nil)
	defer srv2.Close()
	fpRankingsURL = srv2.URL
	if _, err := s.fetchFPRankingsData(t.Context()); !errors.Is(err, errFPRankingsHTTP) {
		t.Fatalf("want errFPRankingsHTTP, got %v", err)
	}
}

func TestFetchFPInjuriesData(t *testing.T) {
	cfg := fpTestConfig(t, "test-key")
	s := NewFPInjuriesSyncer(nil, cfg)
	srv := stubJSONServer(t, http.StatusOK, `{"injuries":[{"name":"A"}]}`, nil)
	defer srv.Close()
	fpInjuriesURL = srv.URL
	defer func() { fpInjuriesURL = "https://api.fantasypros.com/public/v2/json/nfl/injuries" }()

	items, err := s.fetchFPInjuriesData(t.Context())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("want 1 injury, got %d", len(items))
	}

	srv2 := stubJSONServer(t, http.StatusBadGateway, "down", nil)
	defer srv2.Close()
	fpInjuriesURL = srv2.URL
	if _, err := s.fetchFPInjuriesData(t.Context()); !errors.Is(err, errFPInjuriesHTTP) {
		t.Fatalf("want errFPInjuriesHTTP, got %v", err)
	}
}

func TestFetchFPJSONMissingSecret(t *testing.T) {
	t.Setenv("FF_SECRETS_DIR", t.TempDir())
	cfg := &config.Config{}
	var out map[string]any
	err := fetchFPJSON(t.Context(), http.DefaultClient, cfg, "http://unused", errFPRankingsHTTP, "req", "dec", &out)
	if err == nil || !strings.Contains(err.Error(), "get FP API key") {
		t.Fatalf("want secret error, got %v", err)
	}
}
