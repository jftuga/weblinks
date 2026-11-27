package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jftuga/weblinks/internal/bookmarks"
	"github.com/jftuga/weblinks/internal/linkcheck"
)

// newTestStore returns an empty store in a temp dir and a checker to go with
// it.
func newTestStore(t *testing.T) (*bookmarks.Store, *linkcheck.Checker) {
	t.Helper()
	store, err := bookmarks.Open(filepath.Join(t.TempDir(), "weblinks.json"))
	if err != nil {
		t.Fatalf("Open store: %v", err)
	}
	return store, linkcheck.New(linkcheck.Options{})
}

func testServerOptions() Options {
	return Options{
		BaseURL:            "http://127.0.0.1:5309",
		Version:            "test",
		ProgramURL:         "https://example.com",
		ClassificationFile: "../../classification.md",
	}
}

// newTestServer returns a handler backed by an empty store in a temp dir.
func newTestServer(t *testing.T) http.Handler {
	t.Helper()
	store, checker := newTestStore(t)
	server, err := NewServer(store, checker, testServerOptions())
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return server.Handler()
}

// newRequest builds a request the origin guard accepts, which is what the
// page itself sends: this server's own Host, a same-origin fetch and a JSON
// content type on anything that mutates.
func newRequest(method, path, body string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Host = "127.0.0.1:5309"
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	if method != http.MethodGet {
		req.Header.Set("Content-Type", "application/json")
	}
	return req
}

// send runs a prepared request against the handler and returns the recorder.
func send(handler http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

// do issues a request against the handler and returns the recorder.
func do(t *testing.T, handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	return send(handler, newRequest(method, path, body))
}

// createLink posts a bookmark and returns its ID.
func createLink(t *testing.T, handler http.Handler, url, section string) string {
	t.Helper()
	body := `{"url":"` + url + `","title":"T","notes":"n","section":"` + section + `"}`
	rec := do(t, handler, http.MethodPost, "/api/links", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body %s", rec.Code, rec.Body.String())
	}
	var link bookmarks.Link
	if err := json.Unmarshal(rec.Body.Bytes(), &link); err != nil {
		t.Fatalf("decode created link: %v", err)
	}
	return link.ID
}

func TestIndexServesShell(t *testing.T) {
	rec := do(t, newTestServer(t), http.MethodGet, "/", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `id="sections"`) {
		t.Error("expected the sections container in the shell")
	}
}

func TestStaticAssetsAreServed(t *testing.T) {
	handler := newTestServer(t)
	for _, path := range []string{"/static/app.css", "/static/app.js"} {
		rec := do(t, handler, http.MethodGet, path, "")
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200", path, rec.Code)
		}
		if rec.Body.Len() == 0 {
			t.Errorf("%s: empty body", path)
		}
	}
}

func TestBookmarkletHrefSurvivesEscaping(t *testing.T) {
	rec := do(t, newTestServer(t), http.MethodGet, "/bookmarklet", "")
	body := rec.Body.String()
	if strings.Contains(body, "ZgotmplZ") {
		t.Fatal("html/template stripped the javascript: href")
	}

	// Assert against the anchor's href specifically. The page also shows
	// the bookmarklet source in a <pre> block, so a plain substring search
	// over the whole body passes even when the href itself is mangled.
	href := bookmarkletHref(t, body)
	for _, want := range []string{
		"javascript:(function(){window.open(",
		"http://127.0.0.1:5309/add?url=",
		"encodeURIComponent(location.href)",
	} {
		if !strings.Contains(href, want) {
			t.Errorf("href is missing %q\nhref: %s", want, href)
		}
	}
	if strings.ContainsAny(href, "%") {
		t.Errorf("href was percent-encoded and will not run: %s", href)
	}
}

// bookmarkletHref extracts the href of the draggable bookmarklet anchor and
// decodes the HTML entities the attribute is stored with.
func bookmarkletHref(t *testing.T, body string) string {
	t.Helper()
	const marker = `class="bookmarklet-drag" href="`
	start := strings.Index(body, marker)
	if start == -1 {
		t.Fatal("bookmarklet anchor not found in the page")
	}
	start += len(marker)
	end := strings.Index(body[start:], `"`)
	if end == -1 {
		t.Fatal("unterminated href attribute")
	}
	raw := body[start : start+end]
	replacer := strings.NewReplacer("&#39;", "'", "&#34;", `"`, "&amp;", "&")
	return replacer.Replace(raw)
}

func TestAddPagePrefillsFromQuery(t *testing.T) {
	rec := do(t, newTestServer(t), http.MethodGet, "/add?url=https%3A%2F%2Fexample.com&title=Hello", "")
	body := rec.Body.String()
	if !strings.Contains(body, `data-prefill-url="https://example.com"`) {
		t.Error("expected the URL to be prefilled on the body element")
	}
	if !strings.Contains(body, `data-prefill-title="Hello"`) {
		t.Error("expected the title to be prefilled on the body element")
	}
}

func TestAddPageEscapesHostileQuery(t *testing.T) {
	rec := do(t, newTestServer(t), http.MethodGet, `/add?title=%22%3E%3Cscript%3Ealert(1)%3C%2Fscript%3E`, "")
	if strings.Contains(rec.Body.String(), "<script>alert(1)") {
		t.Fatal("query parameter was injected into the page unescaped")
	}
}

func TestCreateUpdateDeleteRoundTrip(t *testing.T) {
	handler := newTestServer(t)
	id := createLink(t, handler, "https://example.com", "Main")

	rec := do(t, handler, http.MethodPut, "/api/links/"+id,
		`{"url":"https://example.org","title":"Moved","notes":"","section":"Archive"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("update: status %d, body %s", rec.Code, rec.Body.String())
	}

	rec = do(t, handler, http.MethodGet, "/api/bookmarks", "")
	var doc struct {
		Sections []struct {
			Name  string `json:"name"`
			Links []struct {
				Title string `json:"title"`
			} `json:"links"`
		} `json:"sections"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode document: %v", err)
	}
	if len(doc.Sections) != 1 || doc.Sections[0].Name != "Archive" {
		t.Fatalf("expected a single Archive section, got %+v", doc.Sections)
	}

	rec = do(t, handler, http.MethodDelete, "/api/links/"+id, "")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete: status %d", rec.Code)
	}
}

func TestRenameSectionEndpoint(t *testing.T) {
	handler := newTestServer(t)
	id := createLink(t, handler, "https://example.com", "Main")

	rec := do(t, handler, http.MethodPatch, "/api/sections", `{"from":"Main","to":"Primary"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("rename: status %d, body %s", rec.Code, rec.Body.String())
	}
	var renamed struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &renamed); err != nil {
		t.Fatalf("decode rename: %v", err)
	}
	if renamed.Name != "Primary" {
		t.Errorf("name = %q, want Primary", renamed.Name)
	}

	rec = do(t, handler, http.MethodGet, "/api/bookmarks", "")
	var doc struct {
		Sections []struct {
			Name  string `json:"name"`
			Links []struct {
				ID string `json:"id"`
			} `json:"links"`
		} `json:"sections"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode document: %v", err)
	}
	if len(doc.Sections) != 1 || doc.Sections[0].Name != "Primary" {
		t.Fatalf("expected a single Primary section, got %+v", doc.Sections)
	}
	if len(doc.Sections[0].Links) != 1 || doc.Sections[0].Links[0].ID != id {
		t.Errorf("the link did not survive the rename: %+v", doc.Sections[0].Links)
	}
}

func TestRenameSectionErrors(t *testing.T) {
	handler := newTestServer(t)
	createLink(t, handler, "https://example.com", "Main")
	createLink(t, handler, "https://example.org", "Archive")

	for name, tc := range map[string]struct {
		body string
		want int
	}{
		"unknown section": {`{"from":"Missing","to":"Other"}`, http.StatusNotFound},
		"name taken":      {`{"from":"Main","to":"Archive"}`, http.StatusConflict},
		"empty target":    {`{"from":"Main","to":"  "}`, http.StatusBadRequest},
		"malformed json":  {`{`, http.StatusBadRequest},
	} {
		t.Run(name, func(t *testing.T) {
			rec := do(t, handler, http.MethodPatch, "/api/sections", tc.body)
			if rec.Code != tc.want {
				t.Errorf("status = %d, want %d; body %s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}
}

func TestSetTitleEndpoint(t *testing.T) {
	handler := newTestServer(t)

	rec := do(t, handler, http.MethodPatch, "/api/title", `{"title":"My Links"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("set title: status %d, body %s", rec.Code, rec.Body.String())
	}

	rec = do(t, handler, http.MethodGet, "/api/bookmarks", "")
	var doc struct {
		Title string `json:"title"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode document: %v", err)
	}
	if doc.Title != "My Links" {
		t.Errorf("title = %q, want My Links", doc.Title)
	}

	for name, body := range map[string]string{
		"blank title":    `{"title":"  "}`,
		"malformed json": `{`,
	} {
		t.Run(name, func(t *testing.T) {
			rec := do(t, handler, http.MethodPatch, "/api/title", body)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400; body %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestDuplicateURLReturnsConflict(t *testing.T) {
	handler := newTestServer(t)
	createLink(t, handler, "https://example.com", "Main")

	rec := do(t, handler, http.MethodPost, "/api/links",
		`{"url":"https://example.com","title":"Again","notes":"","section":"Other"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body %s", rec.Code, rec.Body.String())
	}
}

func TestLookupFindsAnEquivalentURL(t *testing.T) {
	handler := newTestServer(t)
	id := createLink(t, handler, "https://example.com/docs", "Main")

	rec := do(t, handler, http.MethodGet,
		"/api/links/lookup?url="+url.QueryEscape("https://example.com/docs/?utm_source=slack"), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	var link bookmarks.Link
	if err := json.Unmarshal(rec.Body.Bytes(), &link); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if link.ID != id {
		t.Errorf("id = %q, want %q", link.ID, id)
	}

	rec = do(t, handler, http.MethodGet, "/api/links/lookup?url="+url.QueryEscape("https://other.example/"), "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("unbookmarked url: status = %d, want 404", rec.Code)
	}

	rec = do(t, handler, http.MethodGet, "/api/links/lookup", "")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing url: status = %d, want 400", rec.Code)
	}
}

func TestBackupListAndRestore(t *testing.T) {
	handler := newTestServer(t)
	first := createLink(t, handler, "https://example.com/one", "Main")
	createLink(t, handler, "https://example.com/two", "Main")

	if rec := do(t, handler, http.MethodDelete, "/api/links/"+first, ""); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: status %d", rec.Code)
	}

	rec := do(t, handler, http.MethodGet, "/api/backups", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list backups: status %d, body %s", rec.Code, rec.Body.String())
	}
	var backups []bookmarks.BackupInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &backups); err != nil {
		t.Fatalf("decode backups: %v", err)
	}
	if len(backups) == 0 || backups[0].Links != 2 {
		t.Fatalf("expected a newest backup holding both links, got %+v", backups)
	}

	rec = do(t, handler, http.MethodPost, "/api/backups/"+url.PathEscape(backups[0].Name)+"/restore", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("restore: status %d, body %s", rec.Code, rec.Body.String())
	}

	rec = do(t, handler, http.MethodGet, "/api/links/lookup?url="+url.QueryEscape("https://example.com/one"), "")
	if rec.Code != http.StatusOK {
		t.Errorf("the deleted bookmark did not come back: status %d", rec.Code)
	}
}

func TestRestoreRejectsATraversingName(t *testing.T) {
	handler := newTestServer(t)
	createLink(t, handler, "https://example.com/one", "Main")

	rec := do(t, handler, http.MethodPost, "/api/backups/"+url.PathEscape("../weblinks.json")+"/restore", "")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}

	rec = do(t, handler, http.MethodPost, "/api/backups/weblinks-20260101T000000.000000000Z.json/restore", "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("missing backup: status = %d, want 404", rec.Code)
	}
}

func TestUnknownLinkReturnsNotFound(t *testing.T) {
	handler := newTestServer(t)
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodDelete, "/api/links/missing", ""},
		{http.MethodPost, "/api/links/missing/click", ""},
		{http.MethodPut, "/api/links/missing", `{"url":"https://x.example","section":"Main"}`},
	} {
		rec := do(t, handler, tc.method, tc.path, tc.body)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s: status = %d, want 404", tc.method, tc.path, rec.Code)
		}
	}
}

func TestInvalidPayloadReturnsBadRequest(t *testing.T) {
	handler := newTestServer(t)
	for name, body := range map[string]string{
		"malformed json": `{`,
		"missing url":    `{"section":"Main"}`,
		"no scheme":      `{"url":"example.com","section":"Main"}`,
	} {
		t.Run(name, func(t *testing.T) {
			rec := do(t, handler, http.MethodPost, "/api/links", body)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400; body %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestClickUpdatesMetadata(t *testing.T) {
	handler := newTestServer(t)
	id := createLink(t, handler, "https://example.com", "Main")

	rec := do(t, handler, http.MethodPost, "/api/links/"+id+"/click", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var link bookmarks.Link
	if err := json.Unmarshal(rec.Body.Bytes(), &link); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if link.ClickCount != 1 || link.LastClicked == nil {
		t.Errorf("click_count = %d, last_clicked = %v", link.ClickCount, link.LastClicked)
	}
}

func TestCheckSkipsLoopbackAndPersists(t *testing.T) {
	handler := newTestServer(t)
	id := createLink(t, handler, "http://localhost:9/dev", "Main")

	results := runCheck(t, handler)
	if results[id].Category != linkcheck.CategorySkipped {
		t.Errorf("category = %q, want %q for a loopback URL", results[id].Category, linkcheck.CategorySkipped)
	}

	// The outcome must survive into the store, not just the response.
	saved := firstStoredLink(t, handler)
	if saved.LastResult != string(linkcheck.CategorySkipped) || saved.LastChecked == nil {
		t.Errorf("stored result = %q, checked = %v", saved.LastResult, saved.LastChecked)
	}
}

func TestCheckRecordsNothingWhenClientGoesAway(t *testing.T) {
	handler := newTestServer(t)
	rec := do(t, handler, http.MethodPost, "/api/links",
		`{"url":"https://private.example/repo","title":"Private","section":"Main","skip_check":true}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body %s", rec.Code, rec.Body.String())
	}

	// A skipped link gets a result without probing, so a sweep that ignored
	// the cancellation would deterministically stamp it as checked.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	send(handler, newRequest(http.MethodPost, "/api/check", "").WithContext(ctx))

	if saved := firstStoredLink(t, handler); saved.LastChecked != nil {
		t.Errorf("abandoned sweep recorded result %q at %v", saved.LastResult, saved.LastChecked)
	}
}

// firstStoredLink reads the document back through the API and returns the
// first link of the first section.
func firstStoredLink(t *testing.T, handler http.Handler) bookmarks.Link {
	t.Helper()
	rec := do(t, handler, http.MethodGet, "/api/bookmarks", "")
	var doc struct {
		Sections []struct {
			Links []bookmarks.Link `json:"links"`
		} `json:"sections"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode document: %v", err)
	}
	if len(doc.Sections) == 0 || len(doc.Sections[0].Links) == 0 {
		t.Fatalf("no stored links in %s", rec.Body.String())
	}
	return doc.Sections[0].Links[0]
}

func TestCheckHonorsSkipFlag(t *testing.T) {
	handler := newTestServer(t)
	rec := do(t, handler, http.MethodPost, "/api/links",
		`{"url":"https://private.example/repo","title":"Private","section":"Main","skip_check":true}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status %d, body %s", rec.Code, rec.Body.String())
	}
	var link bookmarks.Link
	if err := json.Unmarshal(rec.Body.Bytes(), &link); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !link.SkipCheck {
		t.Fatal("skip_check was not persisted on create")
	}

	results := runCheck(t, handler)
	if results[link.ID].Category != linkcheck.CategorySkipped {
		t.Errorf("category = %q, want %q", results[link.ID].Category, linkcheck.CategorySkipped)
	}
}

// runCheck posts a dead-link sweep and returns the decoded results.
func runCheck(t *testing.T, handler http.Handler) map[string]linkcheck.Result {
	t.Helper()
	rec := do(t, handler, http.MethodPost, "/api/check", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("check: status %d, body %s", rec.Code, rec.Body.String())
	}
	var results map[string]linkcheck.Result
	if err := json.Unmarshal(rec.Body.Bytes(), &results); err != nil {
		t.Fatalf("decode results: %v", err)
	}
	return results
}

func TestMethodNotAllowedOnWrongVerb(t *testing.T) {
	rec := do(t, newTestServer(t), http.MethodGet, "/api/links", "")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", rec.Code)
	}
}

func TestOpenLinkOpensOnlyFileLinks(t *testing.T) {
	store, checker := newTestStore(t)
	server, err := NewServer(store, checker, testServerOptions())
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	var opened []string
	server.open = func(url string) error {
		opened = append(opened, url)
		return nil
	}
	handler := server.Handler()

	fileID := createLink(t, handler, "file:///tmp/my notes.txt", "Main")
	webID := createLink(t, handler, "https://example.com", "Main")

	if rec := do(t, handler, http.MethodPost, "/api/links/"+fileID+"/open", ""); rec.Code != http.StatusNoContent {
		t.Fatalf("open file link: status %d, body %s", rec.Code, rec.Body.String())
	}
	if rec := do(t, handler, http.MethodPost, "/api/links/"+webID+"/open", ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("open web link: status %d, want 400", rec.Code)
	}
	if rec := do(t, handler, http.MethodPost, "/api/links/nope/open", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("open unknown link: status %d, want 404", rec.Code)
	}
	if len(opened) != 1 || opened[0] != "file:///tmp/my%20notes.txt" {
		t.Errorf("opened = %v, want only the file link", opened)
	}
}
