package web

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/jftuga/weblinks/internal/bookmarks"
	"github.com/jftuga/weblinks/internal/linkcheck"
)

// maxBodyBytes caps request bodies. Bookmark payloads are tiny, so anything
// larger is a mistake or an attack.
const maxBodyBytes = 64 << 10

// handleGetBookmarks returns the whole document as JSON.
func (s *Server) handleGetBookmarks(w http.ResponseWriter, r *http.Request) {
	data, err := s.store.JSON()
	if err != nil {
		serverError(w, "read bookmarks", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(data)
}

// handleLookupLink reports the bookmark already holding a URL, or 404 when
// there is none. The client uses it for the bookmarklet's "edit the existing
// entry" path so that the duplicate rule the page applies is the one the
// store enforces, rather than a second implementation of it in JavaScript.
func (s *Server) handleLookupLink(w http.ResponseWriter, r *http.Request) {
	raw := strings.TrimSpace(r.URL.Query().Get("url"))
	if raw == "" {
		writeError(w, http.StatusBadRequest, "url is required")
		return
	}
	link, err := s.store.Lookup(raw)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, link)
}

// handleCreateLink adds a bookmark, reporting a conflict when the URL is
// already present.
func (s *Server) handleCreateLink(w http.ResponseWriter, r *http.Request) {
	var in bookmarks.LinkInput
	if !decodeBody(w, r, &in) {
		return
	}
	link, err := s.store.Add(in)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, link)
}

// handleUpdateLink replaces a bookmark's editable fields, including the
// section it belongs to.
func (s *Server) handleUpdateLink(w http.ResponseWriter, r *http.Request) {
	var in bookmarks.LinkInput
	if !decodeBody(w, r, &in) {
		return
	}
	link, err := s.store.Update(r.PathValue("id"), in)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, link)
}

// handleDeleteLink removes a bookmark.
func (s *Server) handleDeleteLink(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Delete(r.PathValue("id")); err != nil {
		writeStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleRenameSection renames a section, keeping its links and its position in
// the document. The old and new names travel in the body rather than the path
// so that a section name containing a slash needs no special handling.
func (s *Server) handleRenameSection(w http.ResponseWriter, r *http.Request) {
	var in bookmarks.SectionRename
	if !decodeBody(w, r, &in) {
		return
	}
	name, err := s.store.RenameSection(in)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"name": name})
}

// handleSetTitle replaces the document title and returns the stored value.
func (s *Server) handleSetTitle(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Title string `json:"title"`
	}
	if !decodeBody(w, r, &in) {
		return
	}
	title, err := s.store.SetTitle(in.Title)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"title": title})
}

// handleClickLink records that a bookmark was opened and returns its updated
// click metadata.
func (s *Server) handleClickLink(w http.ResponseWriter, r *http.Request) {
	link, err := s.store.RecordClick(r.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, link)
}

// handleOpenLink opens a file link on this machine. Browsers refuse to follow
// a file URL from an http page, so the page asks the server to do it instead.
// Only a stored link can be opened, by ID, and only when it is a file URL:
// the endpoint must not become a way to launch arbitrary URLs.
func (s *Server) handleOpenLink(w http.ResponseWriter, r *http.Request) {
	link, err := s.store.Get(r.PathValue("id"))
	if err != nil {
		writeStoreError(w, err)
		return
	}
	parsed, err := url.Parse(link.URL)
	if err != nil || !bookmarks.IsFileURL(link.URL) {
		writeError(w, http.StatusBadRequest, "only file links are opened by the server")
		return
	}
	// String re-escapes the path, so a hand-typed space reaches the operating
	// system as %20, which its URL handlers require.
	if err := s.open(parsed.String()); err != nil {
		serverError(w, "open link", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleCheckLinks probes every bookmark and persists the outcome, returning
// the status code recorded for each link ID.
func (s *Server) handleCheckLinks(w http.ResponseWriter, r *http.Request) {
	targets, err := s.store.Targets()
	if err != nil {
		serverError(w, "list bookmarks", err)
		return
	}
	requests := make([]linkcheck.Request, 0, len(targets))
	for _, target := range targets {
		requests = append(requests, linkcheck.Request{Key: target.ID, URL: target.URL, Skip: target.Skip})
	}

	results := s.checker.Run(r.Context(), requests)
	if err := r.Context().Err(); err != nil {
		// The client left mid-sweep. Probes it interrupted report "cancelled",
		// which would overwrite real health data, and nobody is left to read
		// the response, so record nothing.
		log.Printf("check links abandoned: %v", err)
		return
	}

	recorded := make(map[string]bookmarks.CheckResult, len(results))
	for id, result := range results {
		recorded[id] = bookmarks.CheckResult{
			Status: result.Status,
			Result: string(result.Category),
			Detail: result.Detail,
		}
	}
	if err := s.store.RecordCheck(recorded); err != nil {
		serverError(w, "save check results", err)
		return
	}
	writeJSON(w, http.StatusOK, results)
}

// decodeBody reads a JSON request body into dst, writing a 400 response and
// returning false when the body is missing or malformed.
func decodeBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return false
	}
	return true
}

// writeStoreError maps store errors onto HTTP status codes. Unrecognized
// errors are treated as bad input because every store mutation validates the
// caller's data before touching the disk.
func writeStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, bookmarks.ErrNotFound), errors.Is(err, bookmarks.ErrSectionNotFound),
		errors.Is(err, bookmarks.ErrBackupNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, bookmarks.ErrDuplicateURL), errors.Is(err, bookmarks.ErrDuplicateSection):
		writeError(w, http.StatusConflict, err.Error())
	default:
		writeError(w, http.StatusBadRequest, err.Error())
	}
}

// serverError logs an unexpected failure and reports it as a 500.
func serverError(w http.ResponseWriter, action string, err error) {
	log.Printf("%s: %v", action, err)
	writeError(w, http.StatusInternalServerError, err.Error())
}

// writeError sends a JSON error payload the client can surface directly.
func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// writeJSON encodes a value as a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("write response: %v", err)
	}
}
