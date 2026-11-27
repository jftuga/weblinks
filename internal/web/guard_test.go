package web

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestGuardRejectsUnknownHost(t *testing.T) {
	handler := newTestServer(t)

	// A hostile site pointing its own name at 127.0.0.1 would otherwise be
	// treated as same-origin by the browser.
	req := newRequest(http.MethodGet, "/api/bookmarks", "")
	req.Host = "evil.example:5309"
	if rec := send(handler, req); rec.Code != http.StatusForbidden {
		t.Errorf("rebound host: status = %d, want 403", rec.Code)
	}

	req = newRequest(http.MethodGet, "/", "")
	req.Host = "evil.example:5309"
	if rec := send(handler, req); rec.Code != http.StatusForbidden {
		t.Errorf("rebound host on the page: status = %d, want 403", rec.Code)
	}
}

func TestGuardAcceptsLoopbackHosts(t *testing.T) {
	handler := newTestServer(t)
	for _, host := range []string{"127.0.0.1:5309", "localhost:5309", "[::1]:5309", "192.168.1.10:5309"} {
		req := newRequest(http.MethodGet, "/api/bookmarks", "")
		req.Host = host
		if rec := send(handler, req); rec.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200", host, rec.Code)
		}
	}
}

func TestGuardAcceptsAllowedHostName(t *testing.T) {
	store, checker := newTestStore(t)
	opts := testServerOptions()
	opts.AllowHosts = []string{"mac.local"}
	server, err := NewServer(store, checker, opts)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	handler := server.Handler()

	req := newRequest(http.MethodGet, "/api/bookmarks", "")
	req.Host = "mac.local:5309"
	if rec := send(handler, req); rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 for an explicitly allowed host", rec.Code)
	}
}

func TestGuardRejectsCrossSiteRequests(t *testing.T) {
	handler := newTestServer(t)

	for name, tc := range map[string]struct {
		site   string
		origin string
	}{
		"cross-site fetch":  {"cross-site", "https://evil.example"},
		"same-site fetch":   {"same-site", ""},
		"foreign origin":    {"", "https://evil.example"},
		"cross-site create": {"cross-site", ""},
	} {
		t.Run(name, func(t *testing.T) {
			req := newRequest(http.MethodPost, "/api/links",
				`{"url":"https://example.com","title":"T","section":"Main"}`)
			if tc.site != "" {
				req.Header.Set("Sec-Fetch-Site", tc.site)
			} else {
				req.Header.Del("Sec-Fetch-Site")
			}
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if rec := send(handler, req); rec.Code != http.StatusForbidden {
				t.Errorf("status = %d, want 403", rec.Code)
			}
		})
	}
}

// TestGuardRejectsSimpleContentTypes covers the case the browser does not
// stop on its own: a CORS-simple POST is delivered and takes effect even
// though the reply is unreadable, so the content type has to be refused here.
func TestGuardRejectsSimpleContentTypes(t *testing.T) {
	handler := newTestServer(t)
	body := `{"url":"https://evil.example/phish","title":"Account Portal","section":"Main"}`

	for _, contentType := range []string{
		"text/plain;charset=UTF-8",
		"application/x-www-form-urlencoded",
		"multipart/form-data; boundary=x",
		"",
	} {
		req := newRequest(http.MethodPost, "/api/links", body)
		req.Header.Set("Content-Type", contentType)
		if rec := send(handler, req); rec.Code != http.StatusUnsupportedMediaType {
			t.Errorf("%q: status = %d, want 415", contentType, rec.Code)
		}
	}

	// Nothing was created by any of them.
	rec := do(t, handler, http.MethodGet, "/api/bookmarks", "")
	var doc struct {
		Sections []struct{} `json:"sections"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode document: %v", err)
	}
	if len(doc.Sections) != 0 {
		t.Errorf("a refused request still wrote to the store: %s", rec.Body.String())
	}
}

func TestGuardAcceptsJSONWithCharset(t *testing.T) {
	handler := newTestServer(t)
	req := newRequest(http.MethodPost, "/api/links",
		`{"url":"https://example.com","title":"T","section":"Main"}`)
	req.Header.Set("Content-Type", "application/json; charset=utf-8")
	if rec := send(handler, req); rec.Code != http.StatusCreated {
		t.Errorf("status = %d, want 201", rec.Code)
	}
}
