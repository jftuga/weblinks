// This file reduces a URL to a comparison key so that duplicate detection is
// not defeated by cosmetic differences. A URL copied from the address bar
// carries a trailing slash, a share link carries tracking parameters and a
// pasted link may differ only in letter case, yet all three name the same
// page. The original string is always what gets stored; normalization exists
// only to answer "is this already bookmarked".

package bookmarks

import (
	"net/url"
	"strings"
)

// trackingParams are query parameters that record how a link was shared
// rather than what it points at. Two URLs differing only in these name the
// same page. Anything beginning with "utm_" is dropped as well.
var trackingParams = map[string]bool{
	"fbclid":  true,
	"gclid":   true,
	"dclid":   true,
	"gbraid":  true,
	"wbraid":  true,
	"msclkid": true,
	"yclid":   true,
	"igshid":  true,
	"mc_cid":  true,
	"mc_eid":  true,
	"ref_src": true,
	"ref_url": true,
	"_hsenc":  true,
	"_hsmi":   true,
}

// defaultPorts maps a scheme to the port it implies, which is redundant in a
// URL and therefore removed before comparison.
var defaultPorts = map[string]string{
	"http":  "80",
	"https": "443",
}

// normalizeURL returns a comparison key for a URL. The scheme and host are
// lowercased, a redundant default port and the fragment are dropped, a
// trailing slash is removed, tracking parameters are stripped and the
// remaining query is sorted. A URL that will not parse is compared
// case-insensitively as plain text, which is no worse than the exact match it
// replaces. A file URL has no host but is still normalized structurally, so
// that the case of its path, which may matter to the filesystem, is kept.
func normalizeURL(raw string) string {
	raw = strings.TrimSpace(raw)
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Host == "" && !isFileURL(parsed)) {
		return strings.ToLower(raw)
	}

	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = stripDefaultPort(parsed.Scheme, strings.ToLower(parsed.Host))
	parsed.Fragment = ""
	parsed.RawFragment = ""
	parsed.Path = strings.TrimSuffix(parsed.Path, "/")
	// RawPath only holds an alternate encoding of Path, and trimming has just
	// invalidated it. Clearing it makes String re-escape from Path.
	parsed.RawPath = ""
	parsed.RawQuery = cleanQuery(parsed.RawQuery)
	parsed.ForceQuery = false

	return parsed.String()
}

// stripDefaultPort removes the port from a host when the scheme already
// implies it, re-bracketing an IPv6 literal so the result stays a valid host.
func stripDefaultPort(scheme, host string) string {
	implied, ok := defaultPorts[scheme]
	if !ok {
		return host
	}
	index := strings.LastIndex(host, ":")
	if index == -1 || strings.HasSuffix(host, "]") {
		return host
	}
	if host[index+1:] != implied {
		return host
	}
	// host[:index] keeps the brackets of an IPv6 literal, so the result is
	// still a valid host on its own.
	return host[:index]
}

// cleanQuery drops tracking parameters and sorts what is left, so that
// parameter order alone cannot make one URL look unlike another.
func cleanQuery(raw string) string {
	if raw == "" {
		return ""
	}
	values, err := url.ParseQuery(raw)
	if err != nil {
		return raw
	}
	for key := range values {
		lower := strings.ToLower(key)
		if trackingParams[lower] || strings.HasPrefix(lower, "utm_") {
			delete(values, key)
		}
	}
	// Encode sorts by key, which is what makes the result order-independent.
	return values.Encode()
}
