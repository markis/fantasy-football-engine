package pipeline

import "strings"

// nonFootballKeywords are whole-word (case-insensitive) signals that an
// article is about a sport other than (American) football. Items whose
// title or content match are skipped at ingest so they never enter the
// enrichment / dedup / clustering pipeline.
var nonFootballKeywords = []string{
	"baseball",
	"basketball",
	"hockey",
	"soccer",
	"mlb",
	"nba",
}

// isNonFootballSport reports whether the given title or content text
// references a non-football sport via a whole-word, case-insensitive match
// against nonFootballKeywords.
func isNonFootballSport(title, contentText string) bool {
	haystack := strings.ToLower(title + " " + contentText)
	for _, kw := range nonFootballKeywords {
		if containsWord(haystack, kw) {
			return true
		}
	}
	return false
}

// containsWord reports whether word appears in s as a whole word
// (bounded by non-letter characters or string ends).
func containsWord(s, word string) bool {
	word = strings.ToLower(word)
	for {
		idx := strings.Index(s, word)
		if idx < 0 {
			return false
		}
		startOK := idx == 0 || !isLetter(rune(s[idx-1]))
		endIdx := idx + len(word)
		endOK := endIdx == len(s) || !isLetter(rune(s[endIdx]))
		if startOK && endOK {
			return true
		}
		s = s[endIdx:]
	}
}

func isLetter(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}
