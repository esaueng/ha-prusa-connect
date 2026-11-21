package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/prusa3d/prusa-connect/internal/authz"
)

// crossSiteCase is one request put to crossSiteMiddleware.
type crossSiteCase struct {
	name    string
	method  string
	site    string
	cookie  string
	headers map[string]string
	refused bool
}

func TestCrossSiteWritesWithACookieAreRefused(t *testing.T) {
	t.Parallel()

	cases := []crossSiteCase{
		{
			name: "session cookie, POST", method: http.MethodPost, site: "cross-site",
			cookie: testCookieName, refused: true,
		},
		{
			name: "prusa account cookie, POST", method: http.MethodPost, site: "cross-site",
			cookie: prusaAccountCookie, refused: true,
		},
		{
			name: "session cookie, DELETE", method: http.MethodDelete, site: "cross-site",
			cookie: testCookieName, refused: true,
		},
		{
			name: "session cookie, PUT", method: http.MethodPut, site: "Cross-Site",
			cookie: testCookieName, refused: true,
		},
		{
			name: "session cookie, PATCH", method: http.MethodPatch, site: "cross-site",
			cookie: testCookieName, refused: true,
		},
		{
			name: "session cookie under a basic header", method: http.MethodPost, site: "cross-site",
			cookie: testCookieName, headers: map[string]string{"Authorization": "Basic dXNlcjpwYXNz"},
			refused: true,
		},
		{
			name: "session cookie under an empty bearer", method: http.MethodPost, site: "cross-site",
			cookie: testCookieName, headers: map[string]string{"Authorization": "Bearer  "},
			refused: true,
		},
		{
			name: "session cookie from an origin off the list", method: http.MethodPost,
			site: "cross-site", cookie: testCookieName,
			headers: map[string]string{"Origin": otherOrigin}, refused: true,
		},
		{
			name: "bearer header beside the cookie", method: http.MethodPost, site: "cross-site",
			cookie: testCookieName, headers: map[string]string{"Authorization": "Bearer pcl_token"},
		},
		{
			name: "bearer header alone", method: http.MethodPost, site: "cross-site",
			headers: map[string]string{"Authorization": "bearer pcl_token"},
		},
		{
			name: "session cookie from an allowed origin", method: http.MethodPost, site: "cross-site",
			cookie: testCookieName, headers: map[string]string{"Origin": farmOrigin},
		},
		{name: "no cookie", method: http.MethodPost, site: "cross-site"},
		{name: "unrelated cookie", method: http.MethodPost, site: "cross-site", cookie: "theme"},
		{name: "same origin", method: http.MethodPost, site: "same-origin", cookie: testCookieName},
		{name: "same site", method: http.MethodPost, site: "same-site", cookie: testCookieName},
		{name: "typed into the address bar", method: http.MethodPost, site: "none", cookie: testCookieName},
		{name: "no header", method: http.MethodPost, cookie: testCookieName},
		{name: "GET", method: http.MethodGet, site: "cross-site", cookie: testCookieName},
		{name: "HEAD", method: http.MethodHead, site: "cross-site", cookie: testCookieName},
		{name: "OPTIONS", method: http.MethodOptions, site: "cross-site", cookie: testCookieName},
	}

	handler := crossSiteMiddleware(testCookieName, []string{farmOrigin})(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, crossSiteRequest(tc))

			if !tc.refused {
				require.Equal(t, http.StatusNoContent, rec.Code)

				return
			}

			require.Equal(t, http.StatusForbidden, rec.Code)
			require.Equal(t, problemMediaType, rec.Header().Get("Content-Type"))

			var problem struct {
				Code string `json:"code"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &problem))
			require.Equal(t, authz.CodeAccessDenied().String(), problem.Code)
		})
	}
}

// TestTheServerRefusesACrossSiteWrite puts the forged write to the production
// server, so the middleware is proven to be on the stack and to answer before
// the operation runs.
func TestTheServerRefusesACrossSiteWrite(t *testing.T) {
	t.Parallel()

	handler := serverWithMounts(t).Handler()

	forged := crossSiteRequest(crossSiteCase{method: http.MethodPost, site: "cross-site", cookie: testCookieName})
	forged.URL.Path = logoutPath

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, forged)
	require.Equal(t, http.StatusForbidden, rec.Code)

	sameOrigin := crossSiteRequest(crossSiteCase{method: http.MethodPost, site: "same-origin", cookie: testCookieName})
	sameOrigin.URL.Path = logoutPath

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, sameOrigin)
	require.NotEqual(t, http.StatusForbidden, rec.Code)
}

func crossSiteRequest(tc crossSiteCase) *http.Request {
	req := httptest.NewRequest(tc.method, "/api/v1/anything", nil)
	if tc.site != "" {
		req.Header.Set("Sec-Fetch-Site", tc.site)
	}

	if tc.cookie != "" {
		req.AddCookie(&http.Cookie{Name: tc.cookie, Value: "pcl_value"})
	}

	for name, value := range tc.headers {
		req.Header.Set(name, value)
	}

	return req
}
