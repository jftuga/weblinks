// This file holds the request guard. The server has no authentication of any
// kind, so the only thing standing between a hostile web page and the bookmark
// file is the browser's own origin signalling. The guard enforces a
// same-origin policy explicitly rather than assuming one, because a loopback
// address is reachable from every page the user visits.

package web

import (
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// guard wraps a handler with the checks that keep a page on another site from
// driving this server. Three checks apply, in order of what they stop:
//
//   - The Host header must name this server. A hostile site can point its own
//     DNS name at 127.0.0.1, at which point the browser considers it
//     same-origin and every other check passes. Only IP literals, localhost
//     and names the operator allowed explicitly get through.
//   - Requests under /api must not report a cross-origin initiator. This is
//     what stops another page from reaching the API at all.
//   - Mutations must carry a JSON content type. A form post or a text/plain
//     fetch is a CORS-simple request that needs no preflight, so it reaches
//     the handler and takes effect even though the reply is unreadable;
//     application/json forces a preflight, which this server never answers.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.hostAllowed(r.Host) {
			writeError(w, http.StatusForbidden, "unrecognized Host header")
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			if !s.sameOrigin(r) {
				writeError(w, http.StatusForbidden, "cross-origin request refused")
				return
			}
			if mutates(r.Method) && !isJSON(r.Header.Get("Content-Type")) {
				writeError(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// hostAllowed reports whether a Host header value names this server. An IP
// literal is always accepted: rebinding needs a name to re-point, so a literal
// means the browser really did connect to that address. Everything else has to
// be localhost or a name passed to NewServer.
func (s *Server) hostAllowed(host string) bool {
	name := hostname(host)
	if name == "" {
		return false
	}
	if net.ParseIP(name) != nil {
		return true
	}
	return s.allowedHosts[strings.ToLower(name)]
}

// sameOrigin reports whether a request could have come from this server's own
// page. Sec-Fetch-Site is the authoritative signal in current browsers; Origin
// is checked as well for the ones that do not send it. A request with neither
// header is not from a browser at all, so no hostile page can produce it.
func (s *Server) sameOrigin(r *http.Request) bool {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "", "none", "same-origin":
		// Absent means a non-browser client such as curl. "none" means the
		// user typed the address or used a bookmark, which cannot carry a
		// JSON body and so cannot mutate anything.
	default:
		return false
	}

	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return s.hostAllowed(parsed.Host)
}

// hostname strips the port from a Host header value, tolerating an IPv6
// literal and a missing port.
func hostname(host string) string {
	if name, _, err := net.SplitHostPort(host); err == nil {
		return name
	}
	return strings.Trim(host, "[]")
}

// mutates reports whether a method can change server state.
func mutates(method string) bool {
	return method != http.MethodGet && method != http.MethodHead
}

// isJSON reports whether a Content-Type header names JSON, ignoring any
// parameters such as a charset.
func isJSON(value string) bool {
	mediaType, _, err := mime.ParseMediaType(value)
	return err == nil && mediaType == "application/json"
}

// allowedHostSet builds the set of non-literal host names the guard accepts:
// localhost, the host in the server's own base URL, and any extra names the
// operator supplied.
func allowedHostSet(baseURL string, extra []string) map[string]bool {
	allowed := map[string]bool{"localhost": true}
	if parsed, err := url.Parse(baseURL); err == nil {
		if name := parsed.Hostname(); name != "" {
			allowed[strings.ToLower(name)] = true
		}
	}
	for _, name := range extra {
		name = strings.TrimSpace(name)
		if name != "" {
			allowed[strings.ToLower(hostname(name))] = true
		}
	}
	return allowed
}
