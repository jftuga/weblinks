package linkcheck

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// testChecker returns a Checker that probes loopback URLs, which the httptest
// servers used here always are.
func testChecker() *Checker {
	return New(Options{Concurrency: 4, Timeout: 2 * time.Second, CheckLoopback: true})
}

// statusServer returns a server that always replies with the given status.
func statusServer(t *testing.T, status int) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
	}))
	t.Cleanup(server.Close)
	return server
}

// probeOne runs a single request and returns its result.
func probeOne(t *testing.T, url string) Result {
	t.Helper()
	return testChecker().Run(context.Background(), []Request{{Key: "k", URL: url}})["k"]
}

func TestAliveResponse(t *testing.T) {
	got := probeOne(t, statusServer(t, http.StatusOK).URL)
	if got.Category != CategoryAlive {
		t.Errorf("category = %q, want %q", got.Category, CategoryAlive)
	}
	if got.Detail != "" {
		t.Errorf("detail = %q, want empty for a healthy link", got.Detail)
	}
}

func TestAuthStatusesAreNotFailures(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusProxyAuthRequired} {
		got := probeOne(t, statusServer(t, status).URL)
		if got.Category != CategoryAuth {
			t.Errorf("status %d: category = %q, want %q", status, got.Category, CategoryAuth)
		}
		if got.Status != status {
			t.Errorf("status %d: recorded %d", status, got.Status)
		}
	}
}

func TestAmbiguousStatusesAreUnknown(t *testing.T) {
	// A private resource commonly answers 404 to hide its existence, so a
	// 404 must not be reported as definitively dead.
	for _, status := range []int{http.StatusNotFound, http.StatusGone, http.StatusInternalServerError, http.StatusTooManyRequests} {
		got := probeOne(t, statusServer(t, status).URL)
		if got.Category != CategoryUnknown {
			t.Errorf("status %d: category = %q, want %q", status, got.Category, CategoryUnknown)
		}
		if got.Detail == "" {
			t.Errorf("status %d: expected a detail phrase", status)
		}
	}
}

func TestRedirectToSignInIsAuthNotAlive(t *testing.T) {
	login := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer login.Close()

	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, login.URL+"/login?return=/doc", http.StatusFound)
	}))
	defer app.Close()

	got := probeOne(t, app.URL)
	if got.Category != CategoryAuth {
		t.Errorf("category = %q, want %q (detail %q)", got.Category, CategoryAuth, got.Detail)
	}
	if got.FinalURL == "" {
		t.Error("expected the post-redirect URL to be reported")
	}
	if got.Status != http.StatusOK {
		t.Errorf("status = %d, want the final 200", got.Status)
	}
}

func TestOrdinaryRedirectStaysAlive(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/docs/page", http.StatusMovedPermanently)
	}))
	defer source.Close()

	got := probeOne(t, source.URL)
	if got.Category != CategoryAlive {
		t.Errorf("category = %q, want %q", got.Category, CategoryAlive)
	}
}

func TestUnresolvableHostIsDead(t *testing.T) {
	got := probeOne(t, "https://this-host-does-not-exist.invalid/page")
	if got.Category != CategoryDead {
		t.Errorf("category = %q, want %q (detail %q)", got.Category, CategoryDead, got.Detail)
	}
}

func TestRefusedConnectionIsDead(t *testing.T) {
	// Port 1 on loopback is reserved and never listening.
	got := probeOne(t, "http://127.0.0.1:1/gone")
	if got.Category != CategoryDead {
		t.Errorf("category = %q, want %q (detail %q)", got.Category, CategoryDead, got.Detail)
	}
	if got.Detail != "connection refused" {
		t.Errorf("detail = %q, want %q", got.Detail, "connection refused")
	}
}

func TestTimeoutIsUnknownNotDead(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
	}))
	defer slow.Close()

	checker := New(Options{Concurrency: 1, Timeout: 50 * time.Millisecond, CheckLoopback: true})
	got := checker.Run(context.Background(), []Request{{Key: "k", URL: slow.URL}})["k"]
	if got.Category != CategoryUnknown {
		t.Errorf("category = %q, want %q; a timeout does not prove a link is gone", got.Category, CategoryUnknown)
	}
	if got.Detail != "timed out" {
		t.Errorf("detail = %q, want %q", got.Detail, "timed out")
	}
}

func TestSkipRequestIsNotProbed(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
	}))
	defer server.Close()

	got := testChecker().Run(context.Background(), []Request{{Key: "k", URL: server.URL, Skip: true}})["k"]
	if got.Category != CategorySkipped {
		t.Errorf("category = %q, want %q", got.Category, CategorySkipped)
	}
	if hits.Load() != 0 {
		t.Errorf("server was contacted %d times, want 0", hits.Load())
	}
}

func TestRunFallsBackToGET(t *testing.T) {
	var sawGet atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		sawGet.Store(true)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	got := probeOne(t, server.URL)
	if !sawGet.Load() {
		t.Error("expected a GET retry after the HEAD was rejected")
	}
	if got.Category != CategoryAlive {
		t.Errorf("category = %q, want %q", got.Category, CategoryAlive)
	}
}

func TestRunSkipsLoopbackByDefault(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
	}))
	defer server.Close()

	results := New(Options{}).Run(context.Background(), []Request{
		{Key: "ip", URL: server.URL},
		{Key: "name", URL: "http://localhost:9/x"},
	})
	if results["ip"].Category != CategorySkipped || results["name"].Category != CategorySkipped {
		t.Errorf("got %+v, want both skipped", results)
	}
	if hits.Load() != 0 {
		t.Errorf("server was contacted %d times, want 0", hits.Load())
	}
}

func TestRunHonorsConcurrencyLimit(t *testing.T) {
	var inFlight, peak atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current := inFlight.Add(1)
		for {
			seen := peak.Load()
			if current <= seen || peak.CompareAndSwap(seen, current) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		inFlight.Add(-1)
	}))
	defer server.Close()

	requests := make([]Request, 12)
	for i := range requests {
		requests[i] = Request{Key: string(rune('a' + i)), URL: server.URL}
	}

	checker := New(Options{Concurrency: 3, Timeout: 2 * time.Second, CheckLoopback: true})
	checker.Run(context.Background(), requests)

	if peak.Load() > 3 {
		t.Errorf("peak concurrency was %d, want at most 3", peak.Load())
	}
}

func TestRunStopsOnCancelledContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(time.Second)
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	requests := make([]Request, 5)
	for i := range requests {
		requests[i] = Request{Key: string(rune('a' + i)), URL: server.URL}
	}

	done := make(chan map[string]Result, 1)
	go func() { done <- testChecker().Run(ctx, requests) }()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return promptly after cancellation")
	}
}

func TestRunWaitsForInFlightProbesOnCancel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(time.Second):
		}
	}))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	requests := make([]Request, 20)
	for i := range requests {
		requests[i] = Request{Key: string(rune('a' + i)), URL: server.URL}
	}
	checker := New(Options{Concurrency: 4, Timeout: 2 * time.Second, CheckLoopback: true})
	results := checker.Run(ctx, requests)

	// A probe still writing after Run returns would race with the caller
	// reading the map: it grows the map here, and trips `make race`.
	before := len(results)
	time.Sleep(100 * time.Millisecond)
	if after := len(results); after != before {
		t.Errorf("results grew from %d to %d after Run returned", before, after)
	}
}

func TestLooksLikeSignIn(t *testing.T) {
	signIn := []string{
		"https://accounts.google.com/v3/signin/identifier",
		"https://example.com/login",
		"https://example.com/users/sign_in",
		"https://tenant.okta.com/app/start",
		"https://login.microsoftonline.com/common/oauth2",
		"https://example.com/auth/callback",
	}
	for _, url := range signIn {
		if !looksLikeSignIn(url) {
			t.Errorf("looksLikeSignIn(%q) = false, want true", url)
		}
	}

	ordinary := []string{
		"https://docs.example.com/concepts/storage",
		"https://github.com/example/project",
		"https://example.com/authors/jane",
		"https://example.com/",
	}
	for _, url := range ordinary {
		if looksLikeSignIn(url) {
			t.Errorf("looksLikeSignIn(%q) = true, want false", url)
		}
	}
}

func TestStatusDetail(t *testing.T) {
	if got := statusDetail(404); got != "404 Not Found" {
		t.Errorf("statusDetail(404) = %q", got)
	}
	if got := statusDetail(799); got != "HTTP 799" {
		t.Errorf("statusDetail(799) = %q", got)
	}
}

func TestFileLinksAreCheckedOnDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes file.txt")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	existing := (&url.URL{Scheme: "file", Path: path}).String()
	missing := (&url.URL{Scheme: "file", Path: path + ".gone"}).String()

	if got := probeOne(t, existing); got.Category != CategoryAlive {
		t.Errorf("existing file: category = %q, want alive", got.Category)
	}
	if got := probeOne(t, missing); got.Category != CategoryDead {
		t.Errorf("missing file: category = %q, want dead", got.Category)
	}
}
