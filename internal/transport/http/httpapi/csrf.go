package httpapi

import (
	"net/http"
	"strings"

	"github.com/prusa3d/prusa-connect/internal/authz"
	"github.com/prusa3d/prusa-connect/sdk/fault"
)

// prusaAccountCookie is the cookie the Prusa Connect web client keeps its
// Prusa Account token in. The constant belongs to internal/platform/auth, which
// this package may not import, so the name is written out here.
const prusaAccountCookie = "auth.access_token"

// bearerPrefix opens an Authorization header that names a credential on its own.
// A browser never adds it to a request by itself, so a request carrying it was
// written by a script that already held the token.
const bearerPrefix = "bearer "

// crossSiteMiddleware refuses a write a browser sent from another site on the
// strength of a cookie alone. That is the shape of a cross-site request
// forgery: a page elsewhere submits a form or calls fetch, and the browser
// attaches the cookie the person signed in with.
//
// The browser says where a request came from in Sec-Fetch-Site, and a page
// cannot set or change that header. The refusal needs every one of these:
//
//   - the header says cross-site. An absent header is a client that is not a
//     browser, or a browser too old to send it, and goes through
//   - the method writes. GET, HEAD and OPTIONS change nothing
//   - a credential cookie rides the request and no Bearer header does. A
//     Bearer header wins over a cookie wherever identity is resolved, and a
//     forged request cannot carry one
//   - the Origin is not one the operator allowed. A page on an allowed origin
//     is the operator's own frontend, which CORS already admits
//
// The refusal reuses core.auth/ACCESS_DENIED, so a client meets a code the
// error reference already documents.
func crossSiteMiddleware(sessionCookie string, origins []string) func(http.Handler) http.Handler {
	cookies := []string{prusaAccountCookie}
	if sessionCookie != "" {
		cookies = append(cookies, sessionCookie)
	}

	allowed := make(map[string]struct{}, len(origins))
	for _, origin := range origins {
		allowed[origin] = struct{}{}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if forgedWrite(r, cookies, allowed) {
				writeProblem(w, r, fault.New(authz.CodeAccessDenied(),
					"a page on another site may not write with this browser's sign-in"))

				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// forgedWrite reports whether a request has every mark crossSiteMiddleware
// refuses.
func forgedWrite(r *http.Request, cookies []string, allowed map[string]struct{}) bool {
	if !strings.EqualFold(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")), "cross-site") {
		return false
	}

	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}

	if _, ok := allowed[r.Header.Get("Origin")]; ok {
		return false
	}

	if carriesBearer(r) {
		return false
	}

	for _, name := range cookies {
		if _, err := r.Cookie(name); err == nil {
			return true
		}
	}

	return false
}

// carriesBearer reports whether the Authorization header names a Bearer token.
// Any other scheme is ignored where identity is resolved, so the cookie would
// still be what the request authenticates with.
func carriesBearer(r *http.Request) bool {
	header := r.Header.Get("Authorization")

	return len(header) > len(bearerPrefix) &&
		strings.EqualFold(header[:len(bearerPrefix)], bearerPrefix) &&
		strings.TrimSpace(header[len(bearerPrefix):]) != ""
}
