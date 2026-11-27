package web

import (
	"bytes"
	"fmt"
	"html/template"
	"log"
	"net/http"
)

// pageData is the template context for the application shell. Prefill fields
// are populated only on the /add route, where the bookmarklet hands over the
// page it was invoked from.
type pageData struct {
	AssetHash    string
	BaseURL      string
	Version      string
	ProgramURL   string
	PrefillURL   string
	PrefillTitle string
	Bookmarklet  template.HTMLAttr
}

// bookmarkletSource is the body of the add-current-page bookmarklet. It opens
// the /add route in a small window rather than posting to the API directly,
// which keeps every request same-origin and leaves the API unreachable from
// the pages the bookmarklet runs on.
const bookmarkletSource = `javascript:(function(){window.open('%s/add?url='+encodeURIComponent(location.href)+'&title='+encodeURIComponent(document.title),'weblinks_add','width=640,height=680');})();`

// basePage returns the template context shared by every page.
func (s *Server) basePage() pageData {
	return pageData{
		AssetHash:  s.assetHash,
		BaseURL:    s.baseURL,
		Version:    s.version,
		ProgramURL: s.programURL,
	}
}

// handleIndex serves the application shell.
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	s.renderPage(w, "index.html", s.basePage())
}

// handleAdd serves the shell with the add dialog pre-filled from the query
// string. This is the landing page for the bookmarklet, which avoids
// cross-origin requests by navigating here instead of posting directly.
func (s *Server) handleAdd(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	data := s.basePage()
	data.PrefillURL = query.Get("url")
	data.PrefillTitle = query.Get("title")
	s.renderPage(w, "index.html", data)
}

// handleBookmarklet serves the installation page for the "add current page"
// bookmarklet, with this server's address baked into the generated link.
func (s *Server) handleBookmarklet(w http.ResponseWriter, r *http.Request) {
	// The whole href attribute is emitted as a unit. template.URL is not
	// enough: it clears the autoescaper's javascript: check but still runs
	// the URL normalizer, which percent-encodes the parentheses and braces
	// and leaves an unusable bookmarklet. The only interpolated value is
	// this server's own address, which never comes from a request.
	source := fmt.Sprintf(bookmarkletSource, s.baseURL)
	data := s.basePage()
	data.Bookmarklet = template.HTMLAttr(`href="` + template.HTMLEscapeString(source) + `"`)
	s.renderPage(w, "bookmarklet.html", data)
}

// renderPage executes a template into a buffer before writing it, so that a
// template error surfaces as a 500 rather than a half-written page.
func (s *Server) renderPage(w http.ResponseWriter, name string, data pageData) {
	var buf bytes.Buffer
	if err := s.templates.ExecuteTemplate(&buf, name, data); err != nil {
		log.Printf("render %s: %v", name, err)
		http.Error(w, "template error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	buf.WriteTo(w)
}
