package corpus

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSchemaForPath(t *testing.T) {
	cases := []struct {
		rel    string
		schema string
		jsonl  bool
	}{
		{"team/leagues/abc/team-state.json", "team-state.schema.json", false},
		{"evidence/records/xyz.json", "evidence-record.schema.json", false},
		{"datasets/player-signals.jsonl", "player-signal.schema.json", true},
		{"datasets/valuations.jsonl", "valuation.schema.json", true},
		{"datasets/change-log.jsonl", "change-log.schema.json", true},
		{"corpus-manifest.json", "corpus-manifest.schema.json", false},
		{"current/daily-brief.md", "", false},
	}
	for _, tc := range cases {
		sch, jsonl := schemaForPath(tc.rel)
		if tc.schema == "" {
			if sch != nil {
				t.Errorf("%s: want no schema, got one", tc.rel)
			}
			continue
		}
		if sch == nil {
			t.Errorf("%s: want schema %s", tc.rel, tc.schema)
			continue
		}
		if jsonl != tc.jsonl {
			t.Errorf("%s: jsonl flag = %v, want %v", tc.rel, jsonl, tc.jsonl)
		}
	}
}

func TestValidateSecrets(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    bool
	}{
		{"github token", "token ghp_" + strings.Repeat("A", 30) + " end", true},
		{"github pat", "github_pat_" + strings.Repeat("a", 30), true},
		{"enc2", "cipher: enc2:0123456789abcdef", true},
		{"private key", "-----BEGIN PRIVATE KEY-----", true},
		{"aws key", "key AKIAABCDEFGHIJKLMNOP", true},
		{"sk key", "Bearer sk-" + strings.Repeat("x", 25), true},
		{"clean", "just normal fantasy news", false},
	}
	for _, tc := range cases {
		errs := validateSecrets("f.txt", tc.content)
		if (len(errs) > 0) != tc.want {
			t.Errorf("%s: got %v, want flagged=%v", tc.name, errs, tc.want)
		}
	}
}

func TestValidateJSONAndJSONLFiles(t *testing.T) {
	if errs := validateJSONFile("a.json", []byte(`{"ok":true}`)); len(errs) != 0 {
		t.Fatalf("valid JSON flagged: %v", errs)
	}
	if errs := validateJSONFile("a.json", []byte(`{`)); len(errs) != 1 || !strings.Contains(errs[0], "invalid JSON") {
		t.Fatalf("invalid JSON not flagged: %v", errs)
	}
	// A schema-governed file with a violating record is flagged.
	bad := `{"signal_id":"x"}`
	if errs := validateJSONLFile("datasets/player-signals.jsonl", bad); len(errs) == 0 {
		t.Fatal("schema-violating signal must be flagged")
	}
	// Schema-valid player-signal record passes (shape from validate_test.go).
	good := `{"signal_id":"signal:0123456789abcdef","player_id":"nfl:1","signal_type":"injury",` +
		`"value":{"status":"Q"},"source":"Sleeper","source_url":null,"observed_at":"2026-08-10T00:00:00Z",` +
		`"published_at":null,"confidence":"high","status":"current","evidence_record_id":null}`
	if errs := validateJSONLFile("datasets/player-signals.jsonl", good); len(errs) != 0 {
		t.Fatalf("valid signal flagged: %v", errs)
	}
	// Blank lines are skipped; malformed lines carry line numbers.
	if errs := validateJSONLFile("datasets/change-log.jsonl", "\n\nnope\n"); len(errs) != 1 || !strings.Contains(errs[0], ":3:") {
		t.Fatalf("line-numbered error wrong: %v", errs)
	}
}

func TestValidateTree(t *testing.T) {
	dir := t.TempDir()
	// A clean minimal tree validates without errors.
	mustWrite(t, filepath.Join(dir, "current", "daily-brief.md"), "# Daily Brief")
	mustWrite(t, filepath.Join(dir, ".gitignore"), "target\n")
	if errs := Validate(dir); len(errs) != 0 {
		t.Fatalf("clean tree flagged: %v", errs)
	}
	// Invalid JSON and a leaked secret are both flagged.
	mustWrite(t, filepath.Join(dir, "team", "broken.json"), `{`)
	mustWrite(t, filepath.Join(dir, "notes.txt"), "leak ghp_"+strings.Repeat("A", 30))
	errs := Validate(dir)
	if len(errs) != 2 {
		t.Fatalf("want 2 errors, got %v", errs)
	}
	// Missing target directory fails closed with an error string.
	if errs := Validate(filepath.Join(dir, "does-not-exist")); len(errs) != 1 {
		t.Fatalf("missing dir must error: %v", errs)
	}
}

func TestValidateManifestReferences(t *testing.T) {
	dir := t.TempDir()
	// No manifest at all -> no errors (validated elsewhere).
	if errs := validateManifest(dir); len(errs) != 0 {
		t.Fatalf("no manifest must not error: %v", errs)
	}
	manifest := map[string]any{"files": []any{
		map[string]any{"path": "exists.md"},
		map[string]any{"path": "missing.md"},
		"garbage",
		map[string]any{"sha256": "no path"},
	}}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(dir, "corpus-manifest.json"), string(data))
	mustWrite(t, filepath.Join(dir, "exists.md"), "x")
	errs := validateManifest(dir)
	want := []string{"missing file missing.md", `entry is not an object`, `entry missing "path"`}
	if len(errs) != len(want) {
		t.Fatalf("want %d errors, got %v", len(want), errs)
	}
	for i, w := range want {
		if !strings.Contains(errs[i], w) {
			t.Errorf("error %d: want %q in %q", i, w, errs[i])
		}
	}
}

func TestReadFileRootedErrors(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "f.txt")
	mustWrite(t, path, "content")
	if got, err := readFileRooted(path); err != nil || string(got) != "content" {
		t.Fatalf("read failed: %q %v", got, err)
	}
	if _, err := readFileRooted(filepath.Join(dir, "sub", "nope.txt")); err == nil {
		t.Fatal("missing file must error")
	}
	if _, err := readFileRooted(filepath.Join(dir, "no-such-dir", "f.txt")); err == nil {
		t.Fatal("missing dir must error")
	}
}

func TestRootWriteAllCreatesParents(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := rootWriteAll(root, filepath.Join("a", "b", "c.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := read(t, filepath.Join(dir, "a", "b", "c.txt")); got != "x" {
		t.Fatalf("content wrong: %q", got)
	}
}
