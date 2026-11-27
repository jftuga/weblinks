// Package linkcheck probes bookmark URLs and sorts the outcome into a small
// set of categories. An anonymous probe cannot tell whether an authenticated
// page still exists, so results distinguish links that are definitely gone
// (the host does not resolve, the connection is refused) from ones that are
// merely unverifiable without credentials. Only the former is worth showing
// as an error.
package linkcheck

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Category classifies the outcome of a probe.
type Category string

const (
	// CategoryAlive means the URL responded successfully.
	CategoryAlive Category = "alive"
	// CategoryAuth means the URL exists but cannot be verified without
	// credentials, either because it demanded them or because it redirected
	// to a sign-in page.
	CategoryAuth Category = "auth"
	// CategoryDead means the URL is definitively unreachable: the host does
	// not resolve, or it actively refused the connection.
	CategoryDead Category = "dead"
	// CategoryUnknown covers everything ambiguous: 404 (which private
	// resources return to hide their existence), server errors, timeouts
	// and transport failures that do not prove the link is gone.
	CategoryUnknown Category = "unknown"
	// CategorySkipped means no probe was attempted.
	CategorySkipped Category = "skipped"
)

// userAgent identifies the checker. Some sites reject requests without a
// browser-like agent outright.
const userAgent = "Mozilla/5.0 (compatible; weblinks/1.0)"

// Checker probes URLs with a fixed concurrency limit and per-request timeout.
type Checker struct {
	client        *http.Client
	concurrency   int
	checkLoopback bool
}

// Options configures a Checker. Zero values select the defaults of eight
// concurrent probes, a ten second per-request timeout, and skipping loopback
// URLs.
type Options struct {
	// Concurrency is the maximum number of simultaneous probes.
	Concurrency int
	// Timeout bounds each individual HTTP request.
	Timeout time.Duration
	// CheckLoopback probes localhost URLs instead of skipping them. The
	// default is to skip, because a local development server being down
	// says nothing about whether the bookmark is still valid.
	CheckLoopback bool
}

// New returns a Checker using the supplied options. The underlying client
// does not follow more than ten redirects, the net/http default.
func New(opts Options) *Checker {
	if opts.Concurrency <= 0 {
		opts.Concurrency = 8
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 10 * time.Second
	}
	return &Checker{
		client:        &http.Client{Timeout: opts.Timeout},
		concurrency:   opts.Concurrency,
		checkLoopback: opts.CheckLoopback,
	}
}

// Request pairs a caller-chosen key with the URL to probe. The key is echoed
// back in the results so callers can match outcomes to their own records.
type Request struct {
	Key string
	URL string
	// Skip suppresses the probe for links the caller knows cannot be
	// verified anonymously.
	Skip bool
}

// Result is the outcome of a single probe.
type Result struct {
	// Status is the HTTP status code, or zero when no response arrived.
	Status int `json:"status"`
	// Category is the bucket this outcome falls into.
	Category Category `json:"category"`
	// Detail is a short phrase suitable for display, empty when the link
	// is healthy.
	Detail string `json:"detail"`
	// FinalURL is where the request ended up after redirects, when that
	// differs from the URL probed.
	FinalURL string `json:"final_url,omitempty"`
}

// Run probes every request and returns a result per key. If ctx is cancelled
// it stops starting probes and returns once the ones in flight, which abort on
// the same context, have finished. Waiting for them is what keeps the returned
// map from being written after Run hands it to the caller.
func (c *Checker) Run(ctx context.Context, requests []Request) map[string]Result {
	results := make(map[string]Result, len(requests))
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, c.concurrency)

loop:
	for _, req := range requests {
		if req.Skip {
			results[req.Key] = Result{Category: CategorySkipped, Detail: "checking disabled"}
			continue
		}

		select {
		case <-ctx.Done():
			break loop
		case sem <- struct{}{}:
		}

		wg.Add(1)
		go func(req Request) {
			defer wg.Done()
			defer func() { <-sem }()
			result := c.probe(ctx, req.URL)
			mu.Lock()
			results[req.Key] = result
			mu.Unlock()
		}(req)
	}

	wg.Wait()
	return results
}

// probe resolves a single URL, falling back from HEAD to GET when the server
// rejects the method.
func (c *Checker) probe(ctx context.Context, rawURL string) Result {
	if path, ok := filePath(rawURL); ok {
		return statFile(path)
	}
	if !c.checkLoopback && isLoopback(rawURL) {
		return Result{Category: CategorySkipped, Detail: "local address"}
	}
	result := c.request(ctx, http.MethodHead, rawURL)
	if result.Status == http.StatusMethodNotAllowed || result.Status == http.StatusNotImplemented {
		return c.request(ctx, http.MethodGet, rawURL)
	}
	return result
}

// filePath returns the local path a file URL names, reporting false for any
// other kind of URL.
func filePath(rawURL string) (string, bool) {
	parsed, err := url.Parse(rawURL)
	if err != nil || !strings.EqualFold(parsed.Scheme, "file") {
		return "", false
	}
	return parsed.Path, true
}

// statFile checks a file link by looking on disk instead of over HTTP. A
// missing file is dead; any other failure, such as a permission error, does
// not prove the file is gone.
func statFile(path string) Result {
	_, err := os.Stat(path)
	switch {
	case err == nil:
		return Result{Category: CategoryAlive, Detail: "file exists"}
	case errors.Is(err, os.ErrNotExist):
		return Result{Category: CategoryDead, Detail: "file not found"}
	default:
		return Result{Category: CategoryUnknown, Detail: "file not readable"}
	}
}

// request performs one HTTP request and classifies what came back.
func (c *Checker) request(ctx context.Context, method, rawURL string) Result {
	req, err := http.NewRequestWithContext(ctx, method, rawURL, nil)
	if err != nil {
		return Result{Category: CategoryDead, Detail: "malformed URL"}
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := c.client.Do(req)
	if err != nil {
		return classifyError(err)
	}
	defer resp.Body.Close()

	// resp.Request is the last request in the redirect chain, so its URL is
	// where the probe actually landed.
	final := ""
	if resp.Request != nil && resp.Request.URL != nil {
		final = resp.Request.URL.String()
	}
	return classifyResponse(resp.StatusCode, rawURL, final)
}

// classifyResponse buckets an HTTP status code, treating a redirect that
// lands on a sign-in page as proof the link needs credentials rather than
// proof it works.
func classifyResponse(status int, original, final string) Result {
	result := Result{Status: status}
	if final != "" && final != original {
		result.FinalURL = final
	}

	switch {
	case status == http.StatusUnauthorized,
		status == http.StatusForbidden,
		status == http.StatusProxyAuthRequired:
		result.Category = CategoryAuth
		result.Detail = "sign-in required"
	case status >= 200 && status < 300:
		if result.FinalURL != "" && looksLikeSignIn(result.FinalURL) {
			result.Category = CategoryAuth
			result.Detail = "redirected to sign-in"
		} else {
			result.Category = CategoryAlive
		}
	default:
		result.Category = CategoryUnknown
		result.Detail = statusDetail(status)
	}
	return result
}

// classifyError buckets a transport failure. Only a missing DNS record or a
// refused connection is treated as proof the link is dead; timeouts and TLS
// problems are left ambiguous because they are just as likely to mean the
// machine is off a VPN or behind an inspecting proxy.
func classifyError(err error) Result {
	var dnsErr *net.DNSError

	switch {
	case errors.Is(err, context.Canceled):
		return Result{Category: CategorySkipped, Detail: "cancelled"}
	case errors.As(err, &dnsErr):
		if dnsErr.IsNotFound {
			return Result{Category: CategoryDead, Detail: "no DNS record"}
		}
		return Result{Category: CategoryUnknown, Detail: "DNS lookup failed"}
	case errors.Is(err, syscall.ECONNREFUSED):
		return Result{Category: CategoryDead, Detail: "connection refused"}
	case isTimeout(err):
		return Result{Category: CategoryUnknown, Detail: "timed out"}
	case isTLSError(err):
		return Result{Category: CategoryUnknown, Detail: "TLS error"}
	default:
		return Result{Category: CategoryUnknown, Detail: "network error"}
	}
}

// isTimeout reports whether an error was caused by exceeding a deadline.
func isTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

// isTLSError reports whether an error came from certificate verification or
// the TLS handshake.
func isTLSError(err error) bool {
	var verifyErr *tls.CertificateVerificationError
	var recordErr tls.RecordHeaderError
	var hostErr x509.HostnameError
	var authorityErr x509.UnknownAuthorityError
	var invalidErr x509.CertificateInvalidError
	return errors.As(err, &verifyErr) ||
		errors.As(err, &recordErr) ||
		errors.As(err, &hostErr) ||
		errors.As(err, &authorityErr) ||
		errors.As(err, &invalidErr)
}

// signInHosts and signInPaths are substrings that mark a URL as a sign-in
// page. This is a heuristic: it catches the common identity providers and
// path conventions, and will miss bespoke login flows.
var signInHosts = []string{
	"accounts.google.com",
	"login.",
	"signin.",
	"auth.",
	"sso.",
	"okta.com",
	"auth0.com",
	"onelogin.com",
	"microsoftonline.com",
	"duosecurity.com",
}

var signInPaths = []string{
	"/login",
	"/log-in",
	"/signin",
	"/sign-in",
	"/sign_in",
	"/sso",
	"/saml",
	"/oauth",
	"/session/new",
	"/auth/",
	"/accounts/",
}

// looksLikeSignIn reports whether a URL appears to be an authentication page.
func looksLikeSignIn(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	for _, token := range signInHosts {
		if strings.Contains(host, token) {
			return true
		}
	}
	path := strings.ToLower(parsed.Path)
	for _, token := range signInPaths {
		if strings.Contains(path, token) {
			return true
		}
	}
	return false
}

// statusDetail renders a status code as a short display phrase.
func statusDetail(status int) string {
	if text := http.StatusText(status); text != "" {
		return fmt.Sprintf("%d %s", status, text)
	}
	return fmt.Sprintf("HTTP %d", status)
}

// isLoopback reports whether a URL points at the local machine.
func isLoopback(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := parsed.Hostname()
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
