package corpus

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Validate validates the rendered corpus tree. Returns a list of errors.
// Fails closed: the publisher aborts on any non-empty list.
func Validate(target string) []string {
	var errors []string

	// JSON parse + secret scan over all tracked files
	filepath.Walk(target, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(target, path)
		if rel == ".gitignore" || rel == "corpus-manifest.json" {
			return nil
		}
		if strings.HasPrefix(rel, ".git") || strings.HasPrefix(rel, ".staging") {
			return nil
		}
		// Scan for secrets
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		content := string(data)
		for _, pat := range secretPatterns {
			if pat.MatchString(content) {
				errors = append(errors, "secret pattern "+pat.String()+" found in "+rel)
			}
		}
		// Validate JSON files
		if strings.HasSuffix(path, ".json") {
			var v interface{}
			if err := json.Unmarshal(data, &v); err != nil {
				errors = append(errors, rel+": invalid JSON: "+err.Error())
			}
		}
		// Validate JSONL files
		if strings.HasSuffix(path, ".jsonl") {
			for i, line := range strings.Split(content, "\n") {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				var v interface{}
				if err := json.Unmarshal([]byte(line), &v); err != nil {
					errors = append(errors, rel+":"+itoa(i+1)+": invalid JSON: "+err.Error())
				}
			}
		}
		return nil
	})

	// Validate manifest hashes
	manifestPath := filepath.Join(target, "corpus-manifest.json")
	if data, err := os.ReadFile(manifestPath); err == nil {
		var manifest map[string]interface{}
		if json.Unmarshal(data, &manifest) == nil {
			if files, ok := manifest["files"].([]interface{}); ok {
				for _, f := range files {
					fi, _ := f.(map[string]interface{})
					fp := filepath.Join(target, fi["path"].(string))
					if _, err := os.Stat(fp); err != nil {
						errors = append(errors, "manifest references missing file "+fi["path"].(string))
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
	regexp.MustCompile(`-----BEGIN (RSA |EC |OPENSSH |)PRIVATE KEY-----`),
	regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
	regexp.MustCompile(`\bsk-[A-Za-z0-9]{20,}`),
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := false
	if i < 0 {
		neg = true
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}