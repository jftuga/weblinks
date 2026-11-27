// Package bookmarks defines the bookmark data model and the persistent JSON
// store that backs it. The model is a flat list of named sections, each
// holding an ordered list of links carrying click and dead-link-check
// metadata. All persistence concerns live in store.go; this file is types
// and the small amount of validation and lookup logic they need.
package bookmarks

import (
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"
)

// Link is a single bookmark along with its click and health metadata.
type Link struct {
	ID          string     `json:"id"`
	URL         string     `json:"url"`
	Title       string     `json:"title"`
	Notes       string     `json:"notes"`
	Created     time.Time  `json:"created"`
	LastClicked *time.Time `json:"last_clicked"`
	ClickCount  int        `json:"click_count"`
	// SkipCheck excludes the link from dead-link checks, for URLs that
	// cannot be verified without credentials.
	SkipCheck bool `json:"skip_check"`
	// LastChecked is nil until the link has been probed at least once.
	LastChecked *time.Time `json:"last_checked"`
	// LastStatus is the HTTP status code from the last probe, or zero when
	// no response arrived.
	LastStatus int `json:"last_status"`
	// LastResult is the category the last probe fell into, such as "alive",
	// "auth" or "dead".
	LastResult string `json:"last_result"`
	// LastDetail is a short display phrase explaining LastResult.
	LastDetail string `json:"last_detail"`
}

// CheckResult is the outcome of a dead-link probe as recorded against a link.
// It mirrors what the checker produces without importing it, keeping the
// store independent of how probing is implemented.
type CheckResult struct {
	Status int
	Result string
	Detail string
}

// Section is a named, ordered group of links.
type Section struct {
	Name  string  `json:"name"`
	Links []*Link `json:"links"`
}

// Document is the root object persisted to the JSON file.
type Document struct {
	Title    string     `json:"title"`
	Sections []*Section `json:"sections"`
}

// LinkInput carries the caller-supplied fields of a create or update request.
// Section names a target section, which is created on demand when unknown.
type LinkInput struct {
	URL       string `json:"url"`
	Title     string `json:"title"`
	Notes     string `json:"notes"`
	Section   string `json:"section"`
	SkipCheck bool   `json:"skip_check"`
}

// SectionRename carries a request to rename a section. From identifies an
// existing section case-insensitively; To is the name it takes on.
type SectionRename struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// normalize trims whitespace, returning an error when either name is missing.
func (in *SectionRename) normalize() error {
	in.From = strings.TrimSpace(in.From)
	in.To = strings.TrimSpace(in.To)

	if in.From == "" {
		return fmt.Errorf("from is required")
	}
	if in.To == "" {
		return fmt.Errorf("to is required")
	}
	return nil
}

// normalize trims whitespace and applies defaults to a LinkInput, returning an
// error when a required field is missing or the URL is unusable.
func (in *LinkInput) normalize() error {
	in.URL = strings.TrimSpace(in.URL)
	in.Title = strings.TrimSpace(in.Title)
	in.Notes = strings.TrimSpace(in.Notes)
	in.Section = strings.TrimSpace(in.Section)

	if in.URL == "" {
		return fmt.Errorf("url is required")
	}
	parsed, err := url.Parse(in.URL)
	if err != nil {
		return fmt.Errorf("invalid url %q: %w", in.URL, err)
	}
	if isFileURL(parsed) {
		if !strings.HasPrefix(parsed.Path, "/") {
			return fmt.Errorf("invalid url %q: a file url must hold an absolute path", in.URL)
		}
	} else if parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("invalid url %q: must include a scheme and host", in.URL)
	}
	if in.Section == "" {
		return fmt.Errorf("section is required")
	}
	if in.Title == "" {
		in.Title = in.URL
	}
	return nil
}

// IsFileURL reports whether a URL uses the file scheme. Browsers refuse to
// follow such a link from an http page, so the server opens it instead.
func IsFileURL(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && isFileURL(parsed)
}

// isFileURL reports whether an already parsed URL uses the file scheme. A
// file URL names a local path and usually has no host, so it cannot be held to
// the scheme-and-host rule that web URLs are.
func isFileURL(parsed *url.URL) bool {
	return strings.EqualFold(parsed.Scheme, "file")
}

// findLink returns the link with the given ID along with its owning section,
// or nil values when no such link exists.
func (s *Document) findLink(id string) (*Link, *Section) {
	for _, section := range s.Sections {
		for _, link := range section.Links {
			if link.ID == id {
				return link, section
			}
		}
	}
	return nil, nil
}

// findSection returns the section with the given name, or nil when absent.
// Names are compared case-insensitively so "github" matches "Github".
func (s *Document) findSection(name string) *Section {
	for _, section := range s.Sections {
		if strings.EqualFold(section.Name, name) {
			return section
		}
	}
	return nil
}

// findByURL returns the first link whose URL matches, ignoring the link whose
// ID is exceptID. Comparison is on the normalized form, so a trailing slash or
// a tracking parameter does not hide an existing bookmark. It is used for
// duplicate detection on create and update.
func (s *Document) findByURL(rawURL, exceptID string) *Link {
	key := normalizeURL(rawURL)
	for _, section := range s.Sections {
		for _, link := range section.Links {
			if link.ID != exceptID && normalizeURL(link.URL) == key {
				return link
			}
		}
	}
	return nil
}

// clearHealth discards the result of the last dead-link check.
func (l *Link) clearHealth() {
	l.LastChecked = nil
	l.LastStatus = 0
	l.LastResult = ""
	l.LastDetail = ""
}

// removeLink detaches a link from a section, preserving the order of the rest.
// slices.Delete also clears the vacated slot at the end of the backing array,
// so the removed link is not kept reachable from there.
func (sec *Section) removeLink(id string) {
	for i, link := range sec.Links {
		if link.ID == id {
			sec.Links = slices.Delete(sec.Links, i, i+1)
			return
		}
	}
}
