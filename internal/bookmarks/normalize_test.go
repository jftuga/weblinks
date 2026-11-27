package bookmarks

import (
	"path/filepath"
	"testing"
)

func TestNormalizeURLTreatsCosmeticDifferencesAsEqual(t *testing.T) {
	const canonical = "https://example.com/docs/page"

	for name, raw := range map[string]string{
		"trailing slash":   "https://example.com/docs/page/",
		"uppercase host":   "https://EXAMPLE.com/docs/page",
		"uppercase scheme": "HTTPS://example.com/docs/page",
		"default port":     "https://example.com:443/docs/page",
		"fragment":         "https://example.com/docs/page#section-2",
		"utm parameters":   "https://example.com/docs/page?utm_source=slack&utm_medium=chat",
		"click id":         "https://example.com/docs/page?fbclid=abc123",
	} {
		t.Run(name, func(t *testing.T) {
			if got, want := normalizeURL(raw), normalizeURL(canonical); got != want {
				t.Errorf("normalizeURL(%q) = %q, want %q", raw, got, want)
			}
		})
	}
}

func TestNormalizeURLKeepsMeaningfulDifferences(t *testing.T) {
	for name, pair := range map[string][2]string{
		"scheme":         {"http://example.com/a", "https://example.com/a"},
		"host":           {"https://example.com/a", "https://example.org/a"},
		"path":           {"https://example.com/a", "https://example.com/b"},
		"non-default":    {"https://example.com/a", "https://example.com:8443/a"},
		"query value":    {"https://example.com/a?id=1", "https://example.com/a?id=2"},
		"real parameter": {"https://example.com/a", "https://example.com/a?id=1"},
	} {
		t.Run(name, func(t *testing.T) {
			if normalizeURL(pair[0]) == normalizeURL(pair[1]) {
				t.Errorf("%q and %q normalized to the same key", pair[0], pair[1])
			}
		})
	}
}

func TestNormalizeURLSortsQueryParameters(t *testing.T) {
	if normalizeURL("https://example.com/a?b=2&a=1") != normalizeURL("https://example.com/a?a=1&b=2") {
		t.Error("parameter order changed the comparison key")
	}
}

func TestNormalizeURLHandlesIPv6AndUnparsableInput(t *testing.T) {
	if got, want := normalizeURL("https://[::1]:443/a"), "https://[::1]/a"; got != want {
		t.Errorf("normalizeURL = %q, want %q", got, want)
	}
	if got, want := normalizeURL("  Not A URL  "), "not a url"; got != want {
		t.Errorf("normalizeURL = %q, want %q", got, want)
	}
}

func TestNormalizeURLKeepsFilePathCase(t *testing.T) {
	if got, want := normalizeURL("FILE:///Users/Me/Notes.txt#top"), "file:///Users/Me/Notes.txt"; got != want {
		t.Errorf("normalizeURL = %q, want %q", got, want)
	}
}

func TestAddRejectsCosmeticDuplicate(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "weblinks.json"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := store.Add(LinkInput{URL: "https://example.com/docs", Title: "Docs", Section: "Main"}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	_, err = store.Add(LinkInput{URL: "https://example.com/docs/?utm_source=slack", Title: "Docs again", Section: "Other"})
	if err == nil {
		t.Fatal("expected a duplicate to be refused")
	}

	// The same URL has to be findable by lookup, which is what the client
	// uses to turn a duplicate add into an edit.
	link, err := store.Lookup("https://example.com/docs/#intro")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if link.Title != "Docs" {
		t.Errorf("Lookup returned %q, want the existing bookmark", link.Title)
	}
}
