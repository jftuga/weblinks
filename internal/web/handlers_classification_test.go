package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jftuga/weblinks/internal/bookmarks"
)

func classificationServer(t *testing.T, path string) (http.Handler, *bookmarks.Store) {
	t.Helper()
	store, checker := newTestStore(t)
	opts := testServerOptions()
	opts.ClassificationFile = path
	server, err := NewServer(store, checker, opts)
	if err != nil {
		t.Fatal(err)
	}
	return server.Handler(), store
}

// captureClassificationLog uses the standard logger like the handlers do.
// Tests using this helper must not run in parallel.
func captureClassificationLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var output bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&output)
	t.Cleanup(func() { log.SetOutput(previous) })
	return &output
}

func writeClassificationFile(t *testing.T, path, source string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
}

func classificationBody(t *testing.T, handler http.Handler) string {
	t.Helper()
	rec := do(t, handler, http.MethodGet, "/api/classification", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q", got)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
	return rec.Body.String()
}

func TestClassificationPromptReloadsFileAndSections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "custom prompt.md")
	writeClassificationFile(t, path, "First: {{sections}}")
	handler, store := classificationServer(t, path)
	if got := classificationBody(t, handler); got != "First: []" {
		t.Fatalf("empty store prompt = %q", got)
	}
	createLink(t, handler, "https://example.com", "Travel")
	if got := classificationBody(t, handler); got != `First: ["Travel"]` {
		t.Fatalf("added section prompt = %q", got)
	}
	// Same-size edit with the old mtime: read on every request, not only when
	// metadata changes. This also covers editors that preserve timestamps.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	writeClassificationFile(t, path, "Fresh: {{sections}}")
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RenameSection(bookmarks.SectionRename{From: "Travel", To: "Hobbies"}); err != nil {
		t.Fatal(err)
	}
	if got := classificationBody(t, handler); got != `Fresh: ["Hobbies"]` {
		t.Fatalf("updated prompt = %q", got)
	}
	// External store edits are picked up too. No bookmark URLs or notes are
	// needed in the prompt, only the section names in their existing order.
	writeClassificationFile(t, store.Path(), `{"sections":[{"name":"Gardening","links":[]},{"name":"Reading","links":[]}]}`)
	if got := classificationBody(t, handler); got != `Fresh: ["Gardening","Reading"]` {
		t.Fatalf("externally edited sections = %q", got)
	}
}

func TestClassificationPreservesSectionNames(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prompt.md")
	writeClassificationFile(t, path, "{{sections}}")
	handler, store := classificationServer(t, path)
	names := []string{"Art & Design", "<Draft>", "日本語", "Quotes \"and\" backslashes \\", "Line\n### Heading {{sections}}"}
	for i, name := range names {
		_, err := store.Add(bookmarks.LinkInput{URL: "https://example.com/" + string(rune('a'+i)), Section: name, Notes: "private notes"})
		if err != nil {
			t.Fatal(err)
		}
	}
	var got []string
	body := classificationBody(t, handler)
	// JSON round-tripping alone hides HTML escapes. The text shown and copied
	// must retain literal &, < and >, as well as readable Unicode names.
	for _, literal := range []string{"Art & Design", "<Draft>", "日本語"} {
		if !strings.Contains(body, literal) {
			t.Errorf("prompt does not contain literal %q: %s", literal, body)
		}
	}
	if strings.HasSuffix(body, "\n") {
		t.Error("section injection added a trailing newline")
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("section names are not valid JSON: %v", err)
	}
	if len(got) != len(names) {
		t.Fatalf("names = %q", got)
	}
	for i := range names {
		if got[i] != names[i] {
			t.Errorf("name %d = %q, want %q", i, got[i], names[i])
		}
	}
}

func TestClassificationErrorsRecoverOnNextRequest(t *testing.T) {
	for _, failure := range []string{"missing", "directory", "empty", "missing placeholder", "invalid store"} {
		t.Run(failure, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "prompt.md")
			writeClassificationFile(t, path, "Before: {{sections}}")
			handler, store := classificationServer(t, path)
			classificationBody(t, handler)
			switch failure {
			case "missing", "directory":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if failure == "directory" {
					if err := os.Mkdir(path, 0700); err != nil {
						t.Fatal(err)
					}
				}
			case "empty":
				writeClassificationFile(t, path, "")
			case "missing placeholder":
				writeClassificationFile(t, path, "No section placeholder here")
			case "invalid store":
				writeClassificationFile(t, store.Path(), "invalid JSON")
			}
			logged := captureClassificationLog(t)
			rec := do(t, handler, http.MethodGet, "/api/classification", "")
			if rec.Code != http.StatusInternalServerError || rec.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("error status = %d, headers = %v", rec.Code, rec.Header())
			}
			var problem struct{ Error string }
			if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil || problem.Error == "" {
				t.Fatalf("missing error message: %s", rec.Body.String())
			}
			if !strings.Contains(logged.String(), problem.Error) {
				t.Errorf("classification error was not logged: %q", logged.String())
			}
			if rec := do(t, handler, http.MethodGet, "/", ""); rec.Code != http.StatusOK {
				t.Fatal("classification failure prevented serving the UI")
			}
			if failure == "directory" {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			writeClassificationFile(t, path, "Recovered: {{sections}}")
			writeClassificationFile(t, store.Path(), `{"sections":[]}`)
			if got := classificationBody(t, handler); got != "Recovered: []" {
				t.Fatalf("recovered prompt = %q", got)
			}
		})
	}
}

func TestClassificationInvalidAtStartupWarnsAndRecovers(t *testing.T) {
	for _, failure := range []string{"missing", "directory", "empty", "missing placeholder"} {
		t.Run(failure, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "prompt.md")
			switch failure {
			case "directory":
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			case "empty":
				writeClassificationFile(t, path, "")
			case "missing placeholder":
				writeClassificationFile(t, path, "No section placeholder here")
			}
			logged := captureClassificationLog(t)
			handler, _ := classificationServer(t, path)
			startupLog := logged.String()
			if !strings.Contains(startupLog, "warning:") || !strings.Contains(startupLog, path) || !strings.Contains(startupLog, "-classification-file") {
				t.Fatalf("missing startup warning with file path and flag: %q", startupLog)
			}
			if rec := do(t, handler, http.MethodGet, "/", ""); rec.Code != http.StatusOK {
				t.Fatalf("invalid prompt prevented serving the UI: %d", rec.Code)
			}
			rec := do(t, handler, http.MethodGet, "/api/classification", "")
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("invalid prompt status = %d", rec.Code)
			}
			var problem struct{ Error string }
			if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil || problem.Error == "" {
				t.Fatalf("missing error message: %s", rec.Body.String())
			}
			if !strings.Contains(startupLog, problem.Error) {
				t.Errorf("startup did not report the same validation error as the request: %q", startupLog)
			}
			if failure == "directory" {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}
			writeClassificationFile(t, path, "Recovered: {{sections}}")
			if got := classificationBody(t, handler); got != "Recovered: []" {
				t.Fatalf("recovered prompt = %q", got)
			}
		})
	}
}

func TestClassificationAvailableAtStartupDoesNotWarn(t *testing.T) {
	logged := captureClassificationLog(t)
	classificationServer(t, "../../classification.md")
	if strings.Contains(logged.String(), "classification") {
		t.Errorf("unexpected classification startup log: %s", logged.String())
	}
}

func TestClassificationExampleContract(t *testing.T) {
	handler, _ := classificationServer(t, "../../classification.md")
	body := classificationBody(t, handler)
	for _, want := range []string{"[]", "Section:", "Suggested title:", "Suggested notes:", "skip_check: true | false"} {
		if !strings.Contains(body, want) {
			t.Errorf("shipped example missing %q", want)
		}
	}
	if strings.Contains(body, "{{sections}}") {
		t.Error("unresolved section placeholder")
	}
}

func TestClassificationIsNotServedAsStaticAsset(t *testing.T) {
	handler := newTestServer(t)
	if rec := do(t, handler, http.MethodGet, "/static/classification.md", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("old embedded prompt route status = %d, want 404", rec.Code)
	}
}

func TestClassificationRejectsCrossSiteRequests(t *testing.T) {
	handler := newTestServer(t)
	// The dynamic prompt now contains user data and is covered by the API guard.
	req := newRequest(http.MethodGet, "/api/classification", "")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	if rec := send(handler, req); rec.Code != http.StatusForbidden {
		t.Fatalf("cross-site status = %d", rec.Code)
	}
}

type failedClassificationWriter struct {
	*httptest.ResponseRecorder
}

func (w failedClassificationWriter) Write([]byte) (int, error) {
	return 0, errors.New("client disconnected")
}

func TestClassificationLogsResponseWriteFailure(t *testing.T) {
	handler := newTestServer(t)
	logged := captureClassificationLog(t)
	w := failedClassificationWriter{httptest.NewRecorder()}
	handler.ServeHTTP(w, newRequest(http.MethodGet, "/api/classification", ""))
	if !strings.Contains(logged.String(), "write classification response: client disconnected") {
		t.Errorf("response write error was not logged: %q", logged.String())
	}
}
