package corpus

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ff-engine/internal/models"
)

// newTestPublisher builds a Publisher whose Common has nil pool/sleeper —
// suitable for the pure file-rendering helpers that never touch them.
func newTestPublisher(t *testing.T) *Publisher {
	t.Helper()
	return NewPublisher(New(nil, nil, t.TempDir()))
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// --- loadTeamState / loadCurrentRecords ---

func TestLoadTeamState(t *testing.T) {
	dir := t.TempDir()
	if got := loadTeamState(dir); got != nil {
		t.Fatalf("want nil when team-state.json missing, got %#v", got)
	}
	mustWrite(t, filepath.Join(dir, "team", "team-state.json"), `{"leagues":[]}`)
	got := loadTeamState(dir)
	if got == nil {
		t.Fatal("want parsed map")
	}
	if _, ok := got["leagues"]; !ok {
		t.Fatalf("missing leagues key: %#v", got)
	}
	// Invalid JSON must yield nil, not a partial map.
	mustWrite(t, filepath.Join(dir, "team", "team-state.json"), `{`)
	if got := loadTeamState(dir); got != nil {
		t.Fatalf("want nil for invalid JSON, got %#v", got)
	}
}

func TestLoadCurrentRecords(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a.json"), `{"id":"sha256:a","status":"current"}`)
	mustWrite(t, filepath.Join(dir, "b.json"), `{"id":"sha256:b","status":"superseded"}`)
	mustWrite(t, filepath.Join(dir, "c.txt"), `not json`)
	recs := loadCurrentRecords(dir)
	if len(recs) != 1 {
		t.Fatalf("want 1 current record, got %d", len(recs))
	}
	if len(loadCurrentRecords(filepath.Join(dir, "missing"))) != 0 {
		t.Fatal("want 0 records for missing dir")
	}
}

// --- daily brief / filters / formatting ---

func TestFilterRecent(t *testing.T) {
	now := time.Now().UTC()
	fresh := now.Add(-1 * time.Hour).Format("2006-01-02T15:04:05Z")
	stale := now.Add(-25 * time.Hour).Format("2006-01-02T15:04:05Z")
	recs := []map[string]any{
		{"published_at": fresh, "id": "fresh"},
		{"published_at": stale, "id": "stale"},
		{"published_at": "not-a-date", "id": "bad"},
		{"id": "empty"},
	}
	got := filterRecent(recs, 24)
	if len(got) != 1 || got[0]["id"] != "fresh" {
		t.Fatalf("want only fresh, got %#v", got)
	}
}

func TestTruncateID(t *testing.T) {
	if got := truncateID("sha256:abcdefghijklmnopqrstuvwx"); len(got) != 24 || got != "sha256:abcdefghijklmnopq" {
		t.Fatalf("want 24-char prefix, got %q", got)
	}
	if got := truncateID("short"); got != "short" {
		t.Fatalf("short id must pass through, got %q", got)
	}
}

func TestFmtRec(t *testing.T) {
	rec := map[string]any{
		"published_at":  "2026-08-01T12:00:00Z",
		"id":            "sha256:0123456789abcdef0123456789abcdef",
		"topic":         "injury",
		"title":         "Player questionable",
		"publisher":     "ESPN",
		"canonical_url": "https://example.com",
		"player_ids":    []string{"nfl:1", "nfl:2", "nfl:3", "nfl:4", "nfl:5"},
	}
	got := fmtRec(rec)
	for _, want := range []string{"2026-08-01", "injury", "Player questionable", "1, 2, 3, 4"} {
		if !strings.Contains(got, want) {
			t.Errorf("fmtRec output missing %q: %s", want, got)
		}
	}
	if strings.Contains(got, "nfl:5") {
		t.Errorf("5th player must be dropped: %s", got)
	}
}

func TestWriteDailyBrief(t *testing.T) {
	p := newTestPublisher(t)
	curDir := t.TempDir()
	fresh := time.Now().UTC().Add(-time.Hour).Format("2006-01-02T15:04:05Z")
	recs := []map[string]any{
		{"published_at": fresh, "status": "current", "id": "sha256:x", "summary": "big news"},
	}
	got := p.writeDailyBrief(curDir, "2026-08-01T00:00:00Z", recs)
	if len(got) != 1 {
		t.Fatalf("want 1 recent item, got %d", len(got))
	}
	body := read(t, filepath.Join(curDir, "daily-brief.md"))
	if !strings.Contains(body, "# Daily Brief") || !strings.Contains(body, "big news") {
		t.Fatalf("daily brief content wrong:\n%s", body)
	}
	// Empty window renders the no-news note.
	got = p.writeDailyBrief(curDir, "2026-08-01T00:00:00Z", nil)
	if len(got) != 0 {
		t.Fatalf("want 0 items, got %d", len(got))
	}
	if !strings.Contains(read(t, filepath.Join(curDir, "daily-brief.md")), "No new decision-relevant evidence") {
		t.Fatal("empty brief must carry the no-news note")
	}
}

func TestWriteInjuryAndUsage(t *testing.T) {
	p := newTestPublisher(t)
	curDir := t.TempDir()
	recs := []map[string]any{
		{"topic": "injury", "id": "sha256:a", "title": "ankle"},
		{"topic": "usage", "id": "sha256:b", "title": "snap count"},
		{"topic": "market", "id": "sha256:c", "title": "trade"},
	}
	p.writeInjuryAndUsage(curDir, "2026-08-01T00:00:00Z", recs)
	body := read(t, filepath.Join(curDir, "injury-and-usage.md"))
	for _, want := range []string{"## Injuries", "ankle", "## Usage / role / depth-chart", "snap count"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in:\n%s", want, body)
		}
	}
	if strings.Contains(body, "trade") {
		t.Errorf("market topic must not appear:\n%s", body)
	}
	// No matching records -> both sections show the empty note.
	p.writeInjuryAndUsage(curDir, "2026-08-01T00:00:00Z", nil)
	if got := read(t, filepath.Join(curDir, "injury-and-usage.md")); !strings.Contains(got, "_None in the current evidence window._") {
		t.Errorf("empty groups must render the note:\n%s", got)
	}
}

func TestWriteWeeklyTeamReview(t *testing.T) {
	p := newTestPublisher(t)
	curDir := t.TempDir()
	if err := p.writeWeeklyTeamReview(curDir, "2026-08-01T00:00:00Z", nil); err != nil {
		t.Fatalf("nil team state: %v", err)
	}
	if !strings.Contains(read(t, filepath.Join(curDir, "weekly-team-review.md")), "# Weekly Team Review") {
		t.Fatal("header missing")
	}
	ts := map[string]any{"leagues": []any{
		map[string]any{"league": map[string]any{"name": "League One"}, "team": map[string]any{"competitive_mode": "win-now"}},
		"garbage-entry",
	}}
	if err := p.writeWeeklyTeamReview(curDir, "2026-08-01T00:00:00Z", ts); err != nil {
		t.Fatalf("with team state: %v", err)
	}
	body := read(t, filepath.Join(curDir, "weekly-team-review.md"))
	if !strings.Contains(body, "## League One") || !strings.Contains(body, "win-now") {
		t.Fatalf("league section missing:\n%s", body)
	}
}

func TestWriteStaticCurrentBriefs(t *testing.T) {
	p := newTestPublisher(t)
	curDir := t.TempDir()
	if err := p.writeUpcomingDecisions(curDir, "2026-08-01T00:00:00Z"); err != nil {
		t.Fatalf("writeUpcomingDecisions: %v", err)
	}
	if !strings.Contains(read(t, filepath.Join(curDir, "upcoming-decisions.md")), "no autonomous action") {
		t.Fatal("autonomy guardrail missing")
	}
	p.writeMarketAndTradeWatch(curDir, "2026-08-01T00:00:00Z")
	if !strings.Contains(read(t, filepath.Join(curDir, "market-and-trade-watch.md")), "Recommendation guardrails") {
		t.Fatal("guardrails section missing")
	}
	targetDir := t.TempDir()
	if err := p.writeCurrentTeamPlan(targetDir, "2026-08-01T00:00:00Z"); err != nil {
		t.Fatalf("writeCurrentTeamPlan: %v", err)
	}
	if !strings.Contains(read(t, filepath.Join(targetDir, "strategy", "current-team-plan.md")), "# Current Team Plan") {
		t.Fatal("team plan header missing")
	}
}

// --- change log ---

func TestCarryForwardChangeLog(t *testing.T) {
	dir := t.TempDir()
	prev := filepath.Join(dir, "prev")
	next := filepath.Join(dir, "next")
	mustWrite(t, filepath.Join(prev, "datasets", "change-log.jsonl"), `{"a":1}`+"\n")
	// The target's datasets dir already exists in a real render tree; the
	// carry-forward writes the file directly without creating parents.
	mustWrite(t, filepath.Join(next, "datasets", "change-log.jsonl"), "")
	carryForwardChangeLog(filepath.Join(next, "datasets", "change-log.jsonl"), filepath.Join(prev, "datasets", "change-log.jsonl"))
	if got := read(t, filepath.Join(next, "datasets", "change-log.jsonl")); got != `{"a":1}`+"\n" {
		t.Fatalf("change log not carried forward: %q", got)
	}
	// No prior log -> empty file created (parent dir pre-exists, as in a
	// real render tree).
	fresh := filepath.Join(dir, "fresh")
	if err := os.MkdirAll(filepath.Join(fresh, "datasets"), 0o750); err != nil {
		t.Fatal(err)
	}
	carryForwardChangeLog(filepath.Join(fresh, "datasets", "change-log.jsonl"), filepath.Join(dir, "missing"))
	if got := read(t, filepath.Join(fresh, "datasets", "change-log.jsonl")); got != "" {
		t.Fatalf("want empty log, got %q", got)
	}
}

func TestAppendEvidenceChangeLog(t *testing.T) {
	dir := t.TempDir()
	recDir := filepath.Join(dir, "records")
	rid := "sha256:" + strings.Repeat("a", 20)
	mustWrite(t, filepath.Join(recDir, strings.TrimPrefix(rid, "sha256:")+".json"), `{"content_hash":"sha256:abc"}`)
	clPath := filepath.Join(dir, "datasets", "change-log.jsonl")
	lastID, n := appendEvidenceChangeLog(clPath, recDir, "2026-08-01T00:00:00Z",
		map[string]any{"added": []string{rid}, "superseded": []string{}})
	if n != 1 {
		t.Fatalf("want 1 change, got %d", n)
	}
	if lastID == "" || !strings.HasPrefix(lastID, "change:") {
		t.Fatalf("bad lastChangeID %q", lastID)
	}
	var entry ChangeLogEntry
	if err := json.Unmarshal([]byte(strings.SplitN(read(t, clPath), "\n", 2)[0]), &entry); err != nil {
		t.Fatalf("change log entry not valid JSON: %v", err)
	}
	if entry.EntityID != rid || entry.Operation != "added" || entry.ContentHash != "sha256:abc" {
		t.Fatalf("entry fields wrong: %+v", entry)
	}
	// Missing record file falls back to hashing the record id itself.
	_, n = appendEvidenceChangeLog(clPath, filepath.Join(dir, "missing"), "2026-08-01T00:00:00Z",
		map[string]any{"superseded": []string{rid}})
	if n != 1 {
		t.Fatalf("want 1 superseded change, got %d", n)
	}
}

// --- manifest assembly ---

func TestWalkFilesForManifest(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "current", "daily-brief.md"), "x")
	mustWrite(t, filepath.Join(dir, ".git", "config"), "x")
	mustWrite(t, filepath.Join(dir, ".staging", "tmp"), "x")
	mustWrite(t, filepath.Join(dir, "schemas", "skip.json"), "x")
	mustWrite(t, filepath.Join(dir, ".gitignore"), "x")
	mustWrite(t, filepath.Join(dir, "corpus-manifest.json"), "x")
	mustWrite(t, filepath.Join(dir, ".publish.log"), "x")
	files, err := walkFilesForManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("want only daily-brief.md, got %v", files)
	}
}

func TestCountJSONLFileAndBuildManifestCounts(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "datasets", "player-signals.jsonl"), "\n\n{\"a\":1}\n{\"b\":2}\n\n")
	if got := countJSONLFile(dir, "datasets/player-signals.jsonl"); got != 2 {
		t.Fatalf("want 2 lines, got %d", got)
	}
	if got := countJSONLFile(dir, "datasets/missing.jsonl"); got != 0 {
		t.Fatalf("want 0 for missing file, got %d", got)
	}
	counts := buildManifestCounts(dir, 5, 1)
	if counts["evidence"] != 5 || counts["team_state"] != 1 || counts["player_signal"] != 2 {
		t.Fatalf("counts wrong: %v", counts)
	}
}

func TestCountCurrentEvidence(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a.json"), `{"status":"current"}`)
	mustWrite(t, filepath.Join(dir, "b.json"), `{"status":"superseded"}`)
	mustWrite(t, filepath.Join(dir, "c.json"), `invalid`)
	mustWrite(t, filepath.Join(dir, "d.txt"), `{"status":"current"}`)
	if got := countCurrentEvidence(dir); got != 1 {
		t.Fatalf("want 1 current, got %d", got)
	}
}

func TestBuildManifestFilesMeta(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "current", "brief.md")
	mustWrite(t, a, "hello")
	cl := filepath.Join(dir, "datasets", "change-log.jsonl")
	mustWrite(t, cl, "{}\n")
	meta := buildManifestFilesMeta(map[string]string{
		"current/brief.md":          a,
		"datasets/change-log.jsonl": cl,
	})
	if len(meta) != 1 {
		t.Fatalf("change-log must be excluded from files meta, got %v", meta)
	}
	if meta[0].Path != "current/brief.md" || meta[0].SHA256 != SHA256("hello") || meta[0].Bytes != 5 {
		t.Fatalf("meta wrong: %+v", meta[0])
	}
}

func TestBuildManifestLeagueList(t *testing.T) {
	list := buildManifestLeagueList()
	if len(list) != len(models.LeagueFormats) {
		t.Fatalf("want one entry per league format, got %d", len(list))
	}
}

func TestRenderManifest(t *testing.T) {
	p := newTestPublisher(t)
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "team", "team-state.json"), `{"leagues":[]}`)
	mustWrite(t, filepath.Join(dir, "datasets", "change-log.jsonl"), "")
	mustWrite(t, filepath.Join(dir, "current", "daily-brief.md"), "# Daily Brief")
	summary, err := p.renderManifest(t.Context(), dir, filepath.Join(dir, "no-prev"), map[string]any{})
	if err != nil {
		t.Fatalf("renderManifest: %v", err)
	}
	if summary["files"] == 0 {
		t.Fatalf("want files in summary: %v", summary)
	}
	var m Manifest
	if err := json.Unmarshal([]byte(read(t, filepath.Join(dir, "corpus-manifest.json"))), &m); err != nil {
		t.Fatalf("manifest not valid JSON: %v", err)
	}
	if m.SchemaVersion != 1 || m.GeneratedAt == "" || len(m.Files) == 0 {
		t.Fatalf("manifest fields wrong: %+v", m)
	}
	if m.TeamStateHash == nil || *m.TeamStateHash != SHA256(`{"leagues":[]}`) {
		t.Fatalf("team state hash wrong: %v", m.TeamStateHash)
	}
	// An added evidence id populates the change-log cursor.
	rid := "sha256:" + strings.Repeat("b", 20)
	summary2, err2 := p.renderManifest(t.Context(), dir, dir, map[string]any{"added": []string{rid}})
	if err2 != nil {
		t.Fatalf("renderManifest with changes: %v", err2)
	}
	if summary2["changes"] != 1 || summary2["cursor"] == nil {
		t.Fatalf("change summary wrong: %v", summary2)
	}
}

// --- rookie draft board row rendering ---

// fakeRows satisfies the minimal rows interface appendRookieDraftLines
// consumes, feeding canned scan results.
var errUnsupportedDest = errors.New("unsupported dest type")

type fakeRows struct {
	rows [][]any
	idx  int
}

func (f *fakeRows) Close()     {}
func (f *fakeRows) Next() bool { return f.idx < len(f.rows) }

func (f *fakeRows) Scan(dest ...any) error {
	vals := f.rows[f.idx]
	f.idx++
	for i, d := range dest {
		switch p := d.(type) {
		case **string:
			v, _ := vals[i].(*string)
			*p = v
		case **int:
			v, _ := vals[i].(*int)
			*p = v
		default:
			return errUnsupportedDest
		}
	}
	return nil
}

func TestAppendRookieDraftLines(t *testing.T) {
	str := func(s string) *string { return &s }
	age := func(n int) *int { return &n }
	rows := &fakeRows{rows: [][]any{
		{str("QB Rookie"), str("QB"), str("KC"), age(23), age(0), age(2500), age(1), age(1)},
		{str("RB Rookie"), str("RB"), str("SF"), age(22), age(0), age(1800), age(8), age(3)},
		{str("Bad Row"), nil, nil, nil, nil, nil, nil, nil}, // Scan error path: skipped
	}}
	var lines []string
	newTestPublisher(t).appendRookieDraftLines(rows, &lines)
	out := strings.Join(lines, "\n")
	for _, want := range []string{"## QB", "## RB", "| QB Rookie | KC | 23 | 2500 | 1 | 1 |", "| RB Rookie | SF |"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	// A scan failure must not abort the remaining rows.
	if rows.idx != 3 {
		t.Fatalf("all rows must be consumed, got %d", rows.idx)
	}
}

func TestDerefHelpers(t *testing.T) {
	s := "val"
	i := 7
	if derefStr(&s, "def") != "val" || derefStr(nil, "def") != "def" {
		t.Fatal("derefStr wrong")
	}
	if derefInt(&i) != "7" || derefInt(nil) != "" {
		t.Fatal("derefInt wrong")
	}
}
