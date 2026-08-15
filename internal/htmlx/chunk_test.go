package htmlx

import (
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
