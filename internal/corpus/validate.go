package corpus

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

// schemaRule maps a corpus-relative path pattern to the JSON schema that
// governs it. jsonl is true when the file is one-record-per-line rather
// than a single JSON document.
type schemaRule struct {
	pathPattern *regexp.Regexp
	schemaFile  string
	jsonl       bool
}

var schemaRules = []schemaRule{
	{regexp.MustCompile(`^team/leagues/[^/]+/team-state\.json$`), "team-state.schema.json", false},
	{regexp.MustCompile(`^evidence/records/[^/]+\.json$`), "evidence-record.schema.json", false},
	{regexp.MustCompile(`^datasets/player-signals\.jsonl$`), "player-signal.schema.json", true},
	{regexp.MustCompile(`^datasets/valuations\.jsonl$`), "valuation.schema.json", true},
	{regexp.MustCompile(`^datasets/change-log\.jsonl$`), "change-log.schema.json", true},
	{regexp.MustCompile(`^strategy/decision-log\.jsonl$`), "decision-record.schema.json", true},
	{regexp.MustCompile(`^corpus-manifest\.json$`), "corpus-manifest.schema.json", false},
}

var compiledSchemas = compileSchemas()

func compileSchemas() map[string]*jsonschema.Schema {
	result := make(map[string]*jsonschema.Schema)
	entries, err := schemaFS.ReadDir("schemas")
	if err != nil {
		return result
	}
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat = true
	for _, e := range entries {
		data, err := schemaFS.ReadFile("schemas/" + e.Name())
		if err != nil {
			continue
		}
		if err := compiler.AddResource(e.Name(), bytes.NewReader(data)); err != nil {
			continue
		}
	}
	for _, e := range entries {
		sch, err := compiler.Compile(e.Name())
		if err != nil {
			continue
		}
		result[e.Name()] = sch
	}
	return result
}

// schemaForPath returns the compiled schema governing rel (if any) and
// whether rel is a JSONL (one-record-per-line) file.
func schemaForPath(rel string) (*jsonschema.Schema, bool) {
	rel = filepath.ToSlash(rel)
	for _, rule := range schemaRules {
		if rule.pathPattern.MatchString(rel) {
			return compiledSchemas[rule.schemaFile], rule.jsonl
		}
	}
	return nil, false
}

// Validate validates the rendered corpus tree. Returns a list of errors.
// Fails closed: the publisher aborts on any non-empty list.
func Validate(target string) []string {
	var errors []string

	// Open the target tree as an os.Root so all reads are confined to it:
	// gosec G304 doesn't flag Root methods, and path traversal is blocked.
	root, rootErr := os.OpenRoot(target)
	if rootErr != nil {
		return []string{"open target directory: " + rootErr.Error()}
	}
	defer root.Close()

	// JSON parse + secret scan + JSON-schema validation over all tracked files
	if err := filepath.Walk(target, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(target, path)
		if err != nil {
			return fmt.Errorf("relativize path %s: %w", path, err)
		}
		if rel == fileGitignore {
			return nil
		}
		if strings.HasPrefix(rel, ".git") || strings.HasPrefix(rel, ".staging") {
			return nil
		}
		data, err := rootReadAll(root, rel)
		if err != nil {
			return fmt.Errorf("read file %s: %w", rel, err)
		}
		content := string(data)
		errors = append(errors, validateSecrets(rel, content)...)
		if strings.HasSuffix(path, ".json") {
			errors = append(errors, validateJSONFile(rel, data)...)
		}
		if strings.HasSuffix(path, ".jsonl") {
			errors = append(errors, validateJSONLFile(rel, content)...)
		}
		return nil
	}); err != nil {
		errors = append(errors, "walk target directory: "+err.Error())
	}

	errors = append(errors, validateManifest(target)...)

	return errors
}

// validateSecrets scans file content for known secret patterns.
func validateSecrets(rel, content string) []string {
	var errors []string
	for _, pat := range secretPatterns {
		if pat.MatchString(content) {
			errors = append(errors, "secret pattern "+pat.String()+" found in "+rel)
		}
	}
	return errors
}

// validateJSONFile checks that a .json file parses as valid JSON and, if a
// schema governs it, validates it against that schema.
func validateJSONFile(rel string, data []byte) []string {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return []string{rel + ": invalid JSON: " + err.Error()}
	}
	var errors []string
	if sch, isJSONL := schemaForPath(rel); sch != nil && !isJSONL {
		if err := sch.Validate(v); err != nil {
			errors = append(errors, rel+": schema violation: "+err.Error())
		}
	}
	return errors
}

// validateJSONLFile checks that each non-empty line of a .jsonl file parses
// as valid JSON and, if a schema governs it, validates it against that schema.
func validateJSONLFile(rel, content string) []string {
	var errors []string
	sch, isJSONL := schemaForPath(rel)
	if !isJSONL {
		sch = nil
	}
	for i, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var v any
		if err := json.Unmarshal([]byte(line), &v); err != nil {
			errors = append(errors, rel+":"+strconv.Itoa(i+1)+": invalid JSON: "+err.Error())
			continue
		}
		if sch != nil {
			if err := sch.Validate(v); err != nil {
				errors = append(errors, rel+":"+strconv.Itoa(i+1)+": schema violation: "+err.Error())
			}
		}
	}
	return errors
}

// validateManifest checks that every file referenced by corpus-manifest.json exists.
func validateManifest(target string) []string {
	var errors []string
	manifestPath := filepath.Join(target, "corpus-manifest.json")
	data, err := readFileRooted(manifestPath)
	if err != nil {
		return errors
	}
	var manifest map[string]any
	if json.Unmarshal(data, &manifest) != nil {
		return errors
	}
	files, ok := manifest["files"].([]any)
	if !ok {
		return errors
	}
	for _, f := range files {
		fi, ok := f.(map[string]any)
		if !ok {
			errors = append(errors, "manifest \"files\" entry is not an object")
			continue
		}
		path, ok := fi["path"].(string)
		if !ok {
			errors = append(errors, "manifest \"files\" entry missing \"path\"")
			continue
		}
		fp := filepath.Join(target, path)
		if _, err := os.Stat(fp); err != nil {
			errors = append(errors, "manifest references missing file "+path)
		}
	}
	return errors
}

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`gh[ps]_[A-Za-z0-9]{20,}`),
	regexp.MustCompile(`github_pat_[A-Za-z0-9_]{20,}`),
	regexp.MustCompile(`\benc2:[0-9a-f]{8,}`),
	regexp.MustCompile(`-{5}BEGIN (RSA |EC |OPENSSH |)PRIVATE KEY-{5}`),
	regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
	regexp.MustCompile(`\bsk-[A-Za-z0-9]{20,}`),
}
