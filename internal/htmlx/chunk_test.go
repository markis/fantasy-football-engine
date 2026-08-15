package htmlx

import (
	"fmt"
	"strings"
	"testing"
)

func TestSplitByHeadings_MultiHeading(t *testing.T) {
	html := `<h2>Round 1 Approach</h2><p>Target elite running backs.</p><h2>Round 2 Approach</h2><p>Look for value WRs.</p>`
	chunks := SplitByHeadings(html, "Draft Strategy 2026")
	if len(chunks) != 2 {
		t.Fatalf("got %d chunks, want 2", len(chunks))
	}
	if chunks[0].Heading != "Round 1 Approach" {
		t.Errorf("chunk[0] heading = %q, want %q", chunks[0].Heading, "Round 1 Approach")
	}
	if chunks[0].Text != "Draft Strategy 2026 > Round 1 Approach Target elite running backs." {
		t.Errorf("chunk[0] text = %q", chunks[0].Text)
	}
	if chunks[1].Heading != "Round 2 Approach" {
		t.Errorf("chunk[1] heading = %q, want %q", chunks[1].Heading, "Round 2 Approach")
	}
	if chunks[1].Text != "Draft Strategy 2026 > Round 2 Approach Look for value WRs." {
		t.Errorf("chunk[1] text = %q", chunks[1].Text)
	}
}

func TestSplitByHeadings_NoHeadings(t *testing.T) {
	html := `<p>Short article with no headings.</p>`
	chunks := SplitByHeadings(html, "Quick Update")
	if len(chunks) != 1 {
		t.Fatalf("got %d chunks, want 1", len(chunks))
	}
	if chunks[0].Heading != "" {
		t.Errorf("heading = %q, want empty", chunks[0].Heading)
	}
	if chunks[0].Text != "Quick Update Short article with no headings." {
		t.Errorf("text = %q", chunks[0].Text)
	}
}

func TestSplitByHeadings_IntroBeforeFirstHeading(t *testing.T) {
	html := `<p>Intro paragraph here.</p><h2>Section A</h2><p>Section A content.</p>`
	chunks := SplitByHeadings(html, "My Article")
	if len(chunks) != 2 {
		t.Fatalf("got %d chunks, want 2", len(chunks))
	}
	if chunks[0].Heading != "" {
		t.Errorf("intro chunk heading = %q, want empty", chunks[0].Heading)
	}
	if chunks[0].Text != "My Article Intro paragraph here." {
		t.Errorf("intro chunk text = %q", chunks[0].Text)
	}
	if chunks[1].Heading != "Section A" {
		t.Errorf("chunk[1] heading = %q, want %q", chunks[1].Heading, "Section A")
	}
}

func TestSplitByHeadings_EmptyHTML(t *testing.T) {
	chunks := SplitByHeadings("", "Title")
	if chunks != nil {
		t.Fatalf("expected nil for empty HTML, got %d chunks", len(chunks))
	}
}

func TestSplitByHeadings_MalformedHTML(t *testing.T) {
	html := `<<<not really html>>>`
	chunks := SplitByHeadings(html, "Broken")
	if len(chunks) != 1 {
		t.Fatalf("got %d chunks, want 1 (fallback)", len(chunks))
	}
	if chunks[0].Heading != "" {
		t.Errorf("heading = %q, want empty", chunks[0].Heading)
	}
}

func TestSplitByHeadings_NestedHeadings(t *testing.T) {
	html := `<h2>Top Section</h2><p>Top content.</p><h3>Sub Section</h3><p>Sub content.</p>`
	chunks := SplitByHeadings(html, "Article")
	if len(chunks) != 2 {
		t.Fatalf("got %d chunks, want 2", len(chunks))
	}
	if chunks[0].Heading != "Top Section" {
		t.Errorf("chunk[0] heading = %q", chunks[0].Heading)
	}
	if chunks[1].Heading != "Sub Section" {
		t.Errorf("chunk[1] heading = %q", chunks[1].Heading)
	}
}

func TestSplitLongChunks_UnderCap(t *testing.T) {
	chunks := []Chunk{{Heading: "H", Text: "short text"}}
	out := SplitLongChunks(chunks)
	if len(out) != 1 || out[0].Text != "short text" {
		t.Fatalf("unchanged chunk should pass through, got %v", out)
	}
}

func TestSplitLongChunks_SentenceSplit(t *testing.T) {
	// Build a chunk with many short sentences that total > MaxChunkChars.
	var b strings.Builder
	b.WriteString("Title ")
	for i := range 500 {
		b.WriteString("This is sentence number ")
		fmt.Fprintf(&b, "%d. ", i)
	}
	chunks := SplitLongChunks([]Chunk{{Heading: "", Text: b.String(), Prefix: "Title"}})
	if len(chunks) < 2 {
		t.Fatalf("expected multiple sub-chunks, got %d", len(chunks))
	}
	for i, c := range chunks {
		if len(c.Text) > MaxChunkChars {
			t.Errorf("sub-chunk %d exceeds cap: %d chars", i, len(c.Text))
		}
		if !strings.HasPrefix(c.Text, "Title ") {
			t.Errorf("sub-chunk %d missing prefix: %q", i, c.Text[:20])
		}
	}
}

func TestSplitLongChunks_HeadingPrefix(t *testing.T) {
	var b strings.Builder
	b.WriteString("Article > Section A ")
	for i := range 500 {
		fmt.Fprintf(&b, "Sentence %d goes here. ", i)
	}
	chunks := SplitLongChunks([]Chunk{{Heading: "Section A", Text: b.String(), Prefix: "Article > Section A"}})
	if len(chunks) < 2 {
		t.Fatalf("expected multiple sub-chunks, got %d", len(chunks))
	}
	for _, c := range chunks {
		if !strings.HasPrefix(c.Text, "Article > Section A ") {
			t.Errorf("sub-chunk missing heading prefix: %q", c.Text[:30])
		}
		if c.Heading != "Section A" {
			t.Errorf("sub-chunk heading = %q, want %q", c.Heading, "Section A")
		}
	}
}

func TestSplitLongChunks_SingleGiantSentence(t *testing.T) {
	long := strings.Repeat("a", MaxChunkChars*2+100)
	chunks := SplitLongChunks([]Chunk{{Heading: "", Text: "Title " + long, Prefix: "Title"}})
	if len(chunks) < 3 {
		t.Fatalf("expected at least 3 sub-chunks for giant run-on, got %d", len(chunks))
	}
	for i, c := range chunks {
		if len(c.Text) > MaxChunkChars {
			t.Errorf("sub-chunk %d exceeds cap: %d chars", i, len(c.Text))
		}
	}
}

func TestSplitByHeadings_LongArticleNoHeadings(t *testing.T) {
	var b strings.Builder
	for i := range 500 {
		fmt.Fprintf(&b, "<p>Paragraph %d with some content here.</p>", i)
	}
	chunks := SplitByHeadings(b.String(), "Long Article")
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks for long headingless article, got %d", len(chunks))
	}
	for i, c := range chunks {
		if len(c.Text) > MaxChunkChars {
			t.Errorf("chunk %d exceeds cap: %d chars", i, len(c.Text))
		}
	}
}
