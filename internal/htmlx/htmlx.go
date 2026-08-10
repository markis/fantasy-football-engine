// Package htmlx provides HTML text extraction utilities.
// It uses a simple tag-stripping approach (no external dependency like
// trafilatura). For most RSS-sourced articles this is sufficient since the
// content is already in the feed. For body-fetched articles, this provides
// a best-effort extraction.
package htmlx

import (
	"regexp"
	"strings"
)

var (
	scriptRe     = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	styleRe      = regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	navRe        = regexp.MustCompile(`(?is)<nav[^>]*>.*?</nav>`)
	headerRe     = regexp.MustCompile(`(?is)<header[^>]*>.*?</header>`)
	footerRe     = regexp.MustCompile(`(?is)<footer[^>]*>.*?</footer>`)
	tagRe        = regexp.MustCompile(`<[^>]+>`)
	whitespaceRe = regexp.MustCompile(`\s+`)
	pRe          = regexp.MustCompile(`(?i)</p>|<br\s*/?>|<br>`)
)

// ExtractText extracts plain text from HTML, removing scripts/styles/nav.
func ExtractText(html string) string {
	html = scriptRe.ReplaceAllString(html, " ")
	html = styleRe.ReplaceAllString(html, " ")
	html = navRe.ReplaceAllString(html, " ")
	html = headerRe.ReplaceAllString(html, " ")
	html = footerRe.ReplaceAllString(html, " ")
	// Convert block tags to spaces/newlines
	html = pRe.ReplaceAllString(html, "\n")
	html = tagRe.ReplaceAllString(html, " ")
	html = whitespaceRe.ReplaceAllString(html, " ")
	return strings.TrimSpace(html)
}

// ExtractMainHTML returns the cleaned HTML body (tags stripped to basic structure).
// For simplicity, this returns the original HTML with scripts/styles removed.
func ExtractMainHTML(html string) string {
	html = scriptRe.ReplaceAllString(html, " ")
	html = styleRe.ReplaceAllString(html, " ")
	return html
}
