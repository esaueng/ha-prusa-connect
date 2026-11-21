package app

import (
	"bytes"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/require"

	"github.com/prusa3d/prusa-connect/internal/app/config"
	"github.com/prusa3d/prusa-connect/internal/printfile"
	"github.com/prusa3d/prusa-connect/sdk/fault"
)

// slicerFile reads one of the files PrusaSlicer exported, which
// internal/printfile/testdata/README.md describes.
func slicerFile(t *testing.T, name string) []byte {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join("..", "printfile", "testdata", name))
	require.NoError(t, err)

	return raw
}

// A file a slicer wrote is described with what the slicer said, through the
// same plane a transport calls, with the storage on a real disk.
func TestStatCarriesWhatTheSlicerWroteIntoTheFile(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		plane := planeOver(t, t.TempDir())
		plane.previews = newPreviewCache(config.DefaultPreviewCacheBytes)

		who := subjectOf(config.Default().Principal)
		sliced := slicerFile(t, "cube10.bgcode")

		_, err := plane.Save(t.Context(), who, "bench", "cube10.bgcode", bytes.NewReader(sliced))
		require.NoError(t, err)

		entry, err := plane.Stat(t.Context(), who, "bench", "cube10.bgcode")
		require.NoError(t, err)
		require.NotNil(t, entry.Print)
		require.Equal(t, int64(136), entry.Print.EstimatedSeconds)
		require.InDelta(t, 133.16, entry.Print.FilamentMM, 0.001)
		require.InDelta(t, 0.4, entry.Print.FilamentG, 0.001)
		require.InDelta(t, 0.3, entry.Print.LayerHeightMM, 0.001)
		require.InDelta(t, 0.4, entry.Print.NozzleMM, 0.001)
		require.Equal(t, "PLA", entry.Print.Material)
		require.Equal(t, "MK4IS", entry.Print.PrinterModel)

		// The slicer rendered no picture on the command line, so this file
		// carries none and the thumbnail route says so rather than failing.
		_, err = plane.Thumbnail(t.Context(), who, "bench", "cube10.bgcode")

		var carried *fault.Fault

		require.ErrorAs(t, err, &carried)
		require.Equal(t, "core.storage/NO_THUMBNAIL", carried.Code().String())
	})
}

// A file that holds no print says nothing, rather than failing the stat a
// listing page makes for every row it shows.
func TestStatOfAFileThatIsNotAPrintSaysNothing(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		plane := planeOver(t, t.TempDir())
		plane.previews = newPreviewCache(config.DefaultPreviewCacheBytes)

		who := subjectOf(config.Default().Principal)

		_, err := plane.Save(t.Context(), who, "bench", "notes.gcode",
			bytes.NewReader([]byte("the printer is on the second floor\n")))
		require.NoError(t, err)

		entry, err := plane.Stat(t.Context(), who, "bench", "notes.gcode")
		require.NoError(t, err)
		require.Nil(t, entry.Print)
	})
}

// A path that climbs out of the storage is refused on the thumbnail route, the
// same way it is on every other route into a storage's contents.
func TestTheThumbnailRefusesAPathThatLeavesTheStorage(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		plane := planeOver(t, t.TempDir())
		who := subjectOf(config.Default().Principal)

		for _, path := range []string{"../etc/passwd", `..\etc\passwd`} {
			_, err := plane.Thumbnail(t.Context(), who, "bench", path)

			var carried *fault.Fault

			require.ErrorAs(t, err, &carried, path)
			require.Equal(t, "core.storage/INVALID_PATH", carried.Code().String(), path)
		}
	})
}

// The cache answers the same preview for a file that has not changed, and a
// file replaced under the same name is a different entry, so a stale picture is
// never served for new bytes.
func TestThePreviewCacheIsKeyedByTheFileAndNotByItsName(t *testing.T) {
	t.Parallel()

	cache := newPreviewCache(config.DefaultPreviewCacheBytes)
	first := previewKey{storage: "bench", path: "a.gcode", size: 10, modified: 1}
	second := previewKey{storage: "bench", path: "a.gcode", size: 20, modified: 2}

	cache.put(first, printfile.Preview{Details: printfile.Details{EstimatedSeconds: 1}})

	found, ok := cache.get(first)
	require.True(t, ok)
	require.Equal(t, int64(1), found.Details.EstimatedSeconds)

	_, ok = cache.get(second)
	require.False(t, ok, "a file of a different length is a different entry")
}

// A picture that replaces a verdict of no picture grows the cache, so the
// oldest entry goes when the cache is then over its bound.
func TestAPictureThatReplacesAVerdictKeepsTheCacheInItsBound(t *testing.T) {
	t.Parallel()

	cache := newPreviewCache(2048)
	oldest := previewKey{device: "printer-1", path: "usb/old.gcode", size: 1, modified: 1}
	taking := previewKey{device: "printer-1", path: "usb/flower.gcode", size: 2, modified: 2}

	cache.put(oldest, printfile.Preview{})
	cache.put(taking, printfile.Preview{})
	cache.put(taking, printfile.Preview{Thumbnail: &printfile.Thumbnail{Bytes: make([]byte, 2048)}})

	_, held := cache.get(oldest)
	require.False(t, held, "the cache went over its bound and dropped nothing")

	found, held := cache.get(taking)
	require.True(t, held)
	require.NotNil(t, found.Thumbnail)
}

// A printer asked about a file it is still taking answers with no picture,
// under the version the whole file will have. The picture it sends once the
// file has landed replaces that verdict, and a later answer without one does
// not take a picture away.
func TestAPictureReplacesAVerdictOfNoPicture(t *testing.T) {
	t.Parallel()

	cache := newPreviewCache(config.DefaultPreviewCacheBytes)
	key := previewKey{device: "printer-1", path: "usb/flower.gcode", size: 3749911, modified: 1}
	picture := &printfile.Thumbnail{Bytes: []byte("a picture"), MediaType: "image/png", Width: 380, Height: 285}

	cache.put(key, printfile.Preview{})

	found, ok := cache.get(key)
	require.True(t, ok)
	require.Nil(t, found.Thumbnail, "the first answer had no picture")

	before := cache.bytes
	cache.put(key, printfile.Preview{Thumbnail: picture})

	found, ok = cache.get(key)
	require.True(t, ok)
	require.Equal(t, picture, found.Thumbnail, "the picture did not replace the verdict of no picture")
	require.Greater(t, cache.bytes, before, "the picture's bytes are not counted")
	require.Len(t, cache.order, 1, "one key is one entry")

	cache.put(key, printfile.Preview{})

	found, _ = cache.get(key)
	require.Equal(t, picture, found.Thumbnail, "an answer without a picture took the picture away")
}

// Several callers asking for one file at the same moment cost one read of it.
// A page showing twelve rows of a folder asks for twelve files at once, and
// without this each row would open and parse the same file again.
func TestThePreviewCacheReadsOneFileOnce(t *testing.T) {
	t.Parallel()

	cache := newPreviewCache(config.DefaultPreviewCacheBytes)
	key := previewKey{storage: "bench", path: "cube.bgcode", size: 12, modified: 1}

	var (
		reads   atomic.Int64
		started = make(chan struct{})
		release = make(chan struct{})
		answers = make(chan printfile.Preview, 8)
		group   sync.WaitGroup
	)

	read := func() printfile.Preview {
		if reads.Add(1) == 1 {
			close(started)
			<-release
		}

		return printfile.Preview{Details: printfile.Details{EstimatedSeconds: 42}}
	}

	for range cap(answers) {
		group.Go(func() {
			answers <- cache.once(t.Context(), key, read)
		})
	}

	// Every caller is waiting on the one read before it is let go, so the count
	// below is not a race that happened to come out right.
	<-started
	close(release)
	group.Wait()
	close(answers)

	if got := reads.Load(); got != 1 {
		t.Errorf("the file was read %d times, and eight callers asked for it once", got)
	}

	for answer := range answers {
		if answer.Details.EstimatedSeconds != 42 {
			t.Errorf("a caller was given %+v", answer.Details)
		}
	}
}

// The cache drops the oldest entries rather than growing without a bound.
func TestThePreviewCacheStaysInsideItsBounds(t *testing.T) {
	t.Parallel()

	cache := newPreviewCache(config.DefaultPreviewCacheBytes)

	for i := range previewCacheEntries * 2 {
		cache.put(previewKey{storage: "bench", path: "f", size: int64(i)}, printfile.Preview{})
	}

	require.LessOrEqual(t, len(cache.entries), previewCacheEntries)
}

// A verdict of no picture costs its key, so a folder of files without one
// still fills the byte bound rather than holding every entry for free.
func TestAPreviewWithoutAPictureCountsAgainstTheByteBound(t *testing.T) {
	t.Parallel()

	cache := newPreviewCache(4 << 10)

	for i := range previewCacheEntries {
		cache.put(previewKey{device: "p1", path: "usb/file.bgcode", size: int64(i)}, printfile.Preview{})
	}

	require.LessOrEqual(t, cache.bytes, 4<<10)
	require.Less(t, len(cache.entries), previewCacheEntries)
}

// The pictures the cache holds stay under the byte bound it was built with.
// A folder of large pictures is what fills it, so the test puts in far more
// than fits and reads the total after every put.
func TestThePreviewCacheStaysUnderItsByteBound(t *testing.T) {
	t.Parallel()

	const bound = 1 << 20

	cache := newPreviewCache(bound)
	picture := printfile.Preview{Thumbnail: &printfile.Thumbnail{Bytes: make([]byte, 100<<10)}}

	for i := range 64 {
		cache.put(previewKey{storage: "bench", path: "f", size: int64(i)}, picture)

		require.LessOrEqual(t, cache.bytes, bound, "after %d pictures", i+1)
	}

	require.Len(t, cache.entries, bound/(100<<10), "the cache keeps as many pictures as fit")
}
