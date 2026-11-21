package httpapi

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"testing/fstest"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/prusa3d/prusa-connect/internal/transport/http/httpfault"
)

// gzipFixture compresses one file of testdata/appbundle the way
// cmd/compress-app does.
func gzipFixture(t *testing.T, name string) []byte {
	t.Helper()

	var packed bytes.Buffer

	writer, err := gzip.NewWriterLevel(&packed, gzip.BestCompression)
	require.NoError(t, err)

	_, err = writer.Write(readFixture(t, "testdata/appbundle/"+name))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	return packed.Bytes()
}

// compressedBundle is testdata/appbundle as the build stores it. The script
// and the stylesheet are kept only as gzip copies. The entry document is kept
// in both forms, as cmd/compress-app keeps it, and so is the icon.
func compressedBundle(t *testing.T) fstest.MapFS {
	t.Helper()

	files := fstest.MapFS{
		"index.html":  {Data: readFixture(t, "testdata/appbundle/index.html")},
		"favicon.svg": {Data: readFixture(t, "testdata/appbundle/favicon.svg")},
	}

	for _, name := range []string{"index.html", "assets/index-abc123.js", "assets/index-abc123.css", "favicon.svg"} {
		files[name+appCompressedSuffix] = &fstest.MapFile{Data: gzipFixture(t, name)}
	}

	return files
}

func compressedFixture(t *testing.T) http.Handler {
	t.Helper()

	return serverWithMounts(t, Mount{
		Pattern: rootCatchAll,
		Handler: AppHandler(compressedBundle(t), AppOptions{}),
		Reason:  appMountReason,
	}).Handler()
}

func get(t *testing.T, handler http.Handler, method, path string, header http.Header) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(method, path, nil)
	maps.Copy(req.Header, header)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	return rec
}

func gunzip(t *testing.T, body []byte) string {
	t.Helper()

	reader, err := gzip.NewReader(bytes.NewReader(body))
	require.NoError(t, err)

	plain, err := io.ReadAll(reader)
	require.NoError(t, err)

	return string(plain)
}

// appCompressedCases are the addresses a first visit asks for, with the file
// that answers each one. A client route answers the entry document.
var appCompressedCases = []struct { //nolint:gochecknoglobals // a read-only table shared by the tests below.
	path        string
	file        string
	contentType string
	cache       string
}{
	{"/", "index.html", "text/html; charset=utf-8", appEntryCacheControl},
	{"/index.html", "index.html", "text/html; charset=utf-8", appEntryCacheControl},
	{"/devices/printer-1", "index.html", "text/html; charset=utf-8", appEntryCacheControl},
	{"/settings/", "index.html", "text/html; charset=utf-8", appEntryCacheControl},
	{"/assets/index-abc123.js", "assets/index-abc123.js", "text/javascript; charset=utf-8", appAssetCacheControl},
	{"/assets/index-abc123.css", "assets/index-abc123.css", "text/css; charset=utf-8", appAssetCacheControl},
	{"/favicon.svg", "favicon.svg", "image/svg+xml", appFileCacheControl},
}

// TestABrowserGetsTheStoredGzipCopy is the point of storing the copy: a client
// that accepts gzip gets the stored bytes with Content-Encoding, and they
// decompress to the file the build wrote.
func TestABrowserGetsTheStoredGzipCopy(t *testing.T) {
	t.Parallel()

	handler := compressedFixture(t)
	bundle := compressedBundle(t)

	for _, tc := range appCompressedCases {
		rec := get(t, handler, http.MethodGet, tc.path, http.Header{"Accept-Encoding": {"gzip, deflate, br, zstd"}})

		stored := bundle[tc.file+appCompressedSuffix].Data

		require.Equalf(t, http.StatusOK, rec.Code, "%s did not reach the app", tc.path)
		require.Equalf(t, "gzip", rec.Header().Get("Content-Encoding"), "%s was not sent compressed", tc.path)
		require.Equalf(t, stored, rec.Body.Bytes(), "%s did not send the stored copy", tc.path)
		require.Equalf(t, string(readFixture(t, "testdata/appbundle/"+tc.file)), gunzip(t, rec.Body.Bytes()),
			"%s does not decompress to the built file", tc.path)
		require.Equalf(t, "Accept-Encoding", rec.Header().Get("Vary"), "%s carries no Vary", tc.path)
		require.Equalf(t, tc.contentType, rec.Header().Get("Content-Type"), "%s carries the wrong type", tc.path)
		require.Equalf(t, tc.cache, rec.Header().Get("Cache-Control"), "%s carries the wrong cache rule", tc.path)
		require.Equalf(t, "nosniff", rec.Header().Get("X-Content-Type-Options"), "%s may be sniffed", tc.path)
		require.Equalf(t, len(stored), mustAtoi(t, rec.Header().Get("Content-Length")),
			"%s carries the wrong length", tc.path)
	}
}

// TestAClientWithoutGzipGetsThePlainFile covers curl and any other tool that
// sends no Accept-Encoding or refuses gzip with a zero weight. A file stored
// only as a copy is decompressed for it, and that answer carries no
// Content-Length because the length is not known before the end.
func TestAClientWithoutGzipGetsThePlainFile(t *testing.T) {
	t.Parallel()

	handler := compressedFixture(t)

	for _, accept := range [][]string{nil, {"identity"}, {"gzip;q=0, br"}, {"br, gzip; q=0.000"}} {
		for _, tc := range appCompressedCases {
			header := http.Header{}
			if accept != nil {
				header["Accept-Encoding"] = accept
			}

			rec := get(t, handler, http.MethodGet, tc.path, header)

			require.Equalf(t, http.StatusOK, rec.Code, "%s did not reach the app", tc.path)
			require.Emptyf(t, rec.Header().Get("Content-Encoding"), "%s was sent compressed to %q", tc.path, accept)
			require.Equalf(t, string(readFixture(t, "testdata/appbundle/"+tc.file)), rec.Body.String(),
				"%s did not send the plain file to %q", tc.path, accept)
			require.Equalf(t, "Accept-Encoding", rec.Header().Get("Vary"), "%s carries no Vary", tc.path)
			require.Equalf(t, tc.contentType, rec.Header().Get("Content-Type"), "%s carries the wrong type", tc.path)
			require.Equalf(t, tc.cache, rec.Header().Get("Cache-Control"), "%s carries the wrong cache rule", tc.path)

			_, storedPlain := compressedBundle(t)[tc.file]
			if storedPlain {
				require.Equalf(t, rec.Body.Len(), mustAtoi(t, rec.Header().Get("Content-Length")),
					"%s carries the wrong length", tc.path)
			} else {
				require.Emptyf(t, rec.Header().Get("Content-Length"), "%s was given a length before the end", tc.path)
			}
		}
	}
}

// TestTheTwoFormsCarryDifferentEntityTags holds RFC 9110 section 8.8.3: an
// entity tag names one representation. A tag shared by both forms would let a
// client revalidate the gzip bytes it holds against the plain file, or the
// other way round.
func TestTheTwoFormsCarryDifferentEntityTags(t *testing.T) {
	t.Parallel()

	handler := compressedFixture(t)
	gzipped := http.Header{"Accept-Encoding": {"gzip"}}

	compressedTag := get(t, handler, http.MethodGet, "/assets/index-abc123.js", gzipped).Header().Get("ETag")
	plainTag := get(t, handler, http.MethodGet, "/assets/index-abc123.js", nil).Header().Get("ETag")

	require.NotEmpty(t, compressedTag)
	require.NotEmpty(t, plainTag)
	require.NotEqual(t, compressedTag, plainTag)

	rec := get(t, handler, http.MethodGet, "/assets/index-abc123.js",
		http.Header{"Accept-Encoding": {"gzip"}, "If-None-Match": {compressedTag}})
	require.Equal(t, http.StatusNotModified, rec.Code)
	require.Equal(t, "Accept-Encoding", rec.Header().Get("Vary"), "a 304 without Vary lets a cache mix the forms")
	require.Empty(t, rec.Body.Bytes())

	rec = get(t, handler, http.MethodGet, "/assets/index-abc123.js", http.Header{"If-None-Match": {compressedTag}})
	require.Equal(t, http.StatusOK, rec.Code, "the gzip tag revalidated the plain file")
	require.Equal(t, plainTag, rec.Header().Get("ETag"))

	rec = get(t, handler, http.MethodGet, "/assets/index-abc123.js", http.Header{"If-None-Match": {plainTag}})
	require.Equal(t, http.StatusNotModified, rec.Code)
}

// TestARangeRequestGetsTheWholeCompressedFile follows the standard library,
// which serves no range of a response that has a Content-Encoding. A range of
// gzip bytes is useless to a browser, and RFC 9110 lets a server ignore Range.
func TestARangeRequestGetsTheWholeCompressedFile(t *testing.T) {
	t.Parallel()

	handler := compressedFixture(t)

	rec := get(t, handler, http.MethodGet, "/assets/index-abc123.js",
		http.Header{"Accept-Encoding": {"gzip"}, "Range": {"bytes=0-9"}})

	require.Equal(t, http.StatusOK, rec.Code)
	require.Empty(t, rec.Header().Get("Content-Range"))
	require.Empty(t, rec.Header().Get("Accept-Ranges"))
	require.Equal(t, compressedBundle(t)["assets/index-abc123.js.gz"].Data, rec.Body.Bytes())
}

// TestAHeadRequestGetsTheHeadersWithoutTheBody asks through the server, which
// answers HEAD with the GET handler, and asks AppHandler alone. The recorder,
// unlike the server, keeps a body written to a HEAD request, so an empty body
// here means the handler wrote none and decompressed nothing.
func TestAHeadRequestGetsTheHeadersWithoutTheBody(t *testing.T) {
	t.Parallel()

	for name, handler := range map[string]http.Handler{
		"the server":  compressedFixture(t),
		"the handler": AppHandler(compressedBundle(t), AppOptions{}),
	} {
		for _, accept := range []string{"gzip", ""} {
			header := http.Header{"Accept-Encoding": {accept}}
			head := get(t, handler, http.MethodHead, "/assets/index-abc123.js", header)
			full := get(t, handler, http.MethodGet, "/assets/index-abc123.js", header)

			require.Equalf(t, http.StatusOK, head.Code, "%s refused HEAD", name)
			require.Emptyf(t, head.Body.Bytes(), "%s answered HEAD with a body", name)
			require.Equalf(t, full.Header(), head.Header(), "%s gave HEAD and GET different headers", name)
		}
	}
}

// TestTheStoredCopyHasNoAddressOfItsOwn keeps one address per file. A name
// ending in .gz is the stored form of another file, so under assets/ it is a
// reference the build never wrote.
func TestTheStoredCopyHasNoAddressOfItsOwn(t *testing.T) {
	t.Parallel()

	handler := compressedFixture(t)

	rec := get(t, handler, http.MethodGet, "/assets/index-abc123.js.gz", http.Header{"Accept-Encoding": {"gzip"}})

	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Equal(t, problemMediaType, rec.Header().Get("Content-Type"))
}

// TestABundleStoredCompressedIsABuiltApp is the check the full build makes
// before it mounts the app: an entry document stored only as a gzip copy is
// still an entry document, and the rooted tree serves it.
func TestABundleStoredCompressedIsABuiltApp(t *testing.T) {
	t.Parallel()

	files, carried := appBundleFS(fstest.MapFS{"dist/index.html.gz": {Data: gzipFixture(t, "index.html")}}, "dist")
	require.True(t, carried, "a bundle with a compressed entry document was taken for an empty one")

	rec := get(t, AppHandler(files, AppOptions{}), http.MethodGet, "/", http.Header{"Accept-Encoding": {"gzip"}})
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, string(readFixture(t, "testdata/appbundle/index.html")), gunzip(t, rec.Body.Bytes()))
}

func mustAtoi(t *testing.T, value string) int {
	t.Helper()

	n, err := strconv.Atoi(value)
	require.NoErrorf(t, err, "%q is not a length", value)

	return n
}

// TestAcceptEncodingIsReadWithItsWeights covers the shapes a client sends. A
// weight of zero refuses gzip, and x-gzip is another name for it.
func TestAcceptEncodingIsReadWithItsWeights(t *testing.T) {
	t.Parallel()

	cases := []struct {
		header []string
		want   bool
	}{
		{nil, false},
		{[]string{""}, false},
		{[]string{"gzip"}, true},
		{[]string{"GZIP;Q=0.5"}, true},
		{[]string{"x-gzip"}, true},
		{[]string{"br", "gzip"}, true},
		{[]string{"gzip;q=0"}, false},
		{[]string{"gzip; q=0.00"}, false},
		{[]string{"gzip;q=0.001"}, true},
		{[]string{"deflate, br"}, false},
	}

	for _, tc := range cases {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.Header["Accept-Encoding"] = tc.header

		require.Equalf(t, tc.want, acceptsGzip(req), "Accept-Encoding %q", tc.header)
	}
}

// blockedWriter is a client that reads nothing. Its first Write waits until
// the test closes release, so the request holds its place until then.
type blockedWriter struct {
	header  http.Header
	release chan struct{}
}

func (w *blockedWriter) Header() http.Header { return w.header }

func (w *blockedWriter) WriteHeader(int) {}

func (w *blockedWriter) Write(body []byte) (int, error) {
	<-w.release

	return len(body), nil
}

// TestDecompressionIsBoundedAndGzipIsNot is the memory bound on a printer. A
// client without gzip makes the server hold a decompressor while the client
// reads, so two such requests at a time are allowed. A third waits, then gets
// 503 with Retry-After. A browser that accepts gzip never waits for a slot.
func TestDecompressionIsBoundedAndGzipIsNot(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		handler := AppHandler(compressedBundle(t), AppOptions{})
		release := make(chan struct{})

		var held sync.WaitGroup

		for range appDecompressSlots {
			held.Go(func() {
				writer := &blockedWriter{header: http.Header{}, release: release}
				handler.ServeHTTP(writer, httptest.NewRequest(http.MethodGet, "/assets/index-abc123.js", nil))
			})
		}

		synctest.Wait()

		start := time.Now()
		rec := get(t, handler, http.MethodGet, "/assets/index-abc123.css", nil)

		require.Equal(t, http.StatusServiceUnavailable, rec.Code)
		require.Equal(t, appDecompressWait, time.Since(start), "the refusal did not wait for a slot")
		require.Equal(t, problemMediaType, rec.Header().Get("Content-Type"))
		require.Equal(t, appDecompressRetryAfter, rec.Header().Get("Retry-After"))
		require.Empty(t, rec.Header().Get("ETag"), "a refusal carries the tag of the file it did not send")
		require.Contains(t, rec.Body.String(), httpfault.CodeAppFileBusy().String())

		rec = get(t, handler, http.MethodGet, "/assets/index-abc123.css", http.Header{"Accept-Encoding": {"gzip"}})
		require.Equal(t, http.StatusOK, rec.Code, "a browser waited behind clients without gzip")

		rec = get(t, handler, http.MethodGet, "/", nil)
		require.Equal(t, http.StatusOK, rec.Code, "the entry document waited, and it is stored plain")

		close(release)
		held.Wait()

		rec = get(t, handler, http.MethodGet, "/assets/index-abc123.css", nil)
		require.Equal(t, http.StatusOK, rec.Code, "a slot was not given back")
	})
}

// TestAFailedDecompressionAbortsTheAnswer holds the rule that a cut file never
// looks whole. The status is sent before decompression fails, so the only
// honest signal left is a connection that ends without a complete body.
func TestAFailedDecompressionAbortsTheAnswer(t *testing.T) {
	t.Parallel()

	whole := gzipFixture(t, "assets/index-abc123.js")
	server := httptest.NewServer(AppHandler(fstest.MapFS{
		"index.html":                {Data: []byte("<!doctype html>")},
		"assets/index-abc123.js.gz": {Data: whole[:len(whole)-12]},
	}, AppOptions{}))
	t.Cleanup(server.Close)

	client := &http.Client{Transport: &http.Transport{DisableCompression: true}}
	t.Cleanup(client.CloseIdleConnections)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/assets/index-abc123.js", nil)
	require.NoError(t, err)

	resp, err := client.Do(req)
	if err != nil {
		return // the connection ended before the status line, which is an honest failure too
	}

	defer func() { _ = resp.Body.Close() }()

	_, err = io.ReadAll(resp.Body)
	require.Error(t, err, "a truncated copy was answered as a complete file")
}

// TestACopyThatIsNotGzipIsAnErrorWithoutCacheHeaders covers a stored copy the
// decompressor cannot open. The answer is a problem document, so it may not
// carry the tag or the cache rule of the file it replaces.
func TestACopyThatIsNotGzipIsAnErrorWithoutCacheHeaders(t *testing.T) {
	t.Parallel()

	handler := AppHandler(fstest.MapFS{
		"index.html":                {Data: []byte("<!doctype html>")},
		"assets/index-abc123.js.gz": {Data: []byte("not gzip")},
	}, AppOptions{})

	rec := get(t, handler, http.MethodGet, "/assets/index-abc123.js", nil)

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.Equal(t, problemMediaType, rec.Header().Get("Content-Type"))

	for _, name := range []string{"ETag", "Cache-Control", "Vary"} {
		require.Emptyf(t, rec.Header().Get(name), "the problem document carries %s", name)
	}
}

// failingWriter is a client whose connection breaks on the first write.
type failingWriter struct{ header http.Header }

func (w *failingWriter) Header() http.Header { return w.header }

func (w *failingWriter) WriteHeader(int) {}

func (w *failingWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

// serveAborted runs one request that must end in the abort panic, the way the
// server's own recovery sees it.
func serveAborted(t *testing.T, handler http.Handler, writer http.ResponseWriter, path string) {
	t.Helper()

	defer func() {
		recovered := recover()
		err, ok := recovered.(error)
		require.Truef(t, ok && errors.Is(err, http.ErrAbortHandler), "%s ended with %v, not an abort", path, recovered)
	}()

	handler.ServeHTTP(writer, httptest.NewRequest(http.MethodGet, path, nil))
}

// TestASlotComesBackAfterAnAbortedAnswer covers the two ways a decompression
// ends in the abort panic: a copy that is cut short, and a client whose
// connection breaks. Each one must give its slot back, or a few broken
// requests would leave every later client without gzip waiting for nothing.
func TestASlotComesBackAfterAnAbortedAnswer(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		whole := gzipFixture(t, "assets/index-abc123.js")
		handler := AppHandler(fstest.MapFS{
			"index.html":                 {Data: readFixture(t, "testdata/appbundle/index.html")},
			"assets/cut-abc123.js.gz":    {Data: whole[:len(whole)-12]},
			"assets/index-abc123.js.gz":  {Data: whole},
			"assets/index-abc123.css.gz": {Data: gzipFixture(t, "assets/index-abc123.css")},
		}, AppOptions{})

		for range appDecompressSlots + 1 {
			serveAborted(t, handler, httptest.NewRecorder(), "/assets/cut-abc123.js")
			serveAborted(t, handler, &failingWriter{header: http.Header{}}, "/assets/index-abc123.js")
		}

		start := time.Now()
		rec := get(t, handler, http.MethodGet, "/assets/index-abc123.css", nil)

		require.Equal(t, http.StatusOK, rec.Code, "an aborted answer kept its slot")
		require.Zero(t, time.Since(start), "the request waited for a slot that should have been free")
	})
}

// TestAClientThatLeavesWhileWaitingIsAnsweredAtOnce holds the request context
// in the wait. A client that has gone must not keep a goroutine waiting for a
// slot it will never use.
func TestAClientThatLeavesWhileWaitingIsAnsweredAtOnce(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		handler := AppHandler(compressedBundle(t), AppOptions{})
		release := make(chan struct{})

		var held sync.WaitGroup

		for range appDecompressSlots {
			held.Go(func() {
				writer := &blockedWriter{header: http.Header{}, release: release}
				handler.ServeHTTP(writer, httptest.NewRequest(http.MethodGet, "/assets/index-abc123.js", nil))
			})
		}

		synctest.Wait()

		ctx, cancel := context.WithCancel(t.Context())
		finished := make(chan time.Duration, 1)
		start := time.Now()

		go func() {
			req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/assets/index-abc123.css", nil)
			handler.ServeHTTP(httptest.NewRecorder(), req)
			finished <- time.Since(start)
		}()

		synctest.Wait()
		cancel()
		synctest.Wait()

		select {
		case waited := <-finished:
			require.Zero(t, waited, "the request went on waiting after its client left")
		default:
			require.Fail(t, "the request went on waiting after its client left")
		}

		close(release)
		held.Wait()
	})
}
