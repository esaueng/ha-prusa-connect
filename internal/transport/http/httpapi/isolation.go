package httpapi

import (
	"net/http"
	"net/url"
	"strings"
)

// The two headers that make a page cross-origin isolated. Only the entry
// document of an isolated printer tab carries them.
const (
	openerPolicyHeader   = "Cross-Origin-Opener-Policy"
	openerPolicy         = "same-origin"
	embedderPolicyHeader = "Cross-Origin-Embedder-Policy"
	embedderPolicy       = "require-corp"
)

// setCrossOriginIsolation adds both headers to an answer.
func setCrossOriginIsolation(header http.Header) {
	header.Set(openerPolicyHeader, openerPolicy)
	header.Set(embedderPolicyHeader, embedderPolicy)
}

// printerTabPrefix is the first segment of a printer's page in the app's
// router: /printers/<device id>/<tab>.
const printerTabPrefix = "printers"

// isolatedTabPath reports whether an escaped request path is the address of a
// module's printer tab that asks for an isolated page:
// /printers/<device id>/<namespace>.<tab id>. The path is split as escaped,
// because a device id may hold %2F. A tab id has no dot and a namespace has
// at least one, so the namespace ends at the last dot.
func isolatedTabPath(escaped string, isolatedTab func(namespace, tab string) bool) bool {
	if isolatedTab == nil {
		return false
	}

	segments := strings.Split(strings.TrimPrefix(escaped, "/"), "/")
	if len(segments) != 3 || segments[0] != printerTabPrefix || segments[1] == "" {
		return false
	}

	last, err := url.PathUnescape(segments[2])
	if err != nil {
		return false
	}

	at := strings.LastIndexByte(last, '.')
	if at <= 0 || at == len(last)-1 {
		return false
	}

	namespace, tab := last[:at], last[at+1:]
	if !strings.Contains(namespace, ".") || strings.Contains(tab, "/") {
		return false
	}

	return isolatedTab(namespace, tab)
}
