package htmlx

import (
	"fmt"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"golang.org/x/net/html"
)

// Chunk is a section of an article split at heading boundaries.
type Chunk struct {
	Heading string
	Text    string
}

// headingTags is the set of HTML tags that start a new chunk.
var headingTags = map[string]bool{
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
}

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
	return chunks
}

func singleChunk(text, articleTitle string) []Chunk {
	clean := strings.TrimSpace(ExtractText(text))
	if clean == "" {
		return nil
	}
	return []Chunk{{Heading: "", Text: articleTitle + " " + clean}}
}
