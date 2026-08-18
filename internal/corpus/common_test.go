package corpus

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"ff-engine/internal/models"
	"ff-engine/internal/sleeper"
)

// --- Stable IDs / hashing ---

func TestSHA256KnownVector(t *testing.T) {
	// Well-known SHA-256 of the empty string.
	if got := SHA256(""); got != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Fatalf("SHA256(\"\") = %q", got)
	}
}

func TestStableIDPrefixesAndTruncation(t *testing.T) {
	// Every ID helper must namespace its prefix and truncate the digest to
	// 16 hex chars (except EvidenceID, which keeps the full digest).
	if got := ClaimID("text"); len(got) != 16+len("claim:") || got[:6] != "claim:" {
		t.Fatalf("ClaimID: %q", got)
	}
	if got := SignalID("1", "injury", "Q", "now"); len(got) != 16+len("signal:") || got[:7] != "signal:" {
		t.Fatalf("SignalID: %q", got)
	}
	if got := ValuationID("1", "s", "t", "c", "now"); len(got) != 16+len("valuation:") || got[:10] != "valuation:" {
		t.Fatalf("ValuationID: %q", got)
	}
	if got := ChangeID("ts", "e", "op"); len(got) != 16+len("change:") || got[:7] != "change:" {
		t.Fatalf("ChangeID: %q", got)
	}
	if got := EvidenceID("url", "hash"); len(got) != 64+len("sha256:") || got[:7] != "sha256:" {
		t.Fatalf("EvidenceID: %q", got)
	}
}

func TestIDHelpersAreDeterministicAndInputSensitive(t *testing.T) {
	first, second := ClaimID("a"), ClaimID("a")
	if first != second {
		t.Fatal("ClaimID not deterministic")
	}
	if ClaimID("a") == ClaimID("b") {
		t.Fatal("ClaimID collision for distinct inputs")
	}
	if got := ContentHash("x"); got != "sha256:"+SHA256("x") {
		t.Fatalf("ContentHash: %q", got)
	}
}

func TestFileSHA256Bytes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := FileSHA256Bytes(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != SHA256("hello") {
		t.Fatalf("got %q", got)
	}
	if _, err := FileSHA256Bytes(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("want error for missing file")
	}
}

// --- Common accessors ---

func TestCommonDirsAndNow(t *testing.T) {
	c := New(nil, nil, filepath.Join("tmp", "corpus-root"))
	if c.CorpusDir() != filepath.Join("tmp", "corpus-root") {
		t.Fatalf("CorpusDir: %q", c.CorpusDir())
	}
	if c.StagingDir() != filepath.Join(filepath.Join("tmp", "corpus-root"), ".staging") {
		t.Fatalf("StagingDir: %q", c.StagingDir())
	}
	c.SetGitIdentity("n", "e", "p")
	iso := c.NowISO()
	if _, err := time.Parse("2006-01-02T15:04:05Z", iso); err != nil {
		t.Fatalf("NowISO not parseable: %q", iso)
	}
	if c.NowTime().IsZero() {
		t.Fatal("NowTime zero")
	}
}

// --- Roster helpers ---

func flexOwner(id string) *sleeper.FlexString {
	fs := sleeper.FlexString(id)
	return &fs
}

func TestMyRoster(t *testing.T) {
	c := New(nil, nil, filepath.Join("tmp", "x"))
	rosters := []sleeper.Roster{
		{RosterID: 1, OwnerID: flexOwner("someone-else")},
		{RosterID: 2, OwnerID: flexOwner(models.MarkisUserID)},
		{RosterID: 3}, // orphaned roster: nil owner must not panic
	}
	got := c.MyRoster(rosters)
	if got == nil || got.RosterID != 2 {
		t.Fatalf("want roster 2, got %#v", got)
	}
	if c.MyRoster(nil) != nil {
		t.Fatal("want nil for no rosters")
	}
	if c.MyRoster([]sleeper.Roster{{RosterID: 9}}) != nil {
		t.Fatal("want nil when Markis absent")
	}
}

func TestAllRosterPlayerIDsDeduplicates(t *testing.T) {
	c := New(nil, nil, filepath.Join("tmp", "x"))
	rosters := []sleeper.Roster{
		{Players: sleeper.FlexStringSlice{"1", "2"}},
		{Players: sleeper.FlexStringSlice{"2", "3"}},
	}
	got := c.AllRosterPlayerIDs(rosters)
	if len(got) != 3 {
		t.Fatalf("want 3 unique ids, got %v", got)
	}
}

// --- Name normalization / matching ---

func TestNormalizeName(t *testing.T) {
	cases := map[string]string{
		"Ja'Marr Chase":       "jamarr chase",
		"BJ Ojulari":          "bj ojulari",
		"Marvin Harrison Jr.": "marvin harrison",
		"  Travis   Hunter  ": "travis hunter",
		"Mikey†":              "mikey",
	}
	for in, want := range cases {
		if got := NormalizeName(in); got != want {
			t.Errorf("NormalizeName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuildNameIndexAndMatch(t *testing.T) {
	str := func(s string) *string { return &s }
	rows := map[string]*PlayerRow{
		"1": {FullName: str("Ja'Marr Chase"), SearchFullName: str("jamarr chase"), LastName: str("Chase")},
		"2": {FullName: str("Travis Hunter")},
	}
	idx := BuildNameIndex(rows)
	// Each distinct normalized name maps back to the player id.
	for _, key := range []string{"jamarr chase", "chase", "travis hunter"} {
		if len(idx[key]) == 0 {
			t.Errorf("index missing %q: %#v", key, idx)
		}
	}

	got := MatchEntitiesToPlayers([]string{"JAMARR CHASE", "Travis Hunter", "ab"}, idx)
	if len(got) != 2 {
		t.Fatalf("want 2 matched players, got %v", got)
	}
	// Short keys (<3 chars) are skipped.
	if len(MatchEntitiesToPlayers([]string{"ab"}, idx)) != 0 {
		t.Fatal("short entity names must not match")
	}
}

// --- Topic mapping ---

func TestTopicFromTopics(t *testing.T) {
	cases := []struct {
		topics []string
		want   string
	}{
		{[]string{"injury"}, "injury"},
		{[]string{"transaction"}, "transaction"},
		{[]string{"depth chart"}, "depth-chart"},
		{[]string{"snap"}, "usage"},             // snap -> usage rule
		{[]string{"trade", "market"}, "market"}, // trade -> market
		{[]string{"draft", "rookie"}, "rookie"}, // draft -> rookie
		{[]string{"schedule"}, "schedule"},
		{[]string{"performance"}, "production"},
		{[]string{"target"}, "usage"},  // target -> usage
		{[]string{"cooking"}, "other"}, // no rule matches
		{[]string{"INJURY"}, "injury"}, // case-insensitive
	}
	for _, tc := range cases {
		if got := TopicFromTopics(tc.topics); got != tc.want {
			t.Errorf("TopicFromTopics(%v) = %q, want %q", tc.topics, got, tc.want)
		}
	}
	// Injury wins over market when both present (rule order).
	if got := TopicFromTopics([]string{"market", "injury"}); got != "injury" {
		t.Fatalf("injury should take precedence, got %q", got)
	}
}

// --- File IO helpers ---

func TestWriteJSONAppendableAndReadable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "doc.json")
	if err := WriteJSON(path, map[string]int{"a": 1}); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]int
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("written file not valid JSON: %v", err)
	}
	if m["a"] != 1 {
		t.Fatalf("content mismatch: %s", data)
	}
}

func TestAppendJSONLAppendsLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "log.jsonl")
	for i := range 2 {
		if err := AppendJSONL(path, map[string]int{"i": i}); err != nil {
			t.Fatalf("AppendJSONL: %v", err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 || data[len(data)-1] != '\n' {
		t.Fatalf("jsonl must end with newline: %q", data)
	}
	lines := 0
	for _, line := range splitLines(string(data)) {
		if line == "" {
			continue
		}
		var m map[string]int
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("bad line %q: %v", line, err)
		}
		lines++
	}
	if lines != 2 {
		t.Fatalf("want 2 lines, got %d", lines)
	}
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := range len(s) {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}
