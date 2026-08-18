package pipeline

import (
	"testing"
	"time"

	"github.com/mmcdole/gofeed"
)

// --- simhash ---

func TestSimhashCompute(t *testing.T) {
	if got := SimhashCompute(""); got != 0 {
		t.Fatalf("empty text must hash to 0, got %d", got)
	}
	a := SimhashCompute("the quick brown fox jumps over the lazy dog")
	b := SimhashCompute("the quick brown fox jumps over the lazy dog")
	if a != b {
		t.Fatal("identical text must produce identical fingerprints")
	}
	// Small edit -> small Hamming distance; unrelated text -> larger.
	closeText := SimhashCompute("the quick brown fox jumps over the lazy dogs")
	if HammingDistance(a, closeText) > 10 {
		t.Fatalf("near-duplicate distance too large: %d", HammingDistance(a, closeText))
	}
	farText := SimhashCompute("completely unrelated words about tax policy and quilting")
	if HammingDistance(a, farText) < 10 {
		t.Fatalf("unrelated distance suspiciously small: %d", HammingDistance(a, farText))
	}
}

func TestHammingDistance(t *testing.T) {
	if got := HammingDistance(0, 0); got != 0 {
		t.Fatalf("equal values: %d", got)
	}
	if got := HammingDistance(0, 7); got != 3 {
		t.Fatalf("0 vs 7: %d", got)
	}
	if got := HammingDistance(-1, 0); got != 64 {
		t.Fatalf("all bits set: %d", got)
	}
}

func TestTokenize(t *testing.T) {
	if got := tokenize(""); got != nil {
		t.Fatalf("empty: %v", got)
	}
	got := tokenize("  Hello   <b>World</b> ")
	if len(got) != 2 || got[0] != "hello" || got[1] != "world" {
		t.Fatalf("tokenize wrong: %v", got)
	}
}

// --- URL / content hashing ---

func TestCanonicalizeURL(t *testing.T) {
	cases := map[string]string{
		"https://Example.com/path/":        "https://example.com/path",
		"http://example.com":               "http://example.com/",
		"https://example.com/a?b=1":        "https://example.com/a?b=1",
		"":                                 "",
		"://bad-url":                       "://bad-url", // unparseable passes through
		"//example.com/x":                  "https://example.com/x",
		"https://example.com/deep/path///": "https://example.com/deep/path",
	}
	for in, want := range cases {
		if got := canonicalizeURL(in); got != want {
			t.Errorf("canonicalizeURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestURLAndContentHash(t *testing.T) {
	// Equivalent URLs hash identically after canonicalization.
	if urlHash("https://a.com/x/") != urlHash("https://a.com/x") {
		t.Fatal("equivalent URLs must hash the same")
	}
	if urlHash("https://a.com/x") == urlHash("https://a.com/y") {
		t.Fatal("distinct URLs must hash differently")
	}
	if got := contentHash(""); got != "" {
		t.Fatalf("empty content hash: %q", got)
	}
	if len(contentHash("text")) != 64 {
		t.Fatalf("content hash must be sha256 hex: %q", contentHash("text"))
	}
}

// --- feed item normalization ---

func feedItem(link, title, content, description string, published *time.Time) *gofeed.Item {
	return &gofeed.Item{Link: link, Title: title, Content: content, Description: description, PublishedParsed: published}
}

func TestExtractAuthor(t *testing.T) {
	if got := extractAuthor(&gofeed.Item{}); got != "" {
		t.Fatalf("no author: %q", got)
	}
	if got := extractAuthor(&gofeed.Item{Author: &gofeed.Person{Name: "Pat"}}); got != "Pat" {
		t.Fatalf("author: %q", got)
	}
	item := &gofeed.Item{Authors: []*gofeed.Person{{Name: "Sam"}}}
	if got := extractAuthor(item); got != "Sam" {
		t.Fatalf("authors fallback: %q", got)
	}
}

func TestExtractContentFields(t *testing.T) {
	html, summary, text := extractContentFields(feedItem("", "t", "<p>Bold <b>move</b></p>", "short desc", nil))
	if html != "<p>Bold <b>move</b></p>" || summary != "short desc" || text != "Bold move" {
		t.Fatalf("content fields wrong: %q %q %q", html, summary, text)
	}
	// No content: description serves as both, summary stays raw.
	html, summary, text = extractContentFields(feedItem("", "t", "", "plain summary", nil))
	if html != "plain summary" || summary != "plain summary" || text != "plain summary" {
		t.Fatalf("description fallback wrong: %q %q %q", html, summary, text)
	}
	// Long content without description: summary derives from stripped content, capped.
	long := "<p>" + repeatChar('x', 600) + "</p>"
	_, summary, _ = extractContentFields(feedItem("", "t", long, "", nil))
	if len(summary) != 500 {
		t.Fatalf("derived summary must cap at 500, got %d", len(summary))
	}
}

func repeatChar(c byte, n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = c
	}
	return string(b)
}

func TestResolvePublished(t *testing.T) {
	if got := resolvePublished(&gofeed.Item{}); got != nil {
		t.Fatalf("no timestamps: %v", got)
	}
	pub := time.Date(2026, 8, 1, 12, 0, 0, 0, time.FixedZone("X", 3600))
	got := resolvePublished(&gofeed.Item{PublishedParsed: &pub})
	if got == nil || got.UTC().Hour() != 11 {
		t.Fatalf("published must win and be UTC: %v", got)
	}
	upd := time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC)
	got = resolvePublished(&gofeed.Item{UpdatedParsed: &upd})
	if got == nil || !got.Equal(upd) {
		t.Fatalf("updated fallback: %v", got)
	}
}

func TestNormalizeFeedItem(t *testing.T) {
	pub := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	item := feedItem("https://example.com/story/", "Big Story", "<p>Some article body text here.</p>", "Some article body text here.", &pub)
	item.GUID = "guid-1"
	n := normalizeFeedItem(item)
	if n.guid != "guid-1" || n.title != "Big Story" {
		t.Fatalf("identity fields wrong: %+v", n)
	}
	if n.cURL != "https://example.com/story" || n.cURLHash == "" || n.cHash == "" {
		t.Fatalf("derived fields wrong: %+v", n)
	}
	if n.published == nil || !n.published.Equal(pub) {
		t.Fatalf("published wrong: %v", n.published)
	}
	if n.bodyStatus != "pending" {
		t.Fatalf("short body with link must be pending: %q", n.bodyStatus)
	}
	// GUID falls back to link; long content marks fetched.
	long := repeatChar('y', 2500)
	n = normalizeFeedItem(feedItem("https://example.com/x", "T", long, "", nil))
	if n.guid != "https://example.com/x" {
		t.Fatalf("guid fallback: %q", n.guid)
	}
	if n.bodyStatus != statusFetched {
		t.Fatalf("long content must be fetched: %q", n.bodyStatus)
	}
	if n.simhash == 0 {
		t.Fatal("simhash must be computed from content")
	}
}

func TestStripHTML(t *testing.T) {
	// Tags collapse to spaces, so punctuation separates from words.
	if got := stripHTML("<p>Hello <b>world</b>!</p>"); got != "Hello world !" {
		t.Fatalf("stripHTML: %q", got)
	}
	if got := stripHTML("  a\t\tb  "); got != "a b" {
		t.Fatalf("whitespace collapse: %q", got)
	}
}

func TestIsEvergreen(t *testing.T) {
	f := NewRSSFetcher(nil, []string{"evergreen.example.com"})
	if !f.isEvergreen("https://evergreen.example.com/post/1") {
		t.Fatal("matching pattern must be evergreen")
	}
	if f.isEvergreen("https://news.example.com/post/1") {
		t.Fatal("non-matching pattern must not be evergreen")
	}
	if f.isEvergreen("") {
		t.Fatal("empty URL must not be evergreen")
	}
}

// --- enrich helpers ---

func TestExtractEntities(t *testing.T) {
	ents := extractEntities("The Philadelphia Eagles signed Saquon Barkley to a big contract.", "Eagles news")
	set := map[string]bool{}
	for _, e := range ents {
		set[e] = true
	}
	if !set["Philadelphia Eagles"] {
		t.Fatalf("team by city must resolve: %v", ents)
	}
	if !set["Saquon Barkley"] {
		t.Fatalf("capitalized name must resolve: %v", ents)
	}
	// Abbreviation form also maps to the full team name.
	ents = extractEntities("KC looks strong this season.", "")
	found := false
	for _, e := range ents {
		if e == "Kansas City Chiefs" {
			found = true
		}
	}
	if !found {
		t.Fatalf("abbr must resolve: %v", ents)
	}
}

func TestExtractTopics(t *testing.T) {
	topics := extractTopics("He is questionable with a hamstring injury.", "depth chart notes")
	set := map[string]bool{}
	for _, tp := range topics {
		set[tp] = true
	}
	if !set["injury"] || !set["depth chart"] {
		t.Fatalf("topics wrong: %v", topics)
	}
	if len(extractTopics("nothing relevant here", "plain words")) != 0 {
		t.Fatal("no keywords must yield no topics")
	}
}

func TestComputeQuality(t *testing.T) {
	cases := []struct {
		n    int
		want float64
	}{
		{6000, 0.9},
		{2000, 0.75},
		{500, 0.5},
		{100, 0.3},
		{10, 0.1},
	}
	for _, tc := range cases {
		text := repeatChar('z', tc.n)
		if got := computeQuality(text, ""); got != tc.want {
			t.Errorf("len %d: got %v, want %v", tc.n, got, tc.want)
		}
	}
	// Empty content falls back to summary length.
	if got := computeQuality("", repeatChar('z', 300)); got != 0.5 {
		t.Fatalf("summary fallback: %v", got)
	}
}

// --- facts helpers ---

func TestParseFactsJSON(t *testing.T) {
	raw := `Here are the facts: [{"fact":"Saquon Barkley was limited in practice.","confidence":"high",` +
		`"occurredAt":null,"entities":["Saquon Barkley"],"topics":["injury"]},{"fact":"","confidence":"low"}] trailing prose`
	facts := parseFactsJSON(raw)
	if len(facts) != 1 {
		t.Fatalf("empty fact strings must be dropped: %d", len(facts))
	}
	if facts[0].Fact == "" || facts[0].Confidence != "high" {
		t.Fatalf("fact wrong: %+v", facts[0])
	}
	if parseFactsJSON("no json here") != nil {
		t.Fatal("no array must yield nil")
	}
	if parseFactsJSON(`{"fact":"not an array"}`) != nil {
		t.Fatal("non-array json must yield nil")
	}
}

func TestFilterValidFacts(t *testing.T) {
	short := "too short"
	ok := "This fact is definitely long enough to count."
	facts := make([]llmFact, maxFactsPerItem+5)
	for i := range facts {
		facts[i] = llmFact{Fact: ok, Confidence: "high"}
	}
	facts[0] = llmFact{Fact: "  " + short + "  "}
	valid := filterValidFacts(facts)
	if len(valid) != maxFactsPerItem {
		t.Fatalf("cap must be %d, got %d", maxFactsPerItem, len(valid))
	}
	if valid[0].text != ok {
		t.Fatalf("valid fact text wrong: %q", valid[0].text)
	}
}

func TestResolveOccurredAt(t *testing.T) {
	pub := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	created := time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC)
	if got := resolveOccurredAt(&llmFact{}, &pub, &created); !got.Equal(pub) {
		t.Fatalf("published must win: %v", got)
	}
	if got := resolveOccurredAt(&llmFact{}, nil, &created); !got.Equal(created) {
		t.Fatalf("created must be second: %v", got)
	}
	llmAt := "2026-07-15T10:00:00Z"
	if got := resolveOccurredAt(&llmFact{OccurredAt: &llmAt}, nil, nil); got.Format(time.RFC3339) != llmAt {
		t.Fatalf("llm value must be third: %v", got)
	}
	bad := "not-a-time"
	if got := resolveOccurredAt(&llmFact{OccurredAt: &bad}, nil, nil); got.IsZero() {
		t.Fatal("bad llm value must fall back to now")
	}
	if got := resolveOccurredAt(&llmFact{}, nil, nil); got.IsZero() {
		t.Fatal("no values must fall back to now")
	}
}

func TestNormalizeConfidence(t *testing.T) {
	for _, ok := range []string{"high", "medium", "low"} {
		got := normalizeConfidence(ok)
		if got == nil || *got != ok {
			t.Fatalf("accepted value %q: %v", ok, got)
		}
	}
	if got := normalizeConfidence("certain"); got != nil {
		t.Fatalf("unaccepted value must be nil: %v", got)
	}
}

// --- stories helpers ---

func TestTruncate(t *testing.T) {
	if got := truncate("short", 10); got != "short" {
		t.Fatalf("short text: %q", got)
	}
	if got := truncate("  a   b  ", 100); got != "a b" {
		t.Fatalf("whitespace collapse: %q", got)
	}
	got := truncate(repeatChar('q', 50), 11)
	if got != repeatChar('q', 10)+"…" {
		t.Fatalf("truncated text must end with ellipsis: %q", got)
	}
}

func TestValidateStory(t *testing.T) {
	good := "This is a perfectly good story summary that is long enough to pass validation checks."
	if got := validateStory(good); got != good {
		t.Fatalf("clean story mangled: %q", got)
	}
	// Code fences and surrounding quotes are stripped.
	if got := validateStory("```json\n" + good + "\n```"); got != good {
		t.Fatalf("code fence stripping: %q", got)
	}
	if got := validateStory(`"` + good + `"`); got != good {
		t.Fatalf("quote stripping: %q", got)
	}
	if got := validateStory("too short"); got != "" {
		t.Fatalf("short story must be rejected: %q", got)
	}
	// List-looking stories are rejected.
	listy := "- point one about something\n- point two about something else\n- point three"
	if got := validateStory(listy); got != "" {
		t.Fatalf("list story must be rejected: %q", got)
	}
	// Over-long stories are capped: 799 chars + 3-byte ellipsis.
	if got := validateStory(repeatChar('s', 900)); len(got) != maxStoryChars+2 {
		t.Fatalf("story cap wrong: %d", len(got))
	}
}

// --- cluster helpers ---

func TestClusterKeyHash(t *testing.T) {
	if len(clusterKeyHash("Some Title", "id")) != 16 {
		t.Fatal("hash must be 16 hex chars")
	}
	once, twice := clusterKeyHash("", "item-1"), clusterKeyHash("", "item-1")
	if once != twice {
		t.Fatal("hash must be deterministic")
	}
	if clusterKeyHash("", "item-1") == clusterKeyHash("", "item-2") {
		t.Fatal("distinct ids must hash differently")
	}
	// Empty title falls back to item id; non-empty title ignores it.
	if clusterKeyHash("T", "a") != clusterKeyHash("T", "b") {
		t.Fatal("same title must hash identically")
	}
}

func TestRoundTo(t *testing.T) {
	if got := roundTo(3.14159, 2); got != 3.14 {
		t.Fatalf("roundTo: %v", got)
	}
	if got := roundTo(2.5, 0); got != 3 {
		t.Fatalf("roundTo 0 places: %v", got)
	}
}

// --- fp_news getStr ---

func TestPipelineGetStr(t *testing.T) {
	if got := getStr(map[string]any{}, "k"); got != "" {
		t.Fatalf("missing: %q", got)
	}
	if got := getStr(map[string]any{"k": nil}, "k"); got != "" {
		t.Fatalf("nil: %q", got)
	}
	if got := getStr(map[string]any{"k": " raw "}, "k"); got != " raw " {
		t.Fatalf("string values pass through untouched: %q", got)
	}
	if got := getStr(map[string]any{"k": 42}, "k"); got != "42" {
		t.Fatalf("numeric: %q", got)
	}
}
