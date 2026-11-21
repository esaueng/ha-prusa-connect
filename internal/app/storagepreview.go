package app

import (
	"context"
	"io"
	"strconv"
	"sync"
	"time"
	"unsafe"

	"github.com/prusa3d/prusa-connect/internal/authz"
	"github.com/prusa3d/prusa-connect/internal/printfile"
	"github.com/prusa3d/prusa-connect/internal/transport/http/httpapi"
	"github.com/prusa3d/prusa-connect/sdk/fault"
	"github.com/prusa3d/prusa-connect/sdk/storageapi"
)

// previewCacheEntries is how many files' previews are held at once. How much
// they may hold together, pictures and keys alike, is core.preview_cache_bytes.
// A preview read out of a storage's file is not written to disk. It is cheap
// to read again, and a cache on disk would be a second copy of somebody's
// model that nobody asked this server to keep. A printer's picture is the
// exception, see thumbnailDisk.
const previewCacheEntries = 256

// previewCache holds what was read out of a file, keyed by the file's identity
// and not by its name alone.
//
// The key carries the length and the modification time, so a file replaced
// under the same name is a different key and the old preview is never served
// for the new file. Nothing here has to be invalidated on a write.
type previewCache struct {
	mu      sync.Mutex
	entries map[previewKey]printfile.Preview
	order   []previewKey
	bytes   int
	// maxBytes is how much picture is held at once, across every entry. The
	// oldest entries are dropped until the total is under it.
	maxBytes int
	// reading is the read in flight for each key. A second caller asking for
	// the same file waits for that read and does not start its own, so a page
	// that shows twelve rows of one folder parses each file once even when
	// every row asks at the same moment.
	reading map[previewKey]*previewRead
}

// previewRead is one read in flight. done is closed when the answer is in.
type previewRead struct {
	done   chan struct{}
	answer printfile.Preview
}

// previewKey is one version of one file. A file on a storage resource names its
// storage and a file on a printer names its device, so the two never share a
// key.
type previewKey struct {
	storage  string
	device   string
	path     string
	size     int64
	modified int64
}

func newPreviewCache(maxBytes int) *previewCache {
	return &previewCache{
		entries:  make(map[previewKey]printfile.Preview),
		reading:  make(map[previewKey]*previewRead),
		maxBytes: maxBytes,
	}
}

// get returns what was read for this version of this file. A nil cache holds
// nothing, so a plane built without one reads every file again.
func (c *previewCache) get(key previewKey) (printfile.Preview, bool) {
	if c == nil {
		return printfile.Preview{}, false
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	found, ok := c.entries[key]

	return found, ok
}

// put records one preview and drops the oldest entries until the cache is
// inside both of its bounds.
func (c *previewCache) put(key previewKey, preview printfile.Preview) {
	if c == nil {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.store(key, preview)
}

// store is put with the lock already held.
//
// The first answer for a key is kept, with one exception: a picture replaces
// a verdict of no picture. A printer asked about a file it is still taking
// answers without the picture, under the version the whole file will have,
// and the picture it sends a moment later is the truth about that version.
// Keeping the first answer there left a file without its picture until the
// server restarted, while the copy on disk held it.
func (c *previewCache) store(key previewKey, preview printfile.Preview) {
	if held, seen := c.entries[key]; seen {
		if held.Thumbnail != nil || preview.Thumbnail == nil {
			return
		}

		c.bytes += previewBytes(key, preview) - previewBytes(key, held)
		c.entries[key] = preview
	} else {
		c.entries[key] = preview
		c.order = append(c.order, key)
		c.bytes += previewBytes(key, preview)
	}

	for len(c.order) > previewCacheEntries || (c.bytes > c.maxBytes && len(c.order) > 1) {
		oldest := c.order[0]
		c.order = c.order[1:]

		c.bytes -= previewBytes(oldest, c.entries[oldest])
		delete(c.entries, oldest)
	}
}

// previewBytes is how much memory one cached preview holds: the key, the
// details and the picture. An entry with no picture still counts its key, so
// a folder of files without one is bounded too.
func previewBytes(key previewKey, preview printfile.Preview) int {
	held := int(unsafe.Sizeof(key)+unsafe.Sizeof(preview)) +
		len(key.storage) + len(key.device) + len(key.path) +
		preview.Details.Bytes()

	if preview.Thumbnail != nil {
		held += int(unsafe.Sizeof(*preview.Thumbnail)) + len(preview.Thumbnail.Bytes) + len(preview.Thumbnail.MediaType)
	}

	return held
}

// once runs read for this key unless the answer is already held or another
// caller is getting it, and returns the answer either way.
//
// With a nil cache every call reads for itself.
func (c *previewCache) once(
	ctx context.Context, key previewKey, read func() printfile.Preview,
) printfile.Preview {
	if c == nil {
		return read()
	}

	c.mu.Lock()

	if found, ok := c.entries[key]; ok {
		c.mu.Unlock()

		return found
	}

	if waiting, ok := c.reading[key]; ok {
		c.mu.Unlock()

		select {
		case <-waiting.done:
			return waiting.answer
		case <-ctx.Done():
			// The caller gave up. The read goes on for whoever else is waiting
			// on it, and this one answers with nothing.
			return printfile.Preview{}
		}
	}

	mine := &previewRead{done: make(chan struct{})}
	c.reading[key] = mine
	c.mu.Unlock()

	mine.answer = read()

	// The answer is stored and the read is removed under one hold of the
	// lock. With two holds, a caller arriving between them would find neither
	// an entry nor a read to wait for, and would start a second read.
	c.mu.Lock()
	c.store(key, mine.answer)
	delete(c.reading, key)
	c.mu.Unlock()

	close(mine.done)

	return mine.answer
}

// preview reads what one file holds, through the cache. A module that reads
// the file's type answers first, for as long as wait, and the core reads the
// file itself when it does not. The facts are the module's, and nil when the
// core answered.
//
// A file that cannot be opened, and one this server does not understand, both
// report an empty preview and no error. A preview is an extra on top of
// describing the file, and a file listing must not fail because one row is a
// text document somebody renamed.
func (p storagePlane) preview(
	ctx context.Context, storage string, info storageapi.FileInfo, wait time.Duration,
) (printfile.Preview, *httpapi.FileAnalysis) {
	if info.IsDir || info.Size <= 0 {
		return printfile.Preview{}, nil
	}

	if answered, ok := p.analysis.read(ctx, storage, info, wait); ok {
		return answered.preview, answered.facts
	}

	key := previewKey{storage: storage, path: info.Path, size: info.Size, modified: info.MTime}

	return p.previews.once(ctx, key, func() printfile.Preview {
		file, err := p.stores.Open(ctx, storage, info.Path)
		if err != nil {
			return printfile.Preview{}
		}

		defer func() { _ = file.Close() }()

		return printfile.Read(ctx, seekReaderAt{inner: file}, info.Size)
	}), nil
}

// Thumbnail returns the picture stored inside one file.
//
// It takes the same permission as reading the file, because the picture is part
// of the file. It refuses a path that leaves the storage, as every other route
// here does.
func (p storagePlane) Thumbnail(
	ctx context.Context, principal string, storage string, path string,
) (httpapi.StorageThumbnail, error) {
	info, err := p.statFor(ctx, principal, storage, path)
	if err != nil {
		return httpapi.StorageThumbnail{}, err
	}

	found, _ := p.preview(ctx, storage, info, analysisWaitOf(ctx, analysisListWait))
	if found.Thumbnail == nil {
		return httpapi.StorageThumbnail{}, fault.StorageNoThumbnail.
			New("the file carries no picture").
			With(fault.StringParam("storage_id", storage), fault.StringParam("path", path))
	}

	return httpapi.StorageThumbnail{
		Bytes:     found.Thumbnail.Bytes,
		MediaType: found.Thumbnail.MediaType,
		Modified:  time.Unix(info.MTime, 0).UTC(),
		Version:   thumbnailVersion(info),
	}, nil
}

// FileVersion answers what a conditional request is compared against, without
// opening the file.
//
// It is separate from Thumbnail because a client that already holds the picture
// gets a 304 with no body. Reading the picture to answer that would parse a
// print file for nothing.
func (p storagePlane) FileVersion(
	ctx context.Context, principal string, storage string, path string,
) (httpapi.FileVersion, error) {
	info, err := p.statFor(ctx, principal, storage, path)
	if err != nil {
		return httpapi.FileVersion{}, err
	}

	return httpapi.FileVersion{
		Modified: time.Unix(info.MTime, 0).UTC(),
		Version:  thumbnailVersion(info),
	}, nil
}

// FileExtent answers how long one file is and whether the path names a
// directory, and reads nothing inside the file.
//
// It is separate from Stat, as FileVersion is. Stat opens a print file and
// parses the head of it to fill StorageEntry.Print. A caller that walks one
// file in pieces asks once per piece and never reads that field.
func (p storagePlane) FileExtent(
	ctx context.Context, principal string, storage string, path string,
) (httpapi.FileExtent, error) {
	info, err := p.statFor(ctx, principal, storage, path)
	if err != nil {
		return httpapi.FileExtent{}, err
	}

	return httpapi.FileExtent{Size: info.Size, Folder: info.IsDir}, nil
}

// thumbnailVersion is the opaque validator a conditional request compares
// against. The storage's digest is used when it kept one, because it names the
// file's contents. For a storage that keeps none, the validator is the length
// and the modification time, as in previewKey.
func thumbnailVersion(info storageapi.FileInfo) string {
	if info.SHA256 != "" {
		return info.SHA256
	}

	return strconv.FormatInt(info.Size, 10) + "-" + strconv.FormatInt(info.MTime, 10)
}

// statFor is the authorization, the path check and the stat every read of one
// file's contents begins with.
func (p storagePlane) statFor(
	ctx context.Context, principal string, storage string, path string,
) (storageapi.FileInfo, error) {
	if err := p.permit(ctx, principal, authz.ActionReadStorage); err != nil {
		return storageapi.FileInfo{}, err
	}

	if err := refuseClimb(storage, path); err != nil {
		return storageapi.FileInfo{}, err
	}

	return p.stores.Stat(ctx, storage, path)
}

// detailsOf renders what a slicer said, for the stat route. It answers nil for
// a file that said nothing, so the field is left out and not sent as a row of
// zeroes.
func detailsOf(found printfile.Preview) *httpapi.PrintDetails {
	if found.Empty() {
		return nil
	}

	out := &httpapi.PrintDetails{
		EstimatedSeconds: found.Details.EstimatedSeconds,
		FilamentMM:       found.Details.FilamentMM,
		FilamentG:        found.Details.FilamentG,
		LayerHeightMM:    found.Details.LayerHeightMM,
		NozzleMM:         found.Details.NozzleMM,
		Material:         found.Details.Material,
		PrinterModel:     found.Details.PrinterModel,
		PrintProfile:     found.Details.PrintProfile,
		PrinterProfile:   found.Details.PrinterProfile,
		FilamentProfile:  found.Details.FilamentProfile,
		HighFlow:         found.Details.HighFlow,
	}

	fillNeeds(out, found.Details)

	if found.Thumbnail != nil {
		out.ThumbnailWidth = found.Thumbnail.Width
		out.ThumbnailHeight = found.Thumbnail.Height
	}

	return out
}

// seekReaderAt reads a storage's file handle at an offset.
//
// A provider hands out a handle that seeks and reads, and the preview reader
// takes one that reads at an offset. One value is used by one goroutine for the
// length of one read, so the seek and the read below cannot interleave with
// another caller's.
type seekReaderAt struct {
	inner io.ReadSeeker
}

func (s seekReaderAt) ReadAt(p []byte, off int64) (int, error) {
	if _, err := s.inner.Seek(off, io.SeekStart); err != nil {
		return 0, err //nolint:wrapcheck // the provider's own failure is what the caller is owed.
	}

	read, err := io.ReadFull(s.inner, p)

	return read, err //nolint:wrapcheck // the provider's own failure is what the caller is owed.
}
