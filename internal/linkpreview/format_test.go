package linkpreview

import "testing"

func TestSummary(t *testing.T) {
	for _, tc := range []struct {
		name   string
		p      Preview
		maxLen int
		want   string
	}{{
		name:   "renders the field that was chosen",
		p:      Preview{Field: FieldOGTitle, Title: "Tide Tables", Desc: "High and low water."},
		maxLen: 80,
		want:   "Tide Tables",
	}, {
		name:   "renders a description when that is what was chosen",
		p:      Preview{Field: FieldOGDescription, Title: "Tide Tables", Desc: "High and low water."},
		maxLen: 80,
		want:   "High and low water.",
	}, {
		// No Field: assembled by hand, so Summary falls back to the same
		// preference the chain encodes — headline first.
		name:   "unset field prefers the title",
		p:      Preview{Title: "Tide Tables", Desc: "High and low water."},
		maxLen: 80,
		want:   "Tide Tables",
	}, {
		name:   "unset field falls through to the description",
		p:      Preview{Desc: "High and low water."},
		maxLen: 80,
		want:   "High and low water.",
	}, {
		name:   "nothing at all stays empty",
		p:      Preview{URL: "https://example.test/"},
		maxLen: 80,
		want:   "",
	}, {
		name:   "collapses wrapped whitespace",
		p:      Preview{Field: FieldOGDescription, Desc: "wrapped\n  across\tlines"},
		maxLen: 80,
		want:   "wrapped across lines",
	}, {
		name:   "non-html names the type and file",
		p:      Preview{URL: "https://example.test/docs/report.pdf", ContentType: "application/pdf"},
		maxLen: 80,
		want:   "PDF: report.pdf",
	}, {
		name:   "non-html without a filename",
		p:      Preview{URL: "https://example.test/generate", ContentType: "application/pdf"},
		maxLen: 80,
		want:   "PDF",
	}, {
		name:   "unknown type falls back to the mime type",
		p:      Preview{URL: "https://example.test/x", ContentType: "application/x-thing"},
		maxLen: 80,
		want:   "application/x-thing",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.p.Summary(tc.maxLen); got != tc.want {
				t.Errorf("Summary(%d) = %q, want %q", tc.maxLen, got, tc.want)
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	for _, tc := range []struct {
		name   string
		in     string
		maxLen int
		want   string
	}{{
		name:   "under the limit is untouched",
		in:     "short",
		maxLen: 20,
		want:   "short",
	}, {
		name:   "exactly at the limit is untouched",
		in:     "exactly ten",
		maxLen: 11,
		want:   "exactly ten",
	}, {
		name:   "cuts at a word boundary",
		in:     "the quick brown fox jumps",
		maxLen: 16,
		want:   "the quick brown…",
	}, {
		name:   "no limit when maxLen is zero",
		in:     "the quick brown fox",
		maxLen: 0,
		want:   "the quick brown fox",
	}, {
		// A single long token has no boundary worth cutting at in the back
		// half, so we take the hard cut rather than throw the line away.
		name:   "hard cut inside a long token",
		in:     "aaaaaaaaaaaaaaaaaaaaaaaa",
		maxLen: 10,
		want:   "aaaaaaaaa…",
	}, {
		// The boundary sits too early to be worth honouring.
		name:   "ignores a boundary in the first half",
		in:     "a bbbbbbbbbbbbbbbbbbbbbb",
		maxLen: 10,
		want:   "a bbbbbbb…",
	}, {
		// Counted in runes, not bytes: 10 multi-byte runes fit under 12.
		name:   "counts runes not bytes",
		in:     "αααααααααα",
		maxLen: 12,
		want:   "αααααααααα",
	}, {
		name:   "degenerate limit",
		in:     "anything",
		maxLen: 1,
		want:   "…",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			got := truncate(tc.in, tc.maxLen)
			if got != tc.want {
				t.Errorf("truncate(%q, %d) = %q, want %q", tc.in, tc.maxLen, got, tc.want)
			}
			if n := len([]rune(got)); tc.maxLen > 0 && n > tc.maxLen {
				t.Errorf("truncate(%q, %d) = %q: %d runes exceeds limit", tc.in, tc.maxLen, got, n)
			}
		})
	}
}

func TestFileName(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"https://example.test/docs/report.pdf", "report.pdf"},
		{"https://example.test/docs/report.pdf?v=2", "report.pdf"},
		{"https://example.test/docs/", ""},
		{"https://example.test/", ""},
		{"https://example.test", ""},
		{"https://example.test/generate", ""},
	} {
		if got := fileName(tc.in); got != tc.want {
			t.Errorf("fileName(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
