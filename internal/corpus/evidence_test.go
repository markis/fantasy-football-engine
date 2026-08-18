package corpus

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- small item-field resolvers ---

func TestEvidenceItemURL(t *testing.T) {
	if got := evidenceItemURL(map[string]any{"canonical_url": "https://c", "url": "https://u"}); got != "https://c" {
		t.Fatalf("canonical must win: %q", got)
	}
	if got := evidenceItemURL(map[string]any{"url": "https://u"}); got != "https://u" {
		t.Fatalf("url fallback: %q", got)
	}
	// A nil map has no URL: fmt.Sprint(nil) yields the "<nil>" sentinel,
	// which callers treat as absent downstream.
	if got := evidenceItemURL(nil); got != "<nil>" {
		t.Fatalf("nil map must yield sentinel, got %q", got)
	}
}

func TestEvidenceSummaryPrecedence(t *testing.T) {
	story, short, title := new("story text"), new("short text"), new("title text")
	if got := evidenceSummary(map[string]any{"news_story": story, "summary_short": short, "title": title}, "u"); got != "story text" {
		t.Fatalf("news_story must win: %q", got)
	}
	if got := evidenceSummary(map[string]any{"summary_short": short, "title": title}, "u"); got != "short text" {
		t.Fatalf("summary_short must be second: %q", got)
	}
	if got := evidenceSummary(map[string]any{"title": title}, "u"); got != "title text" {
		t.Fatalf("title must be third: %q", got)
	}
	if got := evidenceSummary(map[string]any{}, "https://fallback"); got != "https://fallback" {
		t.Fatalf("url must be last resort: %q", got)
	}
	// Over-long summaries are capped at 1200 chars.
	long := strings.Repeat("x", 1500)
	if got := evidenceSummary(map[string]any{"title": new(long)}, "u"); len(got) != 1200 {
		t.Fatalf("want cap 1200, got %d", len(got))
	}
}

func TestEvidenceContentHashSeed(t *testing.T) {
	if got := evidenceContentHashSeed(map[string]any{"content_hash": "sha256:stored"}, "s"); got != "sha256:stored" {
		t.Fatalf("stored hash must win: %q", got)
	}
	if got := evidenceContentHashSeed(map[string]any{}, "s"); got != ContentHash("s") {
		t.Fatalf("derived hash wrong: %q", got)
	}
}

func TestEvidencePlayerAndTeamIDs(t *testing.T) {
	got := evidencePlayerIDs([]string{"1", "2"})
	if len(got) != 2 || got[0] != "nfl:1" || got[1] != "nfl:2" {
		t.Fatalf("player ids wrong: %v", got)
	}
	teams := evidenceTeamIDs(map[string]any{"entities": []string{"Philadelphia Eagles", "Coach Bob"}})
	if len(teams) != 1 || teams[0] != "nfl:PHI" {
		t.Fatalf("only known NFL team names must resolve: %v", teams)
	}
}

func TestEvidencePublisherTitleTopic(t *testing.T) {
	if got := evidencePublisher(map[string]any{"author": new("Pat")}); got != "Pat" {
		t.Fatalf("author must win: %q", got)
	}
	if got := evidencePublisher(map[string]any{}); got != "unknown" {
		t.Fatalf("default publisher wrong: %q", got)
	}
	if got := evidenceTitle(map[string]any{"title": new("T")}, "u"); got != "T" {
		t.Fatalf("title wrong: %q", got)
	}
	if got := evidenceTitle(map[string]any{}, "https://u"); got != "https://u" {
		t.Fatalf("title url fallback wrong: %q", got)
	}
	if got := evidenceTopic(map[string]any{"topics": []string{"injury"}}); got != "injury" {
		t.Fatalf("topic wrong: %q", got)
	}
	if got := evidenceTopic(map[string]any{}); got != "" {
		t.Fatalf("no topics must yield empty, got %q", got)
	}
}

func TestEvidenceTimestamps(t *testing.T) {
	p := newTestPublisher(t)
	ts := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	item := map[string]any{
		"published_at": new(ts),
		"updated_at":   new(ts),
	}
	pub, fetched, upd := p.evidenceTimestamps(item)
	if pub == nil || *pub != "2026-08-01T12:00:00Z" {
		t.Fatalf("published_at wrong: %v", pub)
	}
	if upd == nil || *upd != "2026-08-01T12:00:00Z" {
		t.Fatalf("updated_at wrong: %v", upd)
	}
	if _, err := time.Parse("2006-01-02T15:04:05Z", fetched); err != nil {
		t.Fatalf("fetched_at must fall back to now: %q", fetched)
	}
	// Nil everything -> empty published/updated, now fetched.
	pub, fetched, upd = p.evidenceTimestamps(map[string]any{})
	if pub != nil || upd != nil {
		t.Fatalf("nil timestamps must stay nil: %v %v", pub, upd)
	}
	if fetched == "" {
		t.Fatal("fetched_at must never be empty")
	}
}

// --- decision relevance ---

func TestBuildDecisionRelevance(t *testing.T) {
	ownership := map[string][][2]string{
		"1": {{"League A", "owned"}, {"League B", "rival"}},
		"2": {{"League C", "rival"}},
	}
	got := buildDecisionRelevance([]string{"1", "2"}, ownership)
	if !got.RelevantToRoster || !got.RelevantToTradeTarget {
		t.Fatalf("flags wrong: %+v", got)
	}
	if !strings.Contains(got.Reason, "owned in League A") || !strings.Contains(got.Reason, "rival in League C") {
		t.Fatalf("reason must enumerate roles: %q", got.Reason)
	}
	// No ownership info -> generic reason, no flags.
	got = buildDecisionRelevance([]string{"9"}, nil)
	if got.RelevantToRoster || got.RelevantToTradeTarget || got.Reason != "decision-relevant" {
		t.Fatalf("unowned players must be generic: %+v", got)
	}
	// Duplicate league/role pairs collapse in the reason.
	dup := evidenceOwnerReason([]string{"1", "1"}, map[string][][2]string{"1": {{"L", "owned"}}})
	if strings.Count(dup, "owned in L") != 1 {
		t.Fatalf("duplicates must collapse: %q", dup)
	}
}

// --- record writing / loading ---

func evidenceRecForTest(id, url string) *EvidenceRecord {
	return &EvidenceRecord{
		ID:           id,
		CanonicalURL: url,
		Title:        "title",
		Status:       "current",
		PlayerIDs:    []string{},
		TeamIDs:      []string{},
		Supersedes:   []string{},
		Claims:       []EvidenceClaim{},
	}
}

func TestWriteAndSupersedeRecords(t *testing.T) {
	dir := t.TempDir()
	current := map[string]*EvidenceRecord{
		"sha256:keep": evidenceRecForTest("sha256:keep", "https://keep"),
	}
	prev := map[string]*EvidenceRecord{
		"sha256:keep": evidenceRecForTest("sha256:keep", "https://keep"),
		"sha256:old":  evidenceRecForTest("sha256:old", "https://old"),
	}
	added := writeCurrentRecords(dir, current, prev)
	if len(added) != 0 {
		t.Fatalf("unchanged records must not be 'added': %v", added)
	}
	superseded, err := writeSupersededRecords(dir, current, prev)
	if err != nil {
		t.Fatal(err)
	}
	if len(superseded) != 1 || superseded[0] != "sha256:old" {
		t.Fatalf("want old record superseded: %v", superseded)
	}
	var rec EvidenceRecord
	if err := json.Unmarshal([]byte(read(t, filepath.Join(dir, "old.json"))), &rec); err != nil {
		t.Fatal(err)
	}
	if rec.Status != "superseded" {
		t.Fatalf("persisted status must be superseded: %q", rec.Status)
	}
	loaded := loadExistingRecords(dir)
	if _, ok := loaded["sha256:old"]; !ok || loaded["sha256:old"].Status != "superseded" {
		t.Fatalf("loadExistingRecords must round-trip: %#v", loaded)
	}
	// A brand-new record is reported as added.
	fresh := map[string]*EvidenceRecord{"sha256:new": evidenceRecForTest("sha256:new", "https://new")}
	if got := writeCurrentRecords(dir, fresh, prev); len(got) != 1 || got[0] != "sha256:new" {
		t.Fatalf("new record must be added: %v", got)
	}
}

func TestBuildEvidenceIndexMarkdown(t *testing.T) {
	p := newTestPublisher(t)
	older := "2026-07-01T00:00:00Z"
	newer := "2026-08-01T00:00:00Z"
	current := map[string]*EvidenceRecord{
		"sha256:old": {ID: "sha256:old", Title: "Older|Story", PublishedAt: &older, PlayerIDs: []string{"nfl:7"}},
		"sha256:new": {ID: "sha256:new", Title: "Newer", PublishedAt: &newer, PlayerIDs: []string{}},
	}
	md := p.buildEvidenceIndexMarkdown(current, []string{"sha256:x"})
	if !strings.Contains(md, "| Published | Topic |") {
		t.Fatalf("table header missing:\n%s", md)
	}
	newIdx := strings.Index(md, "Newer")
	oldIdx := strings.Index(md, "Older/Story")
	if newIdx == -1 || oldIdx == -1 || newIdx > oldIdx {
		t.Fatalf("records must sort newest first and escape pipes:\n%s", md)
	}
}

func TestGetStr(t *testing.T) {
	m := map[string]any{
		"s":    "plain",
		"sp":   new("pointed"),
		"ip":   func() any { v := 3; return &v }(),
		"nilp": (*string)(nil),
		"n":    nil,
	}
	if getStr(m, "s") != "plain" || getStr(m, "sp") != "pointed" || getStr(m, "ip") != "3" {
		t.Fatal("value extraction wrong")
	}
	if getStr(m, "nilp") != "" || getStr(m, "n") != "" || getStr(m, "missing") != "" {
		t.Fatal("nils must yield empty")
	}
}

func TestLoadExistingRecordsSkipsBadFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "x.json"), []byte(`not json`), 0o600); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(dir, "y.json"), `{"id":"sha256:y"}`)
	if got := loadExistingRecords(dir); len(got) != 1 {
		t.Fatalf("want 1 record, got %d", len(got))
	}
}
