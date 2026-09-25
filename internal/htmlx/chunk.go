package htmlx

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/net/html"

	"ff-engine/internal/util"
)

// Chunk is a section of an article split at heading boundaries.
type Chunk struct {
	Heading string
	Text    string
	// Prefix is the "{articleTitle}" or "{articleTitle} > {heading}" context
	// prepended to Text. Set by SplitByHeadings; callers constructing chunks
	// directly should set it so SplitLongChunks can re-apply it to sub-chunks.
	Prefix string
}

// headingTags is the set of HTML tags that start a new chunk.
var headingTags = map[string]bool{
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
}

// MaxChunkChars is the per-chunk character budget. Chunks exceeding this are
// further split at sentence boundaries so each sub-chunk gets its own full
// embedding instead of being truncated by the embedder's 7000-char cap.
const MaxChunkChars = 6000

// SplitByHeadings parses HTML and splits it into chunks at heading tags
// (h1–h6). Each chunk's Text is prefixed with "{articleTitle} > {heading}"
// (or just "{articleTitle}" for the intro chunk before the first heading).
// Articles with no headings produce a single chunk containing the full text.
func SplitByHeadings(htmlStr, articleTitle string) []Chunk {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlStr))
	if err != nil || doc == nil {
		return singleChunk(htmlStr, articleTitle)
	}

	body := doc.Find("body")
	if body.Length() == 0 {
		body = doc.Selection
	}

	var chunks []Chunk
	var currentHeading string
	var currentParts []string

	flush := func() {
		if currentHeading == "" && len(currentParts) == 0 {
			return
		}
		prefix := articleTitle
		if currentHeading != "" {
			prefix = fmt.Sprintf("%s > %s", articleTitle, currentHeading)
		}
		text := strings.TrimSpace(strings.Join(currentParts, " "))
		if text == "" {
			return
		}
		chunks = append(chunks, Chunk{
			Heading: currentHeading,
			Text:    prefix + " " + text,
			Prefix:  prefix,
		})
		currentParts = nil
	}

	body.Contents().Each(func(_ int, s *goquery.Selection) {
		node := s.Nodes[0]
		if node.Type == html.ElementNode && headingTags[node.Data] {
			flush()
			currentHeading = strings.TrimSpace(s.Text())
			return
		}
		text := strings.TrimSpace(s.Text())
		if text != "" {
			currentParts = append(currentParts, text)
		}
	})
	flush()

	if len(chunks) == 0 {
		return singleChunk(htmlStr, articleTitle)
	}
	return SplitLongChunks(chunks)
}

func singleChunk(text, articleTitle string) []Chunk {
	clean := strings.TrimSpace(ExtractText(text))
	if clean == "" {
		return nil
	}
	return SplitLongChunks([]Chunk{{Heading: "", Text: articleTitle + " " + clean, Prefix: articleTitle}})
}

// SplitLongChunks splits any chunk whose Text exceeds MaxChunkChars into
// multiple sub-chunks at sentence boundaries, preserving the heading and
// prefix on each sub-chunk. Chunks at or under the cap are returned unchanged.
func SplitLongChunks(chunks []Chunk) []Chunk {
	out := make([]Chunk, 0, len(chunks))
	for _, c := range chunks {
		if len(c.Text) <= MaxChunkChars {
			out = append(out, c)
			continue
		}
		out = append(out, splitOne(c)...)
	}
	return out
}

// splitOne splits a single oversized chunk at sentence boundaries into
// sub-chunks, each prefixed with the original heading context.
func splitOne(c Chunk) []Chunk {
	prefix := c.Prefix
	if prefix == "" {
		prefix = c.Heading
	}
	rest := c.Text
	// Strip the prefix from Text to get just the body.
	if prefix != "" {
		rest = strings.TrimPrefix(rest, prefix)
		rest = strings.TrimSpace(rest)
	}

	sentences := splitSentences(rest)
	var sub []Chunk
	var current strings.Builder
	// Budget for the prefix + space that prepends each sub-chunk's text.
	prefixOverhead := len(prefix) + 1
	for _, s := range sentences {
		// If a single sentence itself exceeds the cap, hard-split it,
		// accounting for the prefix overhead on each piece.
		if prefixOverhead+len(s) > MaxChunkChars {
			if current.Len() > 0 {
				sub = appendChunk(sub, c.Heading, prefix, current.String())
				current.Reset()
			}
			sub = append(sub, hardSplitSentence(c.Heading, prefix, s)...)
			continue
		}
		// If adding this sentence would exceed the cap and we already have
		// content, flush the current sub-chunk first.
		if current.Len() > 0 && prefixOverhead+current.Len()+1+len(s) > MaxChunkChars {
			sub = appendChunk(sub, c.Heading, prefix, current.String())
			current.Reset()
		}
		if current.Len() > 0 {
			current.WriteByte(' ')
		}
		current.WriteString(s)
	}
	if current.Len() > 0 {
		sub = appendChunk(sub, c.Heading, prefix, current.String())
	}
	return sub
}

// hardSplitSentence splits a single sentence longer than the chunk budget
// into fixed-size pieces, each carrying the heading/prefix. Cuts land on
// rune boundaries — cutting mid-rune produces invalid UTF-8 that Postgres
// rejects (SQLSTATE 22021).
func hardSplitSentence(heading, prefix, s string) []Chunk {
	prefixOverhead := len(prefix) + 1
	var sub []Chunk
	for s != "" {
		end := min(MaxChunkChars-prefixOverhead, len(s))
		if end <= 0 {
			end = 1
		}
		cut := util.TruncateRunes(s, end)
		if cut == "" {
			// A single rune larger than the whole chunk budget (only
			// possible with the end==1 fallback): drop it rather than
			// loop forever.
			_, size := utf8.DecodeRuneInString(s)
			if size == 0 {
				size = 1
			}
			s = s[min(size, len(s)):]
			continue
		}
		sub = append(sub, Chunk{Heading: heading, Text: prefix + " " + cut, Prefix: prefix})
		s = s[len(cut):]
	}
	return sub
}

// appendChunk builds a Chunk with the heading/prefix and accumulated text,
// appending to sub only if the text is non-empty.
func appendChunk(sub []Chunk, heading, prefix, text string) []Chunk {
	text = strings.TrimSpace(text)
	if text == "" {
		return sub
	}
	return append(sub, Chunk{Heading: heading, Text: prefix + " " + text, Prefix: prefix})
}

// sentenceEndRe matches sentence-ending punctuation followed by whitespace.
var sentenceEndRe = regexp.MustCompile(`[.!?]\s+`)

// splitSentences splits text into sentences at sentence-ending punctuation,
// preserving the punctuation with its sentence.
func splitSentences(text string) []string {
	indices := sentenceEndRe.FindAllStringIndex(text, -1)
	if len(indices) == 0 {
		return []string{text}
	}
	var sentences []string
	start := 0
	for _, idx := range indices {
		end := idx[1] // include the whitespace after the punctuation
		sentences = append(sentences, strings.TrimSpace(text[start:end]))
		start = end
	}
	if start < len(text) {
		rem := strings.TrimSpace(text[start:])
		if rem != "" {
			sentences = append(sentences, rem)
		}
	}
	return sentences
}
