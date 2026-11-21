package app

// The printer file surface: what a printer reports it has, and the
// dialect-neutral vocabulary for changing it. devices.go holds the plane
// itself, its listing and its session bookkeeping.

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/prusa3d/prusa-connect/internal/authz"
	"github.com/prusa3d/prusa-connect/internal/kernel/lane"
	"github.com/prusa3d/prusa-connect/internal/printfile"
	"github.com/prusa3d/prusa-connect/internal/transport/http/httpapi"
	"github.com/prusa3d/prusa-connect/internal/transport/http/httpfault"
	"github.com/prusa3d/prusa-connect/sdk/connectorapi"
	"github.com/prusa3d/prusa-connect/sdk/event"
	"github.com/prusa3d/prusa-connect/sdk/fault"
)

// Storages returns the storage devices a printer reported.
//
// Without refresh it takes observe, because it reads what the printer last said
// and puts nothing on the wire. refresh asks the printer first, which is a frame
// at a machine that runs one command at a time, so it takes operate as Files
// does.
func (d *devices) Storages(
	ctx context.Context, principal string, id string, refresh bool,
) (httpapi.DeviceStorages, error) {
	action := authz.ActionObserve
	if refresh {
		action = authz.ActionOperate
	}

	entry, err := d.reachLive(ctx, principal, id, action)
	if err != nil {
		return httpapi.DeviceStorages{}, err
	}

	// The question waits behind whatever the printer is running, up to 15 s
	// for a listing of a full USB drive. Past listingGrace the storages held
	// are answered, as for a folder (ADR-20261112-wfc). The driver's own
	// flight goes on, and an answer it still gets is published as
	// storages.changed when it differs.
	if refresh {
		asked, done := context.WithTimeout(ctx, listingGrace)
		askErr := entry.session.AskStorages(asked)

		done()

		if askErr != nil && !errors.Is(askErr, context.DeadlineExceeded) {
			return httpapi.DeviceStorages{}, askErr
		}
	}

	return heldStorages(ctx, entry)
}

// heldStorages is what one printer's session holds about its storages, in the
// shape the route and the storages.changed frame both carry.
func heldStorages(ctx context.Context, entry *device) (httpapi.DeviceStorages, error) {
	storages, err := entry.session.Storages(ctx)
	if err != nil {
		return httpapi.DeviceStorages{}, err
	}

	out := httpapi.DeviceStorages{
		Items:    make([]httpapi.DeviceStorage, 0, len(storages)),
		Reported: entry.session.StoragesReported(),
	}
	for _, device := range storages {
		out.Items = append(out.Items, storageOf(device))
	}

	return out, nil
}

// Files returns one directory of a printer's own file tree, at once.
//
// It takes operate and not observe. Answering it can put a SEND_FILE_INFO on
// the wire at a machine that runs one command at a time. A caller who may only
// observe would otherwise be able to make this server queue work at somebody's
// printer.
//
// An MK4S takes 12 to 15 s to list a USB root of about 300 files, and a
// bigger stick takes longer than the request budget, so this never waits on
// it beyond listingGrace. What is held for path, rows or the failure of the last read, is answered without refresh and starts
// no read. Otherwise refresh, or nothing held, starts a read or joins the one in
// flight and answers that it is reading. A person waits on either, so the read
// runs ahead of background reads. The read publishes files.changed when it
// lands.
func (d *devices) Files(
	ctx context.Context, principal string, id string, path string, refresh bool,
) (httpapi.DeviceListing, error) {
	return d.listFiles(ctx, principal, id, path, refresh, true)
}

// FilesHeld is Files for a module that asked for what is held: it never waits
// for the printer, not even the grace a person's cold read is given, and a
// path nothing is held of is answered as being read.
func (d *devices) FilesHeld(
	ctx context.Context, principal string, id string, path string,
) (httpapi.DeviceListing, error) {
	return d.listFiles(ctx, principal, id, path, false, false)
}

func (d *devices) listFiles(
	ctx context.Context, principal string, id string, path string, refresh, grace bool,
) (httpapi.DeviceListing, error) {
	entry, err := d.reachLive(ctx, principal, id, authz.ActionOperate)
	if err != nil {
		return httpapi.DeviceListing{}, err
	}

	// A path that leaves its storage is the caller's mistake and not a read
	// that failed, so it is refused here and never held as a failure.
	if refusal := connectorapi.RefusePathEscape(path); refusal != nil {
		return httpapi.DeviceListing{}, refusal
	}

	path = strings.Trim(path, "/")
	rows, held := d.listings.get(id, path)
	failed := d.listings.failure(id, path)

	if !refresh && (held || failed != nil) {
		read, _ := d.listings.inFlight(id, path)

		return heldListingOf(path, rows, held, failed, read), nil
	}

	// A read nobody waits the grace for is one no person waits for, so the
	// printer answers a person's commands ahead of it.
	read, mine := d.listings.join(id, path, !grace)
	if mine {
		d.startListing(ctx, entry, id, path, read)
	}

	if grace && !held && failed == nil {
		grace := time.NewTimer(listingGrace)
		defer grace.Stop()

		select {
		case <-read.done:
		case <-grace.C:
		case <-ctx.Done():
		}
	}

	select {
	case <-read.done:
		if read.err != nil {
			return heldListingOf(path, rows, held, read.err, nil), nil
		}

		return read.listing, nil
	default:
		return heldListingOf(path, rows, held, nil, read), nil
	}
}

// listingGrace is how long Files waits for a read of a folder with nothing
// held, its own or one it joined, so a folder the printer lists in about a
// second answers with its rows rather than with a reading state that flashes
// past.
const listingGrace = time.Second

// landedAskBudget bounds the question about a file a send has just landed. The
// driver asked the same question to judge the landing, and the answer only
// makes the folder's held row exact: a printer that is slow to answer leaves
// the file as sent, and the send does not wait for it (ADR-20261112-wfc rule 6).
const landedAskBudget = 2 * time.Second

// listingFault is the fault a failed read of folder is answered with. A read
// that ended in no fault of its own, a dropped connection or the read's bound,
// is LISTING_UNFINISHED.
func listingFault(folder string, err error) *httpapi.ListingFault {
	carried, ok := errors.AsType[*fault.Fault](err)
	if !ok {
		carried = httpfault.ListingUnfinished(folder).Wrap(err)
	}

	return &httpapi.ListingFault{Payload: carried.Payload(), TraceID: carried.TraceID()}
}

// FilesNow returns one directory as the printer answers it now, waiting for
// the printer. It is Files for the module wire, which carries no reading state
// a module could wait on.
func (d *devices) FilesNow(
	ctx context.Context, principal string, id string, path string,
) (httpapi.DeviceListing, error) {
	entry, err := d.reachLive(ctx, principal, id, authz.ActionOperate)
	if err != nil {
		return httpapi.DeviceListing{}, err
	}

	path = strings.Trim(path, "/")
	read, mine := d.listings.join(id, path, false)
	if mine {
		d.startListing(ctx, entry, id, path, read)
	}

	select {
	case <-read.done:
		return read.listing, read.err
	case <-ctx.Done():
		return httpapi.DeviceListing{}, fmt.Errorf("wait for the printer's listing: %w", ctx.Err())
	}
}

// listingReadBound bounds a listing read that every caller has stopped waiting
// for. The connector bounds its own command, and this is the bound for one
// that does not. It is longer than the 60s xBuddy command deadline.
const listingReadBound = 2 * time.Minute

// startListing asks the printer for one directory and files the answer, on a
// goroutine that outlives the request.
//
// Buddy firmware can take more than the 30s request budget to list a large
// folder on a USB stick, and it runs one command at a time. A listing tied to
// the request would be thrown away when the request ended, and each retry from
// the page would start the printer's work again, so such a folder would never
// open. A retry joins the read in flight, and a later visit finds the listing
// held.
func (d *devices) startListing(
	ctx context.Context, entry *device, id string, path string, read *listingRead,
) {
	run := func(readCtx context.Context) {
		if read.background != nil {
			readCtx = lane.WithBackground(readCtx, read.background)
		}

		d.filesChanged(entry, path, true, false)

		listing, err := d.readListing(readCtx, entry, path, read.background == nil)
		if err == nil {
			read.listing = listing
			d.listings.putSince(id, path, listing, read)
		} else {
			read.err = err
			d.listings.failSince(id, path, err, read)
		}

		d.listings.finish(id, path, read)
		d.filesChanged(entry, path, false, read.err != nil)
	}

	// A plane that is closing starts nothing Close would have to wait for, and
	// the read is the caller's own.
	if d.life.Err() != nil {
		run(ctx)

		return
	}

	d.walks.Go(func() {
		readCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), listingReadBound)
		defer cancel()

		stop := context.AfterFunc(d.life, cancel)
		defer stop()

		run(readCtx)
	})
}

// filesChanged says a read of path began, when reading is true, or landed.
func (d *devices) filesChanged(entry *device, path string, reading, failed bool) {
	d.publish(context.Background(), entry.id, event.TypeDeviceFilesChanged,
		event.ClassState, "", filesChangeOf(entry, path, reading, failed))
}

// readListing is one SEND_FILE_INFO, in the shape the route carries. prefetch
// walks the thumbnails of the rows, for a read a person waits on: a warm-up or a
// rescan would read every picture of a folder nobody opened.
func (d *devices) readListing(
	ctx context.Context, entry *device, path string, prefetch bool,
) (httpapi.DeviceListing, error) {
	listing, err := entry.session.List(ctx, path)
	if err != nil {
		return httpapi.DeviceListing{}, err
	}

	entries := make([]httpapi.DeviceFile, 0, len(listing.Entries))
	for _, file := range listing.Entries {
		entries = append(entries, fileOf(file))
	}

	if prefetch && d.prefetchThumbnails {
		d.prefetch(entry, listing.Entries)
	}

	return httpapi.DeviceListing{Directory: fileOf(listing.Directory), Items: entries}, nil
}

// changesStorage reports whether a command verb writes to the printer's
// storage, which changes the held listing of the folder it names.
func changesStorage(verb string) bool {
	switch connectorapi.Capability(verb) {
	case connectorapi.VerbCreateFolder, connectorapi.VerbDeleteFile, connectorapi.VerbDeleteFolder:
		return true
	default:
		return false
	}
}

// verbSend names a file this server sent in fileChanged. It is no capability
// and is never offered on any wire.
const verbSend connectorapi.Capability = "send"

// fileChanged applies one write this server made: patch on success, forget the
// folder on failure or an unknown result.
//
// Only the folder the write named changes: an open Files tab would otherwise
// read the whole USB root again after each send, 12 to 15 s on an MK4S. landed
// is the file a send put there.
func (d *devices) fileChanged(
	entry *device, verb connectorapi.Capability, at string, landed *connectorapi.File, err error,
) {
	at = strings.Trim(at, "/")
	folder, name := "", at

	if cut := strings.LastIndex(at, "/"); cut >= 0 {
		folder, name = at[:cut], at[cut+1:]
	}

	patch := func(change func(httpapi.DeviceListing) httpapi.DeviceListing) {
		d.listings.patch(entry.id, folder, change)
	}

	switch {
	case err != nil:
		d.listings.forget(entry.id, func(key string) bool { return key == folder })

		if verb == connectorapi.VerbCreateFolder || verb == connectorapi.VerbDeleteFolder {
			d.listings.invalidate(entry.id, at)
		}
	case verb == connectorapi.VerbCreateFolder:
		d.listings.invalidate(entry.id, at)
		patch(func(listing httpapi.DeviceListing) httpapi.DeviceListing {
			return withFile(listing, httpapi.DeviceFile{Path: at, Name: name, Folder: true})
		})
	case verb == verbSend:
		file := fileOf(*landed)
		file.Path = at

		if file.Name == "" {
			file.Name = name
		}

		patch(func(listing httpapi.DeviceListing) httpapi.DeviceListing { return withFile(listing, file) })
	default:
		patch(func(listing httpapi.DeviceListing) httpapi.DeviceListing { return withoutFile(listing, at) })

		if verb == connectorapi.VerbDeleteFolder {
			d.listings.invalidate(entry.id, at)
			d.filesChanged(entry, at, false, false)
		}
	}

	d.filesChanged(entry, folder, false, false)
}

// printerChangedFile applies one change the printer reported to the listing
// held for its folder. A folder held under no listing is read when it is
// opened, so a change to it is not kept.
//
// The printer names the entry by its short path. Each segment is matched to
// the held rows of the folder above it, and the row is kept under the long
// name the rest of this server uses.
func (d *devices) printerChangedFile(entry *device, change connectorapi.FileChange) {
	at := strings.Trim(change.File.Path, "/")
	parent, segment := "", at

	if cut := strings.LastIndex(at, "/"); cut >= 0 {
		parent, segment = at[:cut], at[cut+1:]
	}

	if change.Rescan {
		d.rescan(entry, at)

		return
	}

	folder, held := d.listings.resolve(entry.id, parent)
	if !held {
		return
	}

	listing, held := d.listings.get(entry.id, folder)
	if !held {
		return
	}

	name := change.File.DisplayName
	if name == "" {
		name = segment
	}

	row := fileOf(change.File)
	row.Path = folder + "/" + name

	// A FILE_INFO names the file by its path alone, so the held row keeps the
	// names the folder listing gave it.
	if at := slices.IndexFunc(listing.Items, func(item httpapi.DeviceFile) bool {
		return sameEntry(item, row)
	}); at >= 0 && !change.Removed {
		kept := listing.Items[at]
		row.Path = kept.Path

		if row.Name == "" {
			row.Name = kept.Name
		}

		if change.File.DisplayName == "" {
			row.DisplayName = kept.DisplayName
		}

		if sameRow(kept, row) {
			return
		}
	}

	if change.Removed {
		gone := httpapi.DeviceFile{Path: folder + "/" + segment, DisplayName: change.File.DisplayName}

		at := slices.IndexFunc(listing.Items, func(item httpapi.DeviceFile) bool { return sameEntry(item, gone) })
		if at < 0 {
			return
		}

		d.listings.patch(entry.id, folder, func(listing httpapi.DeviceListing) httpapi.DeviceListing {
			return withoutFile(listing, gone.Path)
		})
		d.listings.invalidate(entry.id, listing.Items[at].Path)
		d.filesChanged(entry, folder, false, false)

		return
	}

	d.listings.patch(entry.id, folder, func(listing httpapi.DeviceListing) httpapi.DeviceListing {
		return withFile(listing, row)
	})
	d.filesChanged(entry, folder, false, false)
}

// rescan reads again, in the background, a held folder the printer said
// changed in more ways than one event names. Its rows answer until the read
// lands.
func (d *devices) rescan(entry *device, at string) {
	folder, resolved := d.listings.resolve(entry.id, at)
	if !resolved {
		return
	}

	if _, held := d.listings.get(entry.id, folder); !held {
		return
	}

	if read, mine := d.listings.join(entry.id, folder, true); mine {
		d.startListing(d.life, entry, entry.id, folder, read)
	}
}

// sameRow reports whether two rows say the same about one entry, within the
// second of drift readThumbnail allows between two answers about one file.
func sameRow(a, b httpapi.DeviceFile) bool {
	sameTime := a.Modified == nil && b.Modified == nil ||
		a.Modified != nil && b.Modified != nil && a.Modified.Sub(*b.Modified).Abs() <= time.Second

	return sameEntry(a, b) && a.Folder == b.Folder && a.Size == b.Size && sameTime
}

// Thumbnail returns the picture a printer reads out of one print file on its
// own storage.
//
// It takes operate for the reason Files does: answering it puts a
// SEND_FILE_INFO on the wire. size and modified are what the caller's listing
// reported for the file, and a picture already held for that version answers
// without asking the printer again. A zero size takes the version from the
// held listing of the file's folder, and asks when no listing names the file.
// What the printer answers is held under the version the printer reported and
// never under the one the caller named, so a caller cannot file a picture
// under a version the file never had.
func (d *devices) Thumbnail(
	ctx context.Context, principal string, id string, path string, size int64, modified int64,
) (httpapi.StorageThumbnail, error) {
	entry, err := d.reachLive(ctx, principal, id, authz.ActionOperate)
	if err != nil {
		return httpapi.StorageThumbnail{}, err
	}

	listed := d.listedVersion(id, path)
	if size <= 0 && listed.size > 0 {
		path, size, modified = listed.path, listed.size, listed.modified
	}

	if size > 0 {
		held, ok := d.heldThumbnail(previewKey{device: id, path: path, size: size, modified: modified})
		if ok && held.Thumbnail == nil {
			return httpapi.StorageThumbnail{}, noDeviceThumbnail(path)
		}

		if ok {
			return deviceThumbnail(*held.Thumbnail, size, modified), nil
		}
	}

	file, picture, err := d.readThumbnail(ctx, entry, path, listed)
	if err != nil {
		return httpapi.StorageThumbnail{}, err
	}

	if picture == nil {
		return httpapi.StorageThumbnail{}, noDeviceThumbnail(path)
	}

	return deviceThumbnail(*picture, file.Size, file.Modified), nil
}

// readThumbnail asks the printer about one file and keeps the picture it
// carries under the version the printer reported. A file that carries none is
// kept as an empty preview, so that version is not asked about again. A folder
// is kept as nothing, because it has no version.
//
// A printer answers one command at a time, and a picture takes it seconds. A
// page of them asked for at once would keep a person's own command behind all
// of them, past its budget. So the questions go one at a time and a command
// waits behind one picture at most. The walk and the route both come here.
//
// listed is the version the held listing gave the file, or nothing. A printer
// can name a file's time a second off what its listing said, because FAT keeps
// time in two-second steps. So the picture is kept under the listed version
// too when the answer names the same length within that step. Without it, the
// walk and Files would ask about such a file on every visit.
func (d *devices) readThumbnail(
	ctx context.Context, entry *device, path string, listed previewKey,
) (connectorapi.File, *printfile.Thumbnail, error) {
	select {
	case entry.pictures <- struct{}{}:
		defer func() { <-entry.pictures }()
	case <-ctx.Done():
		return connectorapi.File{}, nil, fmt.Errorf("waiting to ask about %s: %w", path, ctx.Err())
	}

	listing, err := entry.session.List(ctx, path)
	if err != nil {
		return connectorapi.File{}, nil, err
	}

	file := listing.Directory
	if file.Folder {
		return file, nil, nil
	}

	picture := printfile.Picture(file.Preview)
	key := previewKey{device: entry.id, path: path, size: file.Size, modified: file.Modified}

	// A file this server is still sending to the printer has no picture the
	// printer can read yet, and it already reports the length and the time the
	// whole file will have. Remembering "no picture" for that version would
	// outlive the transfer, so nothing is remembered and the next request asks
	// again.
	if picture == nil && d.arriving(entry.id, path) {
		return file, nil, nil
	}

	d.previews.put(key, printfile.Preview{Thumbnail: picture})
	d.thumbnails.put(key, printfile.Preview{Thumbnail: picture})

	drift := max(listed.modified-file.Modified, file.Modified-listed.modified)
	if listed.size > 0 && listed != key && listed.size == file.Size && drift <= 1 {
		d.previews.put(listed, printfile.Preview{Thumbnail: picture})
		d.thumbnails.put(listed, printfile.Preview{Thumbnail: picture})
	}

	return file, picture, nil
}

// arriving reports whether a transfer to this path on this printer is running.
func (d *devices) arriving(id string, path string) bool {
	origin, err := deviceOrigin(id)
	if err != nil {
		return false
	}

	asked := strings.TrimPrefix(path, "/")

	for _, record := range d.transfers.running(origin.String()) {
		if strings.TrimPrefix(record.Destination, "/") == asked {
			return true
		}
	}

	return false
}

// listedVersion names a file as the held listing of its folder does: by the
// path it was listed under and the version it was listed at. A print names its
// file by the printer's short name, such as /usb/MODULA~1.GCO, and a picture is
// held under the listed path, so without this every visit to Jobs would ask
// the printer again. A file no held listing names comes back as nothing.
func (d *devices) listedVersion(id string, path string) previewKey {
	asked := strings.TrimPrefix(path, "/")

	cut := strings.LastIndexByte(asked, '/')
	if cut < 0 {
		return previewKey{}
	}

	listing, ok := d.listings.get(id, asked[:cut])
	if !ok {
		return previewKey{}
	}

	for _, file := range listing.Items {
		if file.Folder || (file.Path != asked && !strings.EqualFold(file.Name, asked[cut+1:])) {
			continue
		}

		key := previewKey{device: id, path: file.Path, size: file.Size}
		if file.Modified != nil {
			key.modified = file.Modified.Unix()
		}

		return key
	}

	return previewKey{}
}

// heldThumbnail returns what is kept for one version of a printer file, from
// memory or else from disk. A picture read from disk is held in memory too.
func (d *devices) heldThumbnail(key previewKey) (printfile.Preview, bool) {
	if held, ok := d.previews.get(key); ok {
		return held, true
	}

	held, ok := d.thumbnails.get(key)
	if ok {
		d.previews.put(key, held)
	}

	return held, ok
}

func noDeviceThumbnail(path string) error {
	return fault.StorageNoThumbnail.
		New("the file carries no picture").
		With(fault.StringParam("path", path))
}

// deviceThumbnail is one printer file's picture with the validators a
// conditional request compares against: the file's length and modification
// time, which is all a printer says about a file's identity.
func deviceThumbnail(picture printfile.Thumbnail, size int64, modified int64) httpapi.StorageThumbnail {
	return httpapi.StorageThumbnail{
		Bytes:     picture.Bytes,
		MediaType: picture.MediaType,
		Modified:  time.Unix(modified, 0).UTC(),
		Version:   strconv.FormatInt(size, 10) + "-" + strconv.FormatInt(modified, 10),
	}
}

// Send puts a file this deployment is holding onto a printer's own storage.
//
// It takes two permissions, because it touches two things: operate on the
// printer, for a stronger reason than the listing does since it writes to the
// machine, and read_storage on the storage kind, because the source is a
// storage's contents and reading those is its own grant. An operator on one
// printer holding nothing on the storage kind could otherwise copy any file out
// of any declared storage, and the refusals would tell them which storages and
// which paths exist.
//
// The file is read from a declared storage resource rather than from the
// request, so nothing here allocates in proportion to a file's length and a
// caller cannot make this server hold a file by sending one.
//
// It returns when the printer says the file arrived, or when the caller stops
// waiting. The bytes leave while the printer asks for them, and a caller that
// stops waiting does not stop them: the relay runs on until the file arrives,
// the stop route ends it or relayDeadline passes. See outliveCaller.
//
// Every byte goes through this process. What reaches the driver is a
// connectorapi.Content, which is a length and a way to open a range, and never
// a storage, a provider or a name a driver could resolve for itself. So a
// driver cannot move a file around the core. A file from a peer server would
// arrive as another Content, with nothing below here changed.
//
// The transfer is recorded while it runs. A record is opened before the first
// byte leaves, the driver reports where it has got to, and the record is closed
// with how it ended, so a caller watching over the event stream sees the same
// transfer the listing shows.
func (d *devices) Send(
	ctx context.Context, principal string, id string, upload httpapi.DeviceUpload,
) (httpapi.DeviceTransfer, error) {
	if err := httpapi.ValidateUpload(upload); err != nil {
		return httpapi.DeviceTransfer{}, err
	}

	return outliveCaller(ctx, d, id, upload.Path, func(relaying context.Context) (httpapi.DeviceTransfer, error) {
		return d.send(relaying, principal, id, upload, nil, false)
	})
}

// send is Send, telling opened the transfer's id as soon as the record is
// written and before the first byte leaves.
//
// The hook is how a print started over the module wire answers while the file
// is still on its way: that caller is given the transfer id here and this call
// goes on relaying the file. opened is nil for every other caller.
//
// reuse lets a copy this server already sent to the path stand in for the
// file, which a print wants and a plain send does not: see clearDestination.
//
// A source inside the temporary area is a copy a person did not keep. It is
// held while the relay runs and handed to incomingCopies once it ends, which
// deletes it or keeps it for another try.
func (d *devices) send(
	ctx context.Context, principal string, id string, upload httpapi.DeviceUpload,
	opened func(httpapi.DeviceTransfer), reuse bool,
) (httpapi.DeviceTransfer, error) {
	folder := copyFolder(upload.Source)
	if folder == "" || d.incoming == nil {
		return d.relay(ctx, principal, id, upload, opened, reuse)
	}

	// Deferred as well, so a driver that panics does not leave the copy held
	// for as long as the process runs. The panic itself goes on up.
	release := d.incoming.hold(upload.Storage, folder)
	released := false

	defer func() {
		if !released {
			release()
		}
	}()

	transfer, err := d.relay(ctx, principal, id, upload, opened, reuse)
	released = true
	release()

	d.incoming.relayed(ctx, upload.Storage, folder, err)

	return transfer, err
}

// relay is send for any source, with nothing held.
func (d *devices) relay(
	ctx context.Context, principal string, id string, upload httpapi.DeviceUpload,
	opened func(httpapi.DeviceTransfer), reuse bool,
) (httpapi.DeviceTransfer, error) {
	entry, err := d.reachLive(ctx, principal, id, authz.ActionOperate)
	if err != nil {
		return httpapi.DeviceTransfer{}, err
	}

	// Before the instance and the name are resolved, so a refusal is answered
	// without saying whether either exists.
	if err = permitStorageContents(ctx, d.graph, principal, authz.ActionReadStorage); err != nil {
		return httpapi.DeviceTransfer{}, err
	}

	content, err := d.files.content(ctx, upload.Storage, upload.Source)
	if err != nil {
		return httpapi.DeviceTransfer{}, err
	}

	// Only asked here. A replace deletes the printer's file below, once every
	// check that can still refuse the send has passed.
	replacing, at, err := d.destination(ctx, entry, id, upload, content, reuse)
	if err != nil {
		return httpapi.DeviceTransfer{}, err
	}

	// keep_both may have moved the file to a free name. Every step below, the
	// record and the event stream included, names the path it lands at.
	upload.Path = at

	// The transfer runs under a context of this plane's own, derived from the
	// caller's. Cancelling it is how another request stops a transfer: the
	// goroutine that is blocked in Send is the only one that can end it, and
	// this is the handle the stop route pulls.
	sending, cancel := context.WithCancel(ctx)
	defer cancel()

	record, err := d.beginTransferRecord(ctx, principal, id, upload, content.Size(), cancel)
	if err != nil {
		return httpapi.DeviceTransfer{}, err
	}

	if opened != nil {
		opened(transferOf(record.snapshot()))
	}

	began := time.Now()

	// The register holds one transfer per machine, so a record left behind by
	// a driver that panics refuses every later transfer to that printer for as
	// long as the process runs. Closing it here is what keeps the printer free.
	// The panic then goes on to the goroutine the relay runs on, whose own
	// recovery writes it down: outliveCaller's for a caller over HTTP, and
	// StartPrint's for a print nobody is waiting for.
	defer func() {
		recovered := recover()
		if recovered == nil {
			return
		}

		d.settleAfterPanic(ctx, record, upload.Path, time.Since(began))

		panic(recovered)
	}()

	entry.stopPrefetch()

	// The last step before the bytes leave, with the printer's slot already
	// held, so nothing this server decides can refuse the send after the
	// printer's file is gone.
	if replacing != "" {
		d.copies.forget(id, upload.Path)

		if deleteErr := entry.session.DeleteFile(sending, replacing); deleteErr != nil {
			err = fmt.Errorf("replace %s on the printer: %w", replacing, deleteErr)
		} else if replacing != upload.Path {
			d.fileChanged(entry, connectorapi.VerbDeleteFile, replacing, nil, nil)
		}
	}

	if err == nil {
		err = entry.session.Send(sending, connectorapi.Upload{
			Path:     upload.Path,
			Content:  content,
			Progress: d.reportProgress(ctx, record),
		})
	}

	d.settleTransferRecord(ctx, record, err, time.Since(began))

	if err != nil {
		d.fileChanged(entry, verbSend, upload.Path, nil, err)
	}

	if refusedAsHeld(err) {
		return httpapi.DeviceTransfer{}, nameTaken(id, upload.Path, false)
	}

	if err != nil {
		d.copies.forget(id, upload.Path)

		return httpapi.DeviceTransfer{}, sendFailure(upload, record, err)
	}

	// The copy as the printer reports it now is what a later print of the same
	// file is held to, and what its folder's held listing gains. A printer
	// that does not say has still reported the transfer finished, so the
	// folder gains the file as sent rather than being read whole again.
	asked, done := context.WithTimeout(ctx, landedAskBudget)
	landed, found := heldAt(asked, entry, upload.Path)

	done()

	if found {
		d.copies.remember(id, upload, identityOfContent(content), landed)
	} else {
		landed = connectorapi.File{Size: content.Size()}
	}

	d.fileChanged(entry, verbSend, upload.Path, &landed, nil)

	return transferOf(record.snapshot()), nil
}

// CreateFolder makes one folder on a printer's storage.
//
// The three functions below take operate for the reason Files does, and each
// one also changes the machine's storage. A caller who may only observe cannot
// reach them.
func (d *devices) CreateFolder(ctx context.Context, principal string, id string, path string) error {
	entry, err := d.reachLive(ctx, principal, id, authz.ActionOperate)
	if err != nil {
		return err
	}

	err = entry.session.CreateFolder(ctx, path)
	d.fileChanged(entry, connectorapi.VerbCreateFolder, path, nil, err)

	return err
}

// DeleteFile removes one file from a printer's storage.
func (d *devices) DeleteFile(ctx context.Context, principal string, id string, path string) error {
	entry, err := d.reachLive(ctx, principal, id, authz.ActionOperate)
	if err != nil {
		return err
	}

	err = entry.session.DeleteFile(ctx, path)
	d.fileChanged(entry, connectorapi.VerbDeleteFile, path, nil, err)

	return err
}

// DeleteFolder removes one folder from a printer's storage.
func (d *devices) DeleteFolder(ctx context.Context, principal string, id string, path string) error {
	entry, err := d.reachLive(ctx, principal, id, authz.ActionOperate)
	if err != nil {
		return err
	}

	err = entry.session.DeleteFolder(ctx, path)
	d.fileChanged(entry, connectorapi.VerbDeleteFolder, path, nil, err)

	return err
}

// settleAfterPanic closes the record of a transfer whose driver stopped.
//
// Closing it can stop too, on a hub or a store that is already failing. That
// second failure is written to the log and goes no further, so the caller can
// raise the driver's own panic: the driver's is the one that says what broke,
// and a second panic raised from here would take its place.
func (d *devices) settleAfterPanic(
	ctx context.Context, record *liveTransfer, path string, elapsed time.Duration,
) {
	defer func() {
		if second := recover(); second != nil {
			d.log.ErrorContext(ctx, "closing the record of a stopped transfer stopped too",
				"path", path, "panic", fmt.Sprint(second))
		}
	}()

	d.settleTransferRecord(ctx, record, driverStopped(path), elapsed)
}

// driverStopped is how a transfer is recorded when the driver sending it
// stopped in the middle. The panic itself goes on to the relay goroutine's
// recovery, which is what logs it; this is what the transfer's own row says
// happened, and what a caller still waiting is told.
func driverStopped(path string) error {
	return connectorapi.TransferFailed.
		New("the driver stopped while sending the file").
		With(
			fault.StringParam("path", path),
			fault.StringParam("event", "driver_stopped"),
		)
}

// sendFailure is what a caller that asked for a transfer is told when it did
// not finish.
//
// A transfer somebody stopped arrives here as a cancelled context, which is not
// a fault of this server and must not be reported as one: without this, ending
// a transfer would answer the request that started it with an unexplained 500. It
// is the same code the driver reports for a transfer the printer abandoned,
// because to the caller they are the same thing: the file is not there and
// sending it again is the answer.
func sendFailure(upload httpapi.DeviceUpload, record *liveTransfer, failure error) error {
	reason, stopped := record.stopped()
	if !stopped {
		return failure
	}

	return connectorapi.TransferFailed.
		New("the transfer was stopped before the file arrived").
		With(
			fault.StringParam("path", upload.Path),
			fault.StringParam("event", "stopped"),
			fault.StringParam("reason", reason),
		).
		Wrap(failure)
}
