package httpapi

import (
	"net/http"
	"os"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// The printer tab these cases treat as one that asks for a cross-origin
// isolated page, and the address the app's router gives it.
const (
	isolatedNamespace = "com.github.aux.acme.console"
	isolatedTabID     = "console"
	isolatedTabPage   = "/printers/one/" + isolatedNamespace + "." + isolatedTabID
)

// askedTabs records every namespace and tab id the app asked about, and
// answers true for the one tab above alone.
type askedTabs struct {
	mu    sync.Mutex
	asked [][2]string
}

func (a *askedTabs) isolatedTab(namespace, tab string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.asked = append(a.asked, [2]string{namespace, tab})

	return namespace == isolatedNamespace && tab == isolatedTabID
}

func (a *askedTabs) seen() [][2]string {
	a.mu.Lock()
	defer a.mu.Unlock()

	return append([][2]string(nil), a.asked...)
}

// anyTabIsolated answers true for every tab, so a case can show an address is
// not isolated because of its shape and not because of the lookup.
func anyTabIsolated(string, string) bool { return true }

// tabApp is the fixture app behind the whole server, with lookup as the module
// registry.
func tabApp(t *testing.T, lookup func(namespace, tab string) bool) http.Handler {
	t.Helper()

	return serverWithMounts(t, Mount{
		Pattern: rootCatchAll,
		Handler: AppHandler(os.DirFS("testdata/appbundle"), AppOptions{IsolatedTab: lookup}),
		Reason:  appMountReason,
	}).Handler()
}

func requireIsolated(t *testing.T, rec interface{ Header() http.Header }, label string) {
	t.Helper()

	require.Equalf(t, "same-origin", rec.Header().Get("Cross-Origin-Opener-Policy"), "%s", label)
	require.Equalf(t, "require-corp", rec.Header().Get("Cross-Origin-Embedder-Policy"), "%s", label)
}

func requireNotIsolated(t *testing.T, rec interface{ Header() http.Header }, label string) {
	t.Helper()

	require.Emptyf(t, rec.Header().Values("Cross-Origin-Opener-Policy"), "%s", label)
	require.Emptyf(t, rec.Header().Values("Cross-Origin-Embedder-Policy"), "%s", label)
}

// TestTheTabsPageIsSentIsolated is the rule itself: the entry document at the
// address of a tab that asks for isolation carries both policies and an entity
// tag of its own, also when a browser revalidates it. A device id may hold a
// slash, which the app escapes, and the address is still the tab's.
func TestTheTabsPageIsSentIsolated(t *testing.T) {
	t.Parallel()

	asked := &askedTabs{}
	handler := tabApp(t, asked.isolatedTab)
	tag := etagOf(appDigest(os.DirFS("testdata/appbundle")) + appIsolatedSuffix)

	for _, path := range []string{isolatedTabPage, "/printers/a%2Fb/" + isolatedNamespace + "." + isolatedTabID} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			rec := call(t, handler, method, path, "")
			label := method + " " + path

			require.Equalf(t, http.StatusOK, rec.Code, "%s did not reach the app", label)
			requireIsolated(t, rec, label)
			require.Equalf(t, tag, rec.Header().Get("ETag"), "%s", label)
		}
	}

	revalidated := get(t, handler, http.MethodGet, isolatedTabPage, http.Header{"If-None-Match": {tag}})
	require.Equal(t, http.StatusNotModified, revalidated.Code)
	requireIsolated(t, revalidated, "the 304")

	require.Contains(t, asked.seen(), [2]string{isolatedNamespace, isolatedTabID},
		"the registry was not asked with the tab's namespace and id")
}

// TestEveryOtherAnswerIsTheOneAServerWithNoSuchTabSends holds what isolation
// does not reach. Each address is answered as an app with no lookup answers
// it, byte for byte: the root, a core tab, another module's tab, a tab the
// registry does not isolate, a file, and addresses only shaped like a tab's.
// The lookup answers true here, so each is left out by its shape alone, except
// the two tabs the registry is asked about, which use the recording lookup.
func TestEveryOtherAnswerIsTheOneAServerWithNoSuchTabSends(t *testing.T) {
	t.Parallel()

	plain := tabApp(t, nil)
	byShape := tabApp(t, anyTabIsolated)
	asked := &askedTabs{}
	byRegistry := tabApp(t, asked.isolatedTab)

	shaped := []string{
		"/", "/index.html", "/printers", "/printers/one", "/printers/one/status",
		"/printers/one/console",                                // no namespace
		"/printers/one/acme.console",                           // a namespace of one label
		"/printers/one/" + isolatedNamespace + ".",             // no tab id
		"/printers/one/" + isolatedNamespace + "." + "x/",      // a trailing slash
		"/printers/" + isolatedNamespace + "." + isolatedTabID, // no device id
		"/printers/one/" + isolatedNamespace + ".a%2Fb",        // a slash in the tab id
		"/printers/one/two/" + isolatedNamespace + "." + isolatedTabID,
		"/files/one/" + isolatedNamespace + "." + isolatedTabID,
		"/assets/index-abc123.js", "/favicon.svg",
	}

	compare := func(handler http.Handler, path string) {
		want := call(t, plain, http.MethodGet, path, "")
		got := call(t, handler, http.MethodGet, path, "")

		requireNotIsolated(t, got, path)
		require.Equalf(t, want.Code, got.Code, "%s", path)
		require.Equalf(t, want.Header(), got.Header(), "%s sends other headers", path)
		require.Equalf(t, want.Body.String(), got.Body.String(), "%s", path)
	}

	for _, path := range shaped {
		compare(byShape, path)
	}

	for _, path := range []string{
		"/printers/one/" + isolatedNamespace + ".log",
		"/printers/one/com.github.aux.acme.other." + isolatedTabID,
	} {
		compare(byRegistry, path)
	}

	require.ElementsMatch(t, [][2]string{
		{isolatedNamespace, "log"}, {"com.github.aux.acme.other", isolatedTabID},
	}, asked.seen(), "the registry was asked about something other than the two tabs")
}

// TestTheTabAddressIsReadAsTheAppRoutesIt is isolatedTabPath alone, on
// addresses the app's handler may never reach it with: AppHandler answers 404
// to a path that fs.ValidPath refuses, such as one with an empty device id.
// The registry is asked only about the namespace and tab id of a printer
// tab's address, and its answer decides.
func TestTheTabAddressIsReadAsTheAppRoutesIt(t *testing.T) {
	t.Parallel()

	tab := [2]string{isolatedNamespace, isolatedTabID}
	cases := []struct {
		path     string
		asked    [][2]string
		isolated bool
	}{
		{"/printers/one/" + isolatedNamespace + "." + isolatedTabID, [][2]string{tab}, true},
		{"/printers/a%2Fb/" + isolatedNamespace + "." + isolatedTabID, [][2]string{tab}, true},
		{"/printers/one/" + isolatedNamespace + ".log", [][2]string{{isolatedNamespace, "log"}}, false},
		{"/printers//" + isolatedNamespace + "." + isolatedTabID, nil, false},
		{"/printers/one/" + isolatedNamespace + ".a%2Fb", nil, false},
		{"/printers/one/" + isolatedNamespace + ".", nil, false},
		{"/printers/one/acme.console", nil, false},
		{"/printers/one/%zz", nil, false},
	}

	for _, tc := range cases {
		asked := &askedTabs{}
		isolated := isolatedTabPath(tc.path, asked.isolatedTab)
		require.Equal(t, tc.asked, asked.seen(), tc.path)
		require.Equal(t, tc.isolated, isolated, tc.path)
	}

	require.False(t, isolatedTabPath("/printers/one/"+isolatedNamespace+"."+isolatedTabID, nil))
}

// TestATabsPageIsTaggedApartFromThePlainPage is the entity tag rule. A 304
// replaces only the stored headers it carries, so a page stored while the tab
// asked for isolation must not be revalidated once it stopped. The reverse is
// pinned as well, so a cache that does not update its stored headers on a 304
// is covered.
// Each state answers the other's tag with the whole file and its own headers.
// The release bundle is the case a browser meets: it stores the gzip copy.
func TestATabsPageIsTaggedApartFromThePlainPage(t *testing.T) {
	t.Parallel()

	release := compressedBundle(t)
	never := func(string, string) bool { return false }

	cases := []struct {
		name            string
		plain, isolated http.Handler
		encoding, sent  string
	}{
		{"plain bundle, gzip asked", tabApp(t, never), tabApp(t, anyTabIsolated), "gzip", ""},
		{"plain bundle", tabApp(t, never), tabApp(t, anyTabIsolated), "identity", ""},
		{
			"release bundle, gzip", AppHandler(release, AppOptions{IsolatedTab: never}),
			AppHandler(release, AppOptions{IsolatedTab: anyTabIsolated}), "gzip", "gzip",
		},
		{
			"release bundle, identity", AppHandler(release, AppOptions{IsolatedTab: never}),
			AppHandler(release, AppOptions{IsolatedTab: anyTabIsolated}), "identity", "",
		},
	}

	for _, tc := range cases {
		accept := http.Header{"Accept-Encoding": {tc.encoding}}
		first := get(t, tc.isolated, http.MethodGet, isolatedTabPage, accept)
		require.Equalf(t, tc.sent, first.Header().Get("Content-Encoding"), "%s", tc.name)

		storedOn := first.Header().Get("ETag")
		storedOff := get(t, tc.plain, http.MethodGet, isolatedTabPage, accept).Header().Get("ETag")

		require.NotEqualf(t, storedOn, storedOff, "%s: one tag for both states", tc.name)

		stopped := get(t, tc.plain, http.MethodGet, isolatedTabPage, http.Header{
			"Accept-Encoding": {tc.encoding}, "If-None-Match": {storedOn},
		})
		require.Equalf(t, http.StatusOK, stopped.Code, "%s: stored isolated, asked once it stopped", tc.name)
		requireNotIsolated(t, stopped, tc.name)

		started := get(t, tc.isolated, http.MethodGet, isolatedTabPage, http.Header{
			"Accept-Encoding": {tc.encoding}, "If-None-Match": {storedOff},
		})
		require.Equalf(t, http.StatusOK, started.Code, "%s: stored plain, asked once it started", tc.name)
		requireIsolated(t, started, tc.name)
	}
}

// TestTheReleaseBundlesTabPageIsIsolatedInEveryForm is the rule over the
// bundle a release build stores, where the entry document is a gzip copy too.
// The copy goes out as it is to a client that takes gzip, decompressed to one
// that does not, and as headers alone to HEAD. Each form of the tab's page
// carries both policies, and a script under assets/ carries neither.
func TestTheReleaseBundlesTabPageIsIsolatedInEveryForm(t *testing.T) {
	t.Parallel()

	handler := AppHandler(compressedBundle(t), AppOptions{IsolatedTab: anyTabIsolated})

	for _, method := range []string{http.MethodGet, http.MethodHead} {
		for _, encoding := range []string{"gzip", "identity"} {
			accept := http.Header{"Accept-Encoding": {encoding}}
			label := method + " " + encoding

			page := get(t, handler, method, isolatedTabPage, accept)
			require.Equalf(t, http.StatusOK, page.Code, "%s", label)
			requireIsolated(t, page, label)

			script := get(t, handler, method, "/assets/index-abc123.js", accept)
			require.Equalf(t, http.StatusOK, script.Code, "%s", label)
			requireNotIsolated(t, script, "the script, "+label)
		}
	}
}

// TestTheReferenceIsNotIsolated holds /docs out of it: the reference is no
// printer tab, whatever the registry answers.
func TestTheReferenceIsNotIsolated(t *testing.T) {
	t.Parallel()

	handler := tabApp(t, anyTabIsolated)

	for _, path := range []string{"/docs", "/docs/scalar.js"} {
		rec := call(t, handler, http.MethodGet, path, "")
		requireNotIsolated(t, rec, path)
	}
}
