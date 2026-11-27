// Package web serves the bookmarks UI and its JSON API. All HTML, CSS and
// JavaScript is compiled into the binary with embed. The classification
// prompt is read from an external file. Page handlers render the shell and
// the browser fetches bookmark data from the API, which keeps rendering and sorting
// logic in one place rather than duplicated across Go templates and the
// client.
package web

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"path/filepath"

	"github.com/jftuga/weblinks/internal/bookmarks"
	"github.com/jftuga/weblinks/internal/launch"
	"github.com/jftuga/weblinks/internal/linkcheck"
)

//go:embed assets
var assetsFS embed.FS

// Server wires the bookmark store and dead-link checker to HTTP handlers.
type Server struct {
	store              *bookmarks.Store
	checker            *linkcheck.Checker
	templates          *template.Template
	static             http.Handler
	baseURL            string
	assetHash          string
	allowedHosts       map[string]bool
	version            string
	programURL         string
	classificationFile string
	// open hands a file link to the operating system. It is a field so that
	// tests can stand in for launch.Open.
	open func(url string) error
}

// Options configures the server's public URL, request guard and external prompt.
type Options struct {
	// BaseURL is the externally reachable origin baked into the bookmarklet.
	BaseURL string
	// AllowHosts lists additional allowed host names; see guard.go.
	AllowHosts []string
	// Version and ProgramURL are shown in the page footer.
	Version    string
	ProgramURL string
	// ClassificationFile is resolved relative to the working directory and
	// read on each classification request, so edits need no restart.
	ClassificationFile string
}

// NewServer builds a Server that serves the given store. An unavailable or invalid
// classification file produces a startup warning but does not prevent serving.
func NewServer(store *bookmarks.Store, checker *linkcheck.Checker, opts Options) (*Server, error) {
	classificationPath, err := filepath.Abs(opts.ClassificationFile)
	if err != nil {
		return nil, fmt.Errorf("resolve classification file: %w", err)
	}
	if _, err := loadClassificationPrompt(classificationPath); err != nil {
		log.Printf("warning: classification prompt unavailable; check -classification-file: %v", err)
	}
	templates, err := template.ParseFS(assetsFS, "assets/templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("parse templates: %w", err)
	}
	staticFS, err := fs.Sub(assetsFS, "assets/static")
	if err != nil {
		return nil, fmt.Errorf("locate static assets: %w", err)
	}
	hash, err := hashFS(staticFS)
	if err != nil {
		return nil, err
	}
	return &Server{
		store:              store,
		checker:            checker,
		templates:          templates,
		static:             http.FileServer(http.FS(staticFS)),
		baseURL:            opts.BaseURL,
		assetHash:          hash,
		allowedHosts:       allowedHostSet(opts.BaseURL, opts.AllowHosts),
		version:            opts.Version,
		programURL:         opts.ProgramURL,
		classificationFile: classificationPath,
		open:               launch.Open,
	}, nil
}

// Handler returns the router for every route the server exposes, behind the
// origin guard.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("GET /add", s.handleAdd)
	mux.HandleFunc("GET /bookmarklet", s.handleBookmarklet)
	mux.Handle("GET /static/", http.StripPrefix("/static/", s.static))

	mux.HandleFunc("GET /api/bookmarks", s.handleGetBookmarks)
	mux.HandleFunc("GET /api/classification", s.handleClassification)
	mux.HandleFunc("GET /api/links/lookup", s.handleLookupLink)
	mux.HandleFunc("POST /api/links", s.handleCreateLink)
	mux.HandleFunc("PUT /api/links/{id}", s.handleUpdateLink)
	mux.HandleFunc("DELETE /api/links/{id}", s.handleDeleteLink)
	mux.HandleFunc("POST /api/links/{id}/click", s.handleClickLink)
	mux.HandleFunc("POST /api/links/{id}/open", s.handleOpenLink)
	mux.HandleFunc("PATCH /api/sections", s.handleRenameSection)
	mux.HandleFunc("PATCH /api/title", s.handleSetTitle)
	mux.HandleFunc("POST /api/check", s.handleCheckLinks)
	mux.HandleFunc("GET /api/backups", s.handleListBackups)
	mux.HandleFunc("POST /api/backups/{name}/restore", s.handleRestoreBackup)
	return s.guard(mux)
}

// hashFS returns a short digest of every embedded static file, used to
// version asset URLs so a rebuilt binary is never served stale CSS or JS from
// the browser cache.
func hashFS(fsys fs.FS) (string, error) {
	digest := sha256.New()
	err := fs.WalkDir(fsys, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := fs.ReadFile(fsys, path)
		if err != nil {
			return err
		}
		digest.Write([]byte(path))
		digest.Write(data)
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("hash static assets: %w", err)
	}
	return hex.EncodeToString(digest.Sum(nil))[:12], nil
}
