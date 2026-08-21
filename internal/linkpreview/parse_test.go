package linkpreview

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fixture parses a testdata page into the Preview it resolves to. It goes through
// newPreview rather than inspecting the raw tag map, because the fallback chain is
// the part worth pinning down.
func fixture(t *testing.T, name string) Preview {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer func() { _ = f.Close() }()
	return newPreview("https://example.test/page", parseHead(f))
}

func TestParseHeadResolvesPreview(t *testing.T) {
	for _, tc := range []struct {
		file      string
		wantDesc  string
		wantTitle string
		wantSite  string
		wantField Field
	}{{
		file:      "full_og.html",
		wantDesc:  "A colony of grey seals has taken up residence on the east harbour wall, delighting locals and complicating the ferry schedule.",
		wantTitle: "Seals colonise the harbour wall",
		wantSite:  "Example News",
		wantField: FieldOGTitle,
	}, {
		file:      "twitter_only.html",
		wantDesc:  "Predicted high and low water for every port between Ardrossan and Oban.",
		wantTitle: "Tide Tables for the North Coast",
		wantField: FieldTwitterTitle,
	}, {
		// <title> is below every description, so meta description still wins.
		file:      "meta_only.html",
		wantDesc:  "Summer sailings, 3 April to 27 October.",
		wantTitle: "Ferry Timetable",
		wantField: FieldMetaDescription,
	}, {
		// No description anywhere, so the <title> becomes the preview.
		file:      "title_only.html",
		wantTitle: "A page with\nnothing but a title",
		wantField: FieldTitle,
	}, {
		// No head metadata at all, but the body opens with a heading, which is
		// the last thing the chain will fall back to.
		file:      "bare.html",
		wantTitle: "Nothing here",
		wantField: FieldH1,
	}, {
		// Nothing anywhere, not even a heading.
		file:      "empty.html",
		wantField: FieldNone,
	}, {
		// TagAttr unescapes attribute values, and Text() unescapes text.
		file:      "entities.html",
		wantDesc:  `Salt & vinegar, "proper" chips — café open till 9.`,
		wantTitle: "Fish & Chips",
		wantField: FieldOGDescription, // <title> only, so the description still wins
	}, {
		file:      "multiline.html",
		wantDesc:  "A description that the\n    templating engine wrapped across\n    several       lines.",
		wantField: FieldOGDescription,
	}, {
		file:      "both_attrs.html",
		wantDesc:  "Property attribute wins.",
		wantTitle: "Also property.",
		wantField: FieldOGTitle,
	}, {
		file:      "duplicate.html",
		wantDesc:  "The real description.",
		wantField: FieldOGDescription,
	}, {
		// The scan must stop where the body starts, even with no </head>.
		file:      "no_head_close.html",
		wantDesc:  "Head tags without an explicit head element.",
		wantField: FieldOGDescription,
	}} {
		t.Run(tc.file, func(t *testing.T) {
			p := fixture(t, tc.file)
			if p.Desc != tc.wantDesc {
				t.Errorf("Desc = %q, want %q", p.Desc, tc.wantDesc)
			}
			if p.Title != tc.wantTitle {
				t.Errorf("Title = %q, want %q", p.Title, tc.wantTitle)
			}
			if p.SiteName != tc.wantSite {
				t.Errorf("SiteName = %q, want %q", p.SiteName, tc.wantSite)
			}
			if p.Field != tc.wantField {
				t.Errorf("Field = %q, want %q", p.Field, tc.wantField)
			}
		})
	}
}

// The body of no_head_close.html repeats og:description with a value that must
// never be read. TestParseHeadResolvesPreview proves the first one wins; this
// proves it is the head one that won and not merely the earlier duplicate.
func TestParseHeadStopsAtBody(t *testing.T) {
	f, err := os.Open(filepath.Join("testdata", "no_head_close.html"))
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer func() { _ = f.Close() }()

	m := parseHead(f)
	if got := m.tags["og:description"]; got != "Head tags without an explicit head element." {
		t.Errorf("og:description = %q, want the head value", got)
	}
}

func TestParseHeadTruncatedInput(t *testing.T) {
	// A body cut off mid-tag, as maxBody would do: everything before the cut
	// must survive.
	const truncated = `<html><head>` +
		`<title>Kept</title>` +
		`<meta property="og:description" content="Also kept.">` +
		`<meta property="og:im`

	m := parseHead(strings.NewReader(truncated))
	if m.title != "Kept" {
		t.Errorf("title = %q, want %q", m.title, "Kept")
	}
	if got := m.tags["og:description"]; got != "Also kept." {
		t.Errorf("og:description = %q, want %q", got, "Also kept.")
	}
}

func TestParseHeadEmptyContentIgnored(t *testing.T) {
	const doc = `<html><head>` +
		`<meta property="og:description" content="">` +
		`<meta property="og:description" content="The non-empty one.">` +
		`<meta content="orphan with no key">` +
		`</head></html>`

	m := parseHead(strings.NewReader(doc))
	if got := m.tags["og:description"]; got != "The non-empty one." {
		t.Errorf("og:description = %q, want the non-empty value", got)
	}
	if len(m.tags) != 1 {
		t.Errorf("tags = %v, want only og:description", m.tags)
	}
}

// The early-out must not change what a page resolves to: it may only stop
// reading once nothing further could alter the result.
func TestParseHeadStopsOnceMetadataIsComplete(t *testing.T) {
	head := `<html><head>` +
		`<title>Fallback title</title>` +
		`<meta property="og:site_name" content="Example">` +
		`<meta property="og:title" content="Real Title">` +
		`<meta property="og:description" content="Real description.">`
	// Everything past this point must never be read.
	tail := strings.Repeat("<!-- padding -->", 200000) + `</head><body>`

	r := &countingReader{r: strings.NewReader(head + tail)}
	m := parseHead(r)

	if got := m.tags["og:description"]; got != "Real description." {
		t.Errorf("og:description = %q", got)
	}
	if got := m.tags["og:title"]; got != "Real Title" {
		t.Errorf("og:title = %q", got)
	}
	if got := m.tags["og:site_name"]; got != "Example" {
		t.Errorf("og:site_name = %q", got)
	}
	// The padding is 3.2MB; stopping early means reading a tiny fraction of it.
	if r.n > 64<<10 {
		t.Errorf("read %d bytes; expected to stop shortly after the metadata", r.n)
	}
}

// A page missing any of the three keeps scanning, so nothing that a full scan
// would have found is lost.
func TestParseHeadWithoutSiteNameScansOn(t *testing.T) {
	head := `<html><head>` +
		`<meta property="og:title" content="Real Title">` +
		`<meta property="og:description" content="Real description.">` +
		strings.Repeat("<!-- padding -->", 4096) +
		`<meta name="twitter:site" content="@example">` +
		`</head><body>`

	m := parseHead(strings.NewReader(head))
	if got := m.tags["twitter:site"]; got != "@example" {
		t.Errorf("twitter:site = %q; a page without og:site_name must be scanned to </head>", got)
	}
}

// The chain prefers a curated headline to a description, but keeps the bare
// <title> below both — it is the one field no author wrote for sharing.
func TestSummaryPrefersHeadlineOverDescription(t *testing.T) {
	for _, tc := range []struct {
		name      string
		head      string
		wantField Field
		want      string
	}{{
		name:      "og:title beats og:description",
		head:      `<meta property="og:title" content="Seals colonise the wall">` + ogDesc,
		wantField: FieldOGTitle,
		want:      "Seals colonise the wall",
	}, {
		name:      "twitter:title beats og:description",
		head:      `<meta name="twitter:title" content="Seals colonise the wall">` + ogDesc,
		wantField: FieldTwitterTitle,
		want:      "Seals colonise the wall",
	}, {
		name:      "og:title beats twitter:title",
		head:      `<meta name="twitter:title" content="Second"><meta property="og:title" content="First">`,
		wantField: FieldOGTitle,
		want:      "First",
	}, {
		name:      "bare <title> loses to a description",
		head:      `<title>Some Page | Section | Site</title>` + ogDesc,
		wantField: FieldOGDescription,
		want:      "A long paragraph written for a search result.",
	}, {
		name:      "bare <title> is used when nothing else exists",
		head:      `<title>Some Page | Section | Site</title>`,
		wantField: FieldTitle,
		want:      "Some Page | Section | Site",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			p := newPreview("https://example.test/", parseHead(strings.NewReader(
				"<html><head>"+tc.head+"</head><body>")))
			if p.Field != tc.wantField {
				t.Errorf("Field = %q, want %q", p.Field, tc.wantField)
			}
			if got := p.Summary(0); got != tc.want {
				t.Errorf("Summary = %q, want %q", got, tc.want)
			}
		})
	}
}

const ogDesc = `<meta property="og:description" content="A long paragraph written for a search result.">`

// The heading hunt only runs when the head came back empty, and it must not
// mistake a masthead logo for the page's name.
func TestScanForH1(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want string
	}{{
		name: "plain heading",
		body: `<body><h1>D. J. Bernstein</h1>`,
		want: "D. J. Bernstein",
	}, {
		name: "inline markup is flattened",
		body: `<body><h1>The <em>Real</em> Title</h1>`,
		want: "The Real Title",
	}, {
		name: "image-only masthead is skipped for the next heading",
		body: `<body><h1><img src="logo.png"></h1><h1>The Actual Page</h1>`,
		want: "The Actual Page",
	}, {
		name: "no heading at all",
		body: `<body><p>text</p><h2>not an h1</h2>`,
		want: "",
	}, {
		name: "unclosed heading still yields its text",
		body: `<body><h1>Dangling`,
		want: "Dangling",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			m := parseHead(strings.NewReader("<html><head></head>" + tc.body))
			if m.h1 != tc.want {
				t.Errorf("h1 = %q, want %q", m.h1, tc.want)
			}
		})
	}
}

// A page that already said something about itself must not pay for the hunt.
func TestH1NotSoughtWhenHeadHasMetadata(t *testing.T) {
	for _, head := range []string{
		`<title>A Title</title>`,
		`<meta property="og:title" content="A Title">`,
		`<meta name="description" content="A description.">`,
	} {
		r := &countingReader{r: strings.NewReader(
			"<html><head>" + head + "</head><body>" +
				strings.Repeat("<!-- padding -->", 100000) + "<h1>Never Read</h1>")}
		m := parseHead(r)
		if m.h1 != "" {
			t.Errorf("head %q: hunted for an h1 anyway and found %q", head, m.h1)
		}
		if r.n > 64<<10 {
			t.Errorf("head %q: read %d bytes, should have stopped at the body", head, r.n)
		}
	}
}

// The hunt is bounded, so a long page with no heading cannot pull the whole
// body across in search of one.
func TestH1SearchIsBounded(t *testing.T) {
	r := &countingReader{r: strings.NewReader(
		"<html><head></head><body>" +
			strings.Repeat("<p>filler</p>", 200000) + "<h1>Too Late</h1>")}
	m := parseHead(r)
	if m.h1 != "" {
		t.Errorf("h1 = %q, want none — it sits past the search budget", m.h1)
	}
	if r.n > h1SearchBudget*2 {
		t.Errorf("read %d bytes hunting for an h1, budget is %d", r.n, h1SearchBudget)
	}
}
