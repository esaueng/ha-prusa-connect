package httpapi

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// appFixture is a bundle shaped like the one `vite build` writes: an entry
// document, a hashed script and stylesheet under assets/, and one file at the
// root that is not the entry. It stands in for a real build, so this suite runs
// without pnpm and answers the same in every build of the core.
func appFixture(t *testing.T) http.Handler {
	t.Helper()

	files := os.DirFS("testdata/appbundle")

	return serverWithMounts(t, Mount{
		Pattern: rootCatchAll,
		Handler: AppHandler(files, AppOptions{}),
		Reason:  appMountReason,
	}).Handler()
}

// TestTheAppAnswersEveryAddressItsRouterOwns is ADR-20260911-4p6 rule 33: a path that
// names no file is a route the app's own router owns, so it answers the entry
// document and a reload on a client route works.
func TestTheAppAnswersEveryAddressItsRouterOwns(t *testing.T) {
	t.Parallel()

	handler := appFixture(t)
	entry := string(readFixture(t, "testdata/appbundle/index.html"))

	for _, path := range []string{"/", "/settings", "/devices/printer-1", "/settings/"} {
		rec := call(t, handler, http.MethodGet, path, "")

		require.Equalf(t, http.StatusOK, rec.Code, "%s did not reach the app", path)
		require.Equalf(t, entry, rec.Body.String(), "%s answered with something other than the entry document", path)
		require.Equal(t, "text/html; charset=utf-8", rec.Header().Get("Content-Type"))
		require.Equal(t, "nosniff", rec.Header().Get("X-Content-Type-Options"))
		require.Equal(t, appEntryCacheControl, rec.Header().Get("Cache-Control"))
		require.NotEmpty(t, rec.Header().Get("ETag"))
	}
}

// TestAMissingAssetIsRefusedRatherThanAnsweredWithAPage is the other half of
// rule 33. A name under assets/ carries the build's own content hash, so one
// the build never wrote is a stale reference. A document there is a 200 the
// browser cannot use: a module script handed HTML fails on the first `<`.
func TestAMissingAssetIsRefusedRatherThanAnsweredWithAPage(t *testing.T) {
	t.Parallel()

	handler := appFixture(t)

	for _, path := range []string{"/assets/missing.js", "/assets/index-000000.css", "/assets/"} {
		rec := call(t, handler, http.MethodGet, path, "")

		require.Equalf(t, http.StatusNotFound, rec.Code, "%s was answered with a page", path)
		require.Equalf(t, problemMediaType, rec.Header().Get("Content-Type"),
			"%s answered with content rather than a refusal", path)
	}
}

// TestEachFileOfTheAppCarriesItsTypeAndItsCacheRule holds the discipline
// ModuleUIHandler already applies: the type comes from the extension and never
// from the bytes, nothing is sniffed, and a name carrying a content hash is
// cached for a year while everything else is not.
func TestEachFileOfTheAppCarriesItsTypeAndItsCacheRule(t *testing.T) {
	t.Parallel()

	handler := appFixture(t)

	cases := map[string]struct {
		contentType string
		cache       string
	}{
		"/assets/index-abc123.js":  {"text/javascript; charset=utf-8", appAssetCacheControl},
		"/assets/index-abc123.css": {"text/css; charset=utf-8", appAssetCacheControl},
		"/favicon.svg":             {"image/svg+xml", appFileCacheControl},
		"/index.html":              {"text/html; charset=utf-8", appEntryCacheControl},
	}

	for path, want := range cases {
		rec := call(t, handler, http.MethodGet, path, "")

		require.Equalf(t, http.StatusOK, rec.Code, "%s did not reach the app", path)
		require.Equalf(t, want.contentType, rec.Header().Get("Content-Type"), "%s carries the wrong type", path)
		require.Equalf(t, want.cache, rec.Header().Get("Cache-Control"), "%s carries the wrong cache rule", path)
		require.Equalf(t, "nosniff", rec.Header().Get("X-Content-Type-Options"), "%s may be sniffed", path)
	}
}

// TestTheAppNeverAnswersAnOperation is what the mount exists beside. The
// pattern matches everything, so without the router's own refusal in front of
// it a call to an operation that is not there would be answered with a page
// saying the route is fine.
func TestTheAppNeverAnswersAnOperation(t *testing.T) {
	t.Parallel()

	handler := appFixture(t)

	for _, path := range []string{"/api", "/api/v1", "/api/v1/nothing-here", specJSONPath} {
		rec := call(t, handler, http.MethodGet, path, "")

		require.NotEqualf(t, "text/html; charset=utf-8", rec.Header().Get("Content-Type"),
			"%s was answered by the app", path)
	}

	rec := call(t, handler, http.MethodGet, healthPath, "")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), `"status":"ok"`)
}

// TestEveryAnswerOfTheAppRefusesToBeFramed holds the security headers on a
// page, an asset and a refusal. A page another site can frame is a page it can
// trick a person into clicking.
func TestEveryAnswerOfTheAppRefusesToBeFramed(t *testing.T) {
	t.Parallel()

	handler := appFixture(t)

	for _, path := range []string{"/", "/settings", "/assets/index-abc123.js", "/assets/missing.js"} {
		rec := call(t, handler, http.MethodGet, path, "")

		require.Equalf(t, appContentSecurityPolicy, rec.Header().Get("Content-Security-Policy"), "%s", path)
		require.Containsf(t, rec.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'", "%s", path)
		require.Equalf(t, "DENY", rec.Header().Get("X-Frame-Options"), "%s", path)
		require.Equalf(t, appReferrerPolicy, rec.Header().Get("Referrer-Policy"), "%s", path)
		require.Emptyf(t, rec.Header().Get("Strict-Transport-Security"),
			"%s asked a browser on plain HTTP to use HTTPS only", path)
	}
}

// TestOnlyAnAnswerOverTLSAsksForHTTPSOnly is the other direction of the HSTS
// rule. Over TLS the browser is told to stay on HTTPS.
func TestOnlyAnAnswerOverTLSAsksForHTTPSOnly(t *testing.T) {
	t.Parallel()

	handler := appFixture(t)

	req := httptest.NewRequest(http.MethodGet, "https://connect.example/settings", nil)
	req.TLS = &tls.ConnectionState{}

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, appStrictTransport, rec.Header().Get("Strict-Transport-Security"))
}

// TestTheAppIsAskedOnlyForANavigation holds the method rule the mount is
// wrapped in. A device dialling an origin whose connector is not mounted is a
// caller that sent something other than a GET, and a page is a success it
// cannot read.
func TestTheAppIsAskedOnlyForANavigation(t *testing.T) {
	t.Parallel()

	handler := appFixture(t)

	rec := call(t, handler, http.MethodPost, "/settings", "")

	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Equal(t, problemMediaType, rec.Header().Get("Content-Type"))
}

// TestAPathThatLeavesTheBundleIsRefused is the traversal case. fs.ValidPath
// refuses a dot element and a leading slash, and a backslash is not a thing a
// URL path legitimately carries.
func TestAPathThatLeavesTheBundleIsRefused(t *testing.T) {
	t.Parallel()

	handler := appFixture(t)

	for _, path := range []string{"/assets/..%2f..%2fgo.mod", `/assets\index-abc123.js`} {
		rec := call(t, handler, http.MethodGet, path, "")

		require.NotEqualf(t, http.StatusOK, rec.Code, "%s was served", path)
	}
}

// TestABuildWithoutTheAppMountsNothing is what the minimal build keeps. The
// embed directory carries a placeholder and no entry document until the app is
// built, so `go build` is green in a checkout nobody has built it in and the
// origin root answers what it always did.
func TestABuildWithoutTheAppMountsNothing(t *testing.T) {
	t.Parallel()

	_, carried := appBundleFS(os.DirFS("testdata"), "appbundle-empty")
	require.False(t, carried, "a directory with no entry document was taken for a built app")
}

func readFixture(t *testing.T, path string) []byte {
	t.Helper()

	body, err := os.ReadFile(path)
	require.NoError(t, err)

	return body
}
