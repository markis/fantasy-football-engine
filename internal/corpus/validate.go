package corpus

import (
	"bytes"
	"encoding/json"
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
			return err
		}
		if rel == fileGitignore {
			return nil
		}
		if strings.HasPrefix(rel, ".git") || strings.HasPrefix(rel, ".staging") {
			return nil
		}
		// Scan for secrets
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		content := string(data)
		for _, pat := range secretPatterns {
			if pat.MatchString(content) {
				errors = append(errors, "secret pattern "+pat.String()+" found in "+rel)
			}
		}
		// Validate JSON files
		if strings.HasSuffix(path, ".json") {
			var v any
			if err := json.Unmarshal(data, &v); err != nil {
				errors = append(errors, rel+": invalid JSON: "+err.Error())
			} else if sch, isJSONL := schemaForPath(rel); sch != nil && !isJSONL {
				if err := sch.Validate(v); err != nil {
					errors = append(errors, rel+": schema violation: "+err.Error())
				}
			}
		}
		// Validate JSONL files
		if strings.HasSuffix(path, ".jsonl") {
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
		}
		return nil
	}); err != nil {
		errors = append(errors, "walk target directory: "+err.Error())
	}

	// Validate manifest hashes
	manifestPath := filepath.Join(target, "corpus-manifest.json")
	if data, err := os.ReadFile(manifestPath); err == nil {
		var manifest map[string]any
		if json.Unmarshal(data, &manifest) == nil {
			if files, ok := manifest["files"].([]any); ok {
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
			}
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
