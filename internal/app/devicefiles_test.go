package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/prusa3d/prusa-connect/internal/app/config"
	"github.com/prusa3d/prusa-connect/internal/authz"
	"github.com/prusa3d/prusa-connect/internal/kernel/lane"
	"github.com/prusa3d/prusa-connect/internal/printer/capability"
	"github.com/prusa3d/prusa-connect/internal/printer/profile"
	"github.com/prusa3d/prusa-connect/internal/transport/http/httpapi"
	"github.com/prusa3d/prusa-connect/internal/transport/http/httpfault"
	"github.com/prusa3d/prusa-connect/internal/transport/stream"
	"github.com/prusa3d/prusa-connect/sdk/connectorapi"
	"github.com/prusa3d/prusa-connect/sdk/deviceapi"
	"github.com/prusa3d/prusa-connect/sdk/event"
)

// TestASimulatedPrinterReportsItsStoragesAndItsFiles is the file half of the
// demo on the real composition root: the binary's own connector mount, the real
// capability resolver, the real authorization graph, and a printer that dialled
// in over the frozen wire, read through the published API.
func TestASimulatedPrinterReportsItsStoragesAndItsFiles(t *testing.T) {
	t.Parallel()

	running := start(t, t.TempDir(), "core:\n  log:\n    level: error\n")

	dialIn(t, running, "SIM-FILES-1")

	listed := printers(t, running)
	require.Len(t, listed, 1)

	id := listed[0].ID

	storages := storagesOf(t, running, id)
	t.Logf("observed storages: %s", mustJSON(t, storages))

	require.Len(t, storages, 1, "the printer reported no storages, so SEND_INFO never reached it")
	require.Equal(t, "/usb", storages[0].Mountpoint)
	require.Equal(t, "usb", storages[0].Name)
	require.Equal(t, "USB", storages[0].Type)
	require.NotNil(t, storages[0].FreeSpace, "the printer reported free space and the API dropped it")
	require.Positive(t, *storages[0].FreeSpace)

	root := filesOf(t, running, id, "")
	t.Logf("observed root listing: %s", mustJSON(t, root))

	require.True(t, root.Directory.Folder)
	require.Len(t, root.Items, 1)
	require.Equal(t, "usb", root.Items[0].Path)

	stick := filesOf(t, running, id, "usb")
	t.Logf("observed usb listing: %s", mustJSON(t, stick))

	require.Equal(t, "usb", stick.Directory.Path)
	require.True(t, stick.Directory.Folder)
	require.NotEmpty(t, stick.Items, "the simulated storage is empty, so this listing proves nothing")

	names := map[string]httpapi.DeviceFile{}
	for _, entry := range stick.Items {
		names[entry.Path] = entry
	}

	require.Contains(t, names, "usb/examples")
	require.True(t, names["usb/examples"].Folder)
	require.Contains(t, names, "usb/tangle.bgcode")
	require.False(t, names["usb/tangle.bgcode"].Folder)
	require.Positive(t, names["usb/tangle.bgcode"].Size)

	inside := filesOf(t, running, id, "usb/examples")
	t.Logf("observed usb/examples listing: %s", mustJSON(t, inside))

	require.Len(t, inside.Items, 1)
	require.Equal(t, "usb/examples/benchy.bgcode", inside.Items[0].Path)
}

// The picture a printer reads out of a print file reaches the published
// surface as a PNG, and a folder, which carries none, is a 404.
func TestASimulatedPrinterServesThePictureInsideItsFile(t *testing.T) {
	t.Parallel()

	running := start(t, t.TempDir(), "core:\n  log:\n    level: error\n")

	dialIn(t, running, "SIM-THUMB-1")

	listed := printers(t, running)
	require.Len(t, listed, 1)

	base := "/api/v1/devices/" + listed[0].ID + "/files/thumbnail?path="

	picture := running.get(t, base+url.QueryEscape("usb/tangle.bgcode"))
	require.Equal(t, http.StatusOK, picture.status, picture.body)
	require.True(t, strings.HasPrefix(picture.body, "\x89PNG\r\n\x1a\n"), "the answer is not a PNG")

	folder := running.get(t, base+url.QueryEscape("usb/examples"))
	require.Equal(t, http.StatusNotFound, folder.status, folder.body)
	require.Contains(t, folder.body, "core.storage/NO_THUMBNAIL")
}

// A printer reads the slicer's print time out of a file it is asked about,
// beside the picture. The answer about that one file carries it in seconds,
// and a folder's listing names no times, because the printer reads no file
// for it.
func TestASimulatedPrinterSaysHowLongItsFilePrints(t *testing.T) {
	t.Parallel()

	running := start(t, t.TempDir(), "core:\n  log:\n    level: error\n")

	dialIn(t, running, "SIM-TIME-1")

	listed := printers(t, running)
	require.Len(t, listed, 1)

	one := filesOf(t, running, listed[0].ID, "usb/tangle.bgcode")
	require.False(t, one.Directory.Folder)
	require.Equal(t, int64(4530), one.Directory.EstimatedSeconds, "the simulator names 1h 15m 30s")

	stick := filesOf(t, running, listed[0].ID, "usb")
	require.Zero(t, stick.Directory.EstimatedSeconds)

	for _, entry := range stick.Items {
		require.Zero(t, entry.EstimatedSeconds, "%s is an entry of a folder", entry.Path)
	}
}

// A path that leaves the storage it names is refused with a 400 carrying the
// code, on the published surface. It is the hole the shipping validator's fix
// closed, checked where a client can reach it.
func TestAPathThatLeavesItsStorageIsRefusedOnThePublishedSurface(t *testing.T) {
	t.Parallel()

	running := start(t, t.TempDir(), "core:\n  log:\n    level: error\n")

	dialIn(t, running, "SIM-FILES-2")

	id := printers(t, running)[0].ID

	for _, path := range []string{"..", "../etc/passwd", "usb/..", "usb/../../etc/passwd"} {
		read := running.get(t, "/api/v1/devices/"+id+"/files?path="+url.QueryEscape(path))
		require.Equalf(t, http.StatusBadRequest, read.status,
			"listing %q answered %d: %s", path, read.status, read.body)
		require.Containsf(t, read.body, connectorapi.PathNotAllowed.Code().String(),
			"listing %q answered without the code a client acts on: %s", path, read.body)
	}
}

// A path the printer itself refuses is answered with a fault carrying the code
// and the machine's own word, on the published surface.
//
// It is checked here and not only at the driver because the code is decided
// on the way up: an error nothing there recognises reaches a caller as
// core.internal/UNEXPECTED with the detail redacted. A printer in ERROR
// refuses every SEND_FILE_INFO, so its Files tab reads every listing this way.
func TestAPathThePrinterRefusesIsAFaultOnThePublishedSurface(t *testing.T) {
	t.Parallel()

	running := start(t, t.TempDir(), "core:\n  log:\n    level: error\n")

	dialIn(t, running, "SIM-FILES-REFUSED")

	id := printers(t, running)[0].ID

	read := running.get(t,
		"/api/v1/devices/"+id+"/files?path="+url.QueryEscape("usb/nothing-here"))

	require.Equalf(t, http.StatusOK, read.status,
		"a path the printer refused answered %d: %s", read.status, read.body)
	require.Contains(t, read.body, `"fault":`, "the refusal is not the listing's fault")
	require.Contains(t, read.body, connectorapi.ListingRefused.Code().String(),
		"the refusal carries no code a client acts on")
	require.Contains(t, read.body, "usb/nothing-here",
		"the refusal does not name the path that was asked for")
	require.NotContains(t, read.body, "core.internal/UNEXPECTED",
		"a printer's refusal is reported as this server failing")
	require.Contains(t, read.body, `"detail":"FILE_NOT_FOUND"`,
		"the printer's own word is not the detail a person reads")
}

// A read of a printer folder is published when it begins and when it lands,
// which is what tells a page whose route stopped waiting that the rows are in.
// The read here is the warm-up of the drive's root, which an idle printer gets
// as soon as it reports its storages.
func TestALandedReadIsPublished(t *testing.T) {
	t.Parallel()

	running := start(t, t.TempDir(), "core:\n  log:\n    level: error\n")

	seen := watch(t, running)

	dialInPrinter(t, running, "SIM-FILES-EVENT")

	frames := folderFrames(t, seen, "usb")
	require.Contains(t, frames[0].data, `"reading":true`, "the read that began was not published")
	require.Contains(t, frames[1].data, `"reading":false`, "the read that landed was not published")
	require.Contains(t, frames[1].data, `"failed":false`, "a read that landed is published as failed")
}

// A read the printer refuses lands too, and its frame says it failed.
func TestAFailedReadIsPublishedAsFailed(t *testing.T) {
	t.Parallel()

	running := start(t, t.TempDir(), "core:\n  log:\n    level: error\n")

	dialInPrinter(t, running, "SIM-FILES-EVENT-REFUSED")
	id := printers(t, running)[0].ID

	seen := watch(t, running)

	running.get(t, "/api/v1/devices/"+id+"/files?path="+url.QueryEscape("usb/nothing-here"))

	frames := folderFrames(t, seen, "usb/nothing-here")
	require.Contains(t, frames[0].data, `"reading":true`, "the read that began was not published")
	require.Contains(t, frames[1].data, `"reading":false`, "the read that landed was not published")
	require.Contains(t, frames[1].data, `"failed":true`, "a refused read is published as landed")
}

// folderFrames waits for the two files.changed frames one read of path
// publishes and returns them in order.
func folderFrames(t *testing.T, seen *eventLog, path string) []sseFrame {
	t.Helper()

	named := `"path":"` + path + `"`

	var frames []sseFrame

	require.Eventually(t, func() bool {
		frames = frames[:0]

		for _, frame := range seen.framesOfType(event.TypeDeviceFilesChanged) {
			if strings.Contains(frame.data, named) {
				frames = append(frames, frame)
			}
		}

		return len(frames) >= 2
	}, settle, 25*time.Millisecond, "the read of %s was not published twice: %v", path, seen.kinds())

	return frames
}

// A folder created over the API is a folder the next listing reports, and a
// file deleted over it is gone. It is the pair that proves the listing reads
// the machine rather than anything this server cached.
func TestAFolderCreatedOnThePrinterAppearsInTheNextListing(t *testing.T) {
	t.Parallel()

	running := start(t, t.TempDir(), "core:\n  log:\n    level: error\n")

	dialIn(t, running, "SIM-FILES-3")

	id := printers(t, running)[0].ID

	created := running.call(t, http.MethodPost, "/api/v1/devices/"+id+"/commands",
		`{"verb":"core.command.create_folder","args":{"path":"usb/fresh"}}`)
	require.Equal(t, http.StatusOK, created.status, created.body)
	require.Contains(t, created.body, `"ok":true`)

	after := filesOf(t, running, id, "usb")
	require.Contains(t, mustJSON(t, after), `"usb/fresh"`,
		"a folder created over the API is not in the listing that follows it")

	deleted := running.call(t, http.MethodPost, "/api/v1/devices/"+id+"/commands",
		`{"verb":"core.command.delete_file","args":{"path":"usb/tangle.bgcode"}}`)
	require.Equal(t, http.StatusOK, deleted.status, deleted.body)

	gone := filesOf(t, running, id, "usb")
	require.NotContains(t, mustJSON(t, gone), `"usb/tangle.bgcode"`,
		"a file deleted over the API is still in the listing that follows it")
}

// A storage with nothing left and a storage that reported no figure are two
// answers, and the API has to keep them apart: one means an upload will fail
// and the other means nobody knows. Go's zero value cannot carry the
// difference, so the wire form is a pointer and this is what pins it.
func TestAStorageThatReportedNoFreeSpaceIsNotAFullOne(t *testing.T) {
	t.Parallel()

	silent := storageOf(connectorapi.Storage{Mountpoint: "/usb", Name: "usb"})
	require.Nil(t, silent.FreeSpace,
		"a printer that reported no free space reads as a device with none left")

	full := storageOf(connectorapi.Storage{Mountpoint: "/usb", Name: "usb", FreeSpaceKnown: true})
	require.NotNil(t, full.FreeSpace, "a printer that reported nothing left reads as one that said nothing")
	require.Equal(t, int64(0), *full.FreeSpace)

	room := storageOf(connectorapi.Storage{
		Mountpoint: "/usb", Name: "usb", FreeSpace: 4096, FreeSpaceKnown: true,
	})
	require.NotNil(t, room.FreeSpace)
	require.Equal(t, int64(4096), *room.FreeSpace)
}

// filesSession is a driver session that can report files, for the cases a
// simulated printer cannot reach.
type filesSession struct {
	countedSession
}

func (*filesSession) Storages(context.Context) ([]connectorapi.Storage, error) {
	return []connectorapi.Storage{{Mountpoint: "/usb", Name: "usb"}}, nil
}

func (*filesSession) List(_ context.Context, path string) (connectorapi.Listing, error) {
	return connectorapi.Listing{Directory: connectorapi.File{Path: path, Folder: true}}, nil
}

// Reading a printer's storages and browsing its files are two permissions, not
// one. The storages are what the printer already volunteered, so a viewer reads
// them; a listing puts a SEND_FILE_INFO on the wire at a machine that runs one
// command at a time, so it takes the action a command takes.
//
// Both directions are here. A permit test without its refusal is half a test,
// and the half that matters is the one that says a viewer cannot make this
// server queue work at somebody else's printer.
func TestAViewerReadsStoragesAndDoesNotBrowseFiles(t *testing.T) {
	t.Parallel()

	plane := newPlane(t)

	// The two verbs the file half is spoken through. A driver with no profiles
	// keeps its whole baseline, so this is the resolved set as well.
	baseline := connectorapi.Set{
		connectorapi.VerbSendInfo:     {Flag: true},
		connectorapi.VerbSendFileInfo: {Flag: true},
	}

	host := plane.Arrivals("test", baseline, capability.NewResolver(profile.Registry{}, discard()), "")

	session := &filesSession{countedSession{
		identity: connectorapi.Identity{Vendor: "Prusa", Model: "MK4", Serial: "SIM-FILES-AUTHZ"},
		patches:  make(chan connectorapi.StatePatch),
	}}

	_, err := host.Admit(t.Context(), session.identity)
	require.NoError(t, err)
	require.NoError(t, host.Arrived(t.Context(), session))

	// The plane releases a session when its state channel closes, and it waits
	// for every reader on the way out. Cleanup runs newest first, so this ends
	// the pump before newPlane's own Close is reached.
	t.Cleanup(func() { close(session.patches) })

	id := plane.idOf(session.identity)

	owner, err := authz.NewSubject(humanSubjectType, config.DefaultPrincipal)
	require.NoError(t, err)

	watcher, err := authz.NewSubject(authz.TypeUser, "watcher")
	require.NoError(t, err)

	object, err := authz.NewObject(authz.TypeDevice, id)
	require.NoError(t, err)

	viewing, err := authz.NewTuple(object, authz.RelViewer, watcher)
	require.NoError(t, err)
	require.NoError(t, plane.graph.Write(t.Context(), viewing))

	stranger, err := authz.NewSubject(authz.TypeUser, "stranger")
	require.NoError(t, err)

	cases := []struct {
		who               string
		principal         string
		readsStorages     bool
		browsesTheFileset bool
	}{
		{"the owner", owner.String(), true, true},
		{"a viewer", watcher.String(), true, false},
		{"somebody holding nothing", stranger.String(), false, false},
	}

	for _, testCase := range cases {
		t.Run(testCase.who, func(t *testing.T) {
			t.Parallel()

			storages, storageErr := plane.Storages(t.Context(), testCase.principal, id, false)

			if testCase.readsStorages {
				require.NoError(t, storageErr)
				require.Len(t, storages.Items, 1)
			} else {
				require.Error(t, storageErr, "somebody holding nothing read the printer's storages")
				requireClassRefusal(t, storageErr, testCase.readsStorages)
			}

			// Asking again puts a frame on the wire, so it takes what browsing takes.
			_, askErr := plane.Storages(t.Context(), testCase.principal, id, true)
			if testCase.browsesTheFileset {
				require.NoError(t, askErr)
			} else {
				require.Error(t, askErr, "a caller who may not operate the printer made it ask for its storages")
			}

			listing, listErr := plane.Files(t.Context(), testCase.principal, id, "usb", false)

			if testCase.browsesTheFileset {
				require.NoError(t, listErr)
				require.Equal(t, "usb", listing.Directory.Path)

				return
			}

			require.Error(t, listErr, "%s browsed a printer's files", testCase.who)
			requireClassRefusal(t, listErr, testCase.readsStorages)
		})
	}
}

// listingSession counts the listings the printer was asked for, and records
// whether each was read in the background lane.
type listingSession struct {
	filesSession

	lists atomic.Int64
	// refuse is what every listing answers instead of rows, when set.
	refuse error
	// hold, when set, keeps every listing from answering until it is closed.
	hold chan struct{}
	// refuseWrite is what a file delete answers, when set.
	refuseWrite error

	mu         sync.Mutex
	background []bool
	// extra is listed after part.bgcode in every listing.
	extra []connectorapi.File
}

func (s *listingSession) List(ctx context.Context, path string) (connectorapi.Listing, error) {
	s.mu.Lock()
	s.background = append(s.background, lane.BackgroundOf(ctx) != nil)
	s.mu.Unlock()

	s.lists.Add(1)

	if s.hold != nil {
		select {
		case <-s.hold:
		case <-ctx.Done():
			return connectorapi.Listing{}, ctx.Err()
		}
	}

	if s.refuse != nil {
		return connectorapi.Listing{}, s.refuse
	}

	listing, err := s.filesSession.List(ctx, path)
	listing.Entries = append(
		listing.Entries,
		connectorapi.File{Path: path + "/part.bgcode", Name: "part.bgcode", Size: 1},
	)

	s.mu.Lock()
	listing.Entries = append(listing.Entries, s.extra...)
	s.mu.Unlock()

	return listing, err
}

// lanes is whether each listing so far was read in the background lane.
func (s *listingSession) lanes() []bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	return slices.Clone(s.background)
}

func (*listingSession) CreateFolder(context.Context, string) error { return nil }

func (s *listingSession) DeleteFile(context.Context, string) error { return s.refuseWrite }

func (*listingSession) DeleteFolder(context.Context, string) error { return nil }

func newListingSession(serial string) *listingSession {
	return &listingSession{filesSession: filesSession{countedSession{
		identity: connectorapi.Identity{Vendor: "Prusa", Model: "MK4", Serial: serial},
		patches:  make(chan connectorapi.StatePatch),
	}}}
}

// slowListingSession lists only once release is closed, as a printer does that
// takes longer than the request budget to read a large folder.
type slowListingSession struct {
	listingSession

	release chan struct{}
	asks    atomic.Int64
}

func (s *slowListingSession) List(ctx context.Context, path string) (connectorapi.Listing, error) {
	s.asks.Add(1)

	select {
	case <-s.release:
	case <-ctx.Done():
		return connectorapi.Listing{}, ctx.Err()
	}

	return s.listingSession.List(ctx, path)
}

func newSlowListingSession(serial string) *slowListingSession {
	return &slowListingSession{listingSession: *newListingSession(serial), release: make(chan struct{})}
}

// arriveListing connects session to a plane that holds listings, and returns
// the plane, the printer's id and its owner.
func arriveListing(
	t *testing.T, session connectorapi.Session, patches chan connectorapi.StatePatch,
) (plane *devices, id string, owner string) {
	t.Helper()

	plane = newPlane(t)
	plane.listings = newListingCache(1 << 20)

	baseline := connectorapi.Set{
		connectorapi.VerbSendInfo:     {Flag: true},
		connectorapi.VerbSendFileInfo: {Flag: true},
		connectorapi.VerbCreateFolder: {Flag: true},
		connectorapi.VerbDeleteFile:   {Flag: true},
		connectorapi.VerbDeleteFolder: {Flag: true},

		connectorapi.ArgumentKey(connectorapi.VerbDeleteFile, "path"): {
			Flag:     true,
			Kind:     connectorapi.KindString,
			Required: true,
		},
	}

	host := plane.Arrivals("test", baseline, capability.NewResolver(profile.Registry{}, discard()), "")

	_, err := host.Admit(t.Context(), session.Identity())
	require.NoError(t, err)
	require.NoError(t, host.Arrived(t.Context(), session))
	t.Cleanup(func() { close(patches) })

	subject, err := authz.NewSubject(humanSubjectType, config.DefaultPrincipal)
	require.NoError(t, err)

	return plane, plane.idOf(session.Identity()), subject.String()
}

// A repeat listing is answered from memory and marked stale, refresh revalidates
// it in the background, and a write through this server patches what is held.
func TestARepeatListingIsHeldUntilRefreshedOrWritten(t *testing.T) {
	t.Parallel()

	session := newListingSession("SIM-FILES-HELD")
	plane, id, owner := arriveListing(t, session, session.patches)

	list := func(refresh bool) httpapi.DeviceListing {
		listing, listErr := plane.Files(t.Context(), owner, id, "usb", refresh)
		require.NoError(t, listErr)

		return listing
	}

	require.False(t, list(false).Stale, "the first listing came from the printer")
	require.True(t, list(false).Stale, "a repeat listing asked the printer again")
	require.Equal(t, int64(1), session.lists.Load())

	session.hold = make(chan struct{})
	require.True(t, list(true).Stale, "a refresh waited for the printer")
	require.Eventually(t, func() bool { return session.lists.Load() == 2 }, settle, time.Millisecond)
	close(session.hold)

	require.Eventually(t, func() bool {
		_, reading := plane.listings.inFlight(id, "usb")

		return !reading
	}, settle, time.Millisecond)

	require.NoError(t, plane.CreateFolder(t.Context(), owner, id, "usb/new"))
	patched := list(false)
	require.True(t, patched.Stale, "a write through this server read the folder again")
	require.Contains(t, mustJSON(t, patched.Items), `"usb/new"`, "the new folder is not in the held listing")
	require.Equal(t, int64(2), session.lists.Load())
}

// A listing slower than the request is answered as being read, and a retry
// joins the read in flight, so the printer is asked once and the next visit
// finds the listing held.
func TestFiles_retryJoinsASlowListing(t *testing.T) {
	t.Parallel()

	session := newSlowListingSession("SIM-FILES-SLOW")
	plane, id, owner := arriveListing(t, session, session.patches)

	first, err := plane.Files(t.Context(), owner, id, "usb", false)
	require.NoError(t, err)
	require.True(t, first.Reading, "a read slower than the grace was not answered as reading")

	retried, err := plane.Files(t.Context(), owner, id, "usb", true)
	require.NoError(t, err)
	require.True(t, retried.Reading)

	close(session.release)
	require.Eventually(t, func() bool {
		held, _ := plane.Files(t.Context(), owner, id, "usb", false)

		return held.Stale && !held.Reading
	}, settle, time.Millisecond, "the slow listing was not kept for the next visit")
	require.Equal(t, int64(1), session.asks.Load(), "the retry asked the printer again")
}

// A folder nobody has read is answered at once as being read, with the moment
// the read began, while the printer is still working on it.
func TestAColdFolderAnswersReadingWithoutWaiting(t *testing.T) {
	t.Parallel()

	session := newSlowListingSession("SIM-FILES-COLD")
	plane, id, owner := arriveListing(t, session, session.patches)

	began := time.Now()
	listing, err := plane.Files(t.Context(), owner, id, "usb", false)
	require.NoError(t, err)
	require.Less(t, time.Since(began), 1500*time.Millisecond, "the route waited on the printer")

	require.True(t, listing.Reading)
	require.NotNil(t, listing.ReadingSince)
	require.Empty(t, listing.Items)
	require.Equal(t, "usb", listing.Directory.Path)
	require.Equal(t, int64(1), session.asks.Load())

	close(session.release)
}

// A second caller is told the read began when the first caller's did, so two
// tabs count from the same moment.
func TestASecondCallerCountsFromTheSameMoment(t *testing.T) {
	t.Parallel()

	session := newSlowListingSession("SIM-FILES-TWO")
	plane, id, owner := arriveListing(t, session, session.patches)

	first, err := plane.Files(t.Context(), owner, id, "usb", false)
	require.NoError(t, err)

	second, err := plane.Files(t.Context(), owner, id, "usb", false)
	require.NoError(t, err)

	require.NotNil(t, first.ReadingSince)
	require.NotNil(t, second.ReadingSince)
	require.True(t, first.ReadingSince.Equal(*second.ReadingSince))
	require.Equal(t, int64(1), session.asks.Load())

	close(session.release)
}

// A folder the printer reads inside the grace is answered with its rows, so a
// fast folder never shows a reading state.
func TestAFastFolderAnswersRowsInsideTheGrace(t *testing.T) {
	t.Parallel()

	session := newListingSession("SIM-FILES-FAST")
	plane, id, owner := arriveListing(t, session, session.patches)

	listing, err := plane.Files(t.Context(), owner, id, "usb", false)
	require.NoError(t, err)
	require.NotEmpty(t, listing.Items)
	require.False(t, listing.Reading)
	require.False(t, listing.Stale)
}

// The page re-reads a folder after its read lands without refresh, and that
// answer is the held one: an event never causes a read.
func TestAnAnswerAfterTheEventStartsNoRead(t *testing.T) {
	t.Parallel()

	session := newListingSession("SIM-FILES-EVENT-HELD")
	plane, id, owner := arriveListing(t, session, session.patches)

	_, err := plane.Files(t.Context(), owner, id, "usb", false)
	require.NoError(t, err)

	for range 2 {
		listing, listErr := plane.Files(t.Context(), owner, id, "usb", false)
		require.NoError(t, listErr)
		require.True(t, listing.Stale)
		require.False(t, listing.Reading)
	}

	require.Equal(t, int64(1), session.lists.Load())
}

// A Refresh of a held folder answers what is held and reads it again. A person
// waits on it, so it is no background read: those wait behind other background
// work and do not run at all while a transfer is open.
func TestARefreshOfHeldRowsIsAPersonsRead(t *testing.T) {
	t.Parallel()

	session := newListingSession("SIM-FILES-REVALIDATE")
	plane, id, owner := arriveListing(t, session, session.patches)

	_, err := plane.Files(t.Context(), owner, id, "usb", false)
	require.NoError(t, err)

	session.hold = make(chan struct{})
	defer close(session.hold)

	listing, err := plane.Files(t.Context(), owner, id, "usb", true)
	require.NoError(t, err)
	require.True(t, listing.Stale)
	require.True(t, listing.Reading)
	require.NotNil(t, listing.ReadingSince)

	require.Eventually(t, func() bool { return session.lists.Load() == 2 }, settle, time.Millisecond)
	require.Equal(t, []bool{false, false}, session.lanes(), "a person's refresh went on the background lane")
}

// Retry reads a folder whose last read failed, even when its rows landed
// moments ago.
func TestARefreshRetriesAHeldFailure(t *testing.T) {
	t.Parallel()

	session := newListingSession("SIM-FILES-FRESH-FAULT")
	plane, id, owner := arriveListing(t, session, session.patches)

	_, err := plane.Files(t.Context(), owner, id, "usb", false)
	require.NoError(t, err)

	session.refuse = connectorapi.ListingRefused.New("FILE_NOT_FOUND")
	_, err = plane.FilesNow(t.Context(), owner, id, "usb")
	require.Error(t, err)

	failed, err := plane.Files(t.Context(), owner, id, "usb", false)
	require.NoError(t, err)
	require.NotNil(t, failed.Fault, "the module read's failure was not held")

	retried, err := plane.Files(t.Context(), owner, id, "usb", true)
	require.NoError(t, err)
	require.Nil(t, retried.Fault, "a refresh answered the held failure without a read")
	require.Eventually(t, func() bool { return session.lists.Load() == 3 }, settle, time.Millisecond,
		"a refresh of a held failure asked the printer nothing")
}

// A folder nobody has read is a person's read, which goes ahead of background
// reads.
func TestAColdReadIsAPersonsRead(t *testing.T) {
	t.Parallel()

	session := newListingSession("SIM-FILES-PERSON")
	plane, id, owner := arriveListing(t, session, session.patches)

	_, err := plane.Files(t.Context(), owner, id, "usb", true)
	require.NoError(t, err)
	require.Equal(t, []bool{false}, session.lanes())
}

// A read the printer refuses is answered as a fault, not an error, and held
// until the next read; a refresh starts that read and answers it is reading.
func TestAFailedReadIsAnsweredAsAFault(t *testing.T) {
	t.Parallel()

	session := newListingSession("SIM-FILES-FAULT")
	session.refuse = connectorapi.ListingRefused.New("FILE_NOT_FOUND")
	plane, id, owner := arriveListing(t, session, session.patches)

	failed, err := plane.Files(t.Context(), owner, id, "usb", false)
	require.NoError(t, err)
	require.NotNil(t, failed.Fault, "a refused read answered no fault")
	require.Equal(t, connectorapi.ListingRefused.Code(), failed.Fault.Code)
	require.Empty(t, failed.Items)

	held, err := plane.Files(t.Context(), owner, id, "usb", false)
	require.NoError(t, err)
	require.NotNil(t, held.Fault, "the failure was not held")
	require.Equal(t, int64(1), session.lists.Load(), "a held failure asked the printer again")

	session.hold = make(chan struct{})
	defer close(session.hold)

	retried, err := plane.Files(t.Context(), owner, id, "usb", true)
	require.NoError(t, err)
	require.True(t, retried.Reading)
	require.Nil(t, retried.Fault)
}

// A read that ended without a fault of its own, a dropped connection or its
// bound, is answered as one a caller may ask again for, naming the folder.
func TestAnUnfinishedReadMayBeAskedAgain(t *testing.T) {
	t.Parallel()

	payload := listingFault("usb", context.DeadlineExceeded)
	require.Equal(t, httpfault.CodeListingUnfinished(), payload.Code)
	require.True(t, payload.Retryable)
	require.Contains(t, mustJSON(t, payload.Params), "usb")
}

// A module that asks for what this server holds of a path is answered at
// once. Nothing held is answered with no rows while the read it starts runs,
// and the rows that read brought are answered after it without the printer.
func TestAModuleCachedListingNeverWaitsForThePrinter(t *testing.T) {
	t.Parallel()

	session := newSlowListingSession("SIM-FILES-CACHED")
	plane, id, owner := arriveListing(t, session, session.patches)
	fleet := moduleFleet{fleet: plane}
	cached := deviceapi.FilesParams{ID: id, Path: "usb", Cached: true}

	began := time.Now()
	cold, err := fleet.listing(t.Context(), owner, cached)
	require.NoError(t, err)
	require.Less(t, time.Since(began), listingGrace, "a cached read waited the grace a person's read is given")
	require.True(t, cold.Reading, "a cached read of nothing held did not say it reads")
	require.Empty(t, cold.Items)

	// Nothing is held of the path yet, so the module is not told it is a
	// folder: it may be a file.
	unread, err := fleet.Files(t.Context(), deviceapi.FilesParams{Principal: owner, ID: id, Path: "usb", Cached: true})
	require.NoError(t, err)
	require.Nil(t, unread.Entry, "a path nothing is held of was answered as an entry")
	require.Empty(t, unread.Items)

	close(session.release)
	require.Eventually(t, func() bool {
		held, readErr := fleet.listing(t.Context(), owner, cached)

		return readErr == nil && !held.Reading && len(held.Items) > 0
	}, settle, time.Millisecond, "the read the cached ask started never landed")

	read, err := fleet.Files(t.Context(), deviceapi.FilesParams{Principal: owner, ID: id, Path: "usb", Cached: true})
	require.NoError(t, err)
	require.NotNil(t, read.Entry, "a held folder was answered with no entry")
	require.True(t, read.Entry.Folder)
	require.NotEmpty(t, read.Items)

	asked := session.lists.Load()
	_, err = fleet.listing(t.Context(), owner, cached)
	require.NoError(t, err)
	require.Equal(t, asked, session.lists.Load(), "a cached read of a held folder asked the printer")
}

// A cached read of a path whose last read failed says no entry and no rows,
// and puts no second read on the printer for that ask.
func TestAModuleCachedListingOfAFailedPathAsksThePrinterNoMore(t *testing.T) {
	t.Parallel()

	session := newListingSession("SIM-FILES-CACHED-FAILED")
	session.refuse = errors.New("the printer refused the listing")
	plane, id, owner := arriveListing(t, session, session.patches)
	fleet := moduleFleet{fleet: plane}
	cached := deviceapi.FilesParams{Principal: owner, ID: id, Path: "usb", Cached: true}

	_, err := fleet.Files(t.Context(), cached)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		held, readErr := fleet.listing(t.Context(), owner, cached)

		return readErr == nil && !held.Reading && held.Fault != nil
	}, settle, time.Millisecond, "the read the cached ask started never failed")

	session.mu.Lock()
	background := slices.Clone(session.background)
	session.mu.Unlock()
	require.Equal(t, []bool{true}, background, "the read a cached ask started was not a background one")

	asked := session.lists.Load()
	failed, err := fleet.Files(t.Context(), cached)
	require.NoError(t, err)
	require.Nil(t, failed.Entry, "a path whose read failed was answered as an entry")
	require.Empty(t, failed.Items)
	require.Equal(t, asked, session.lists.Load(), "a cached read of a failed path asked the printer again")
}

// A module reads a folder through FilesNow, which waits for the printer: the
// module wire carries no reading state to wait on.
func TestAModuleListingWaitsForThePrinter(t *testing.T) {
	t.Parallel()

	session := newSlowListingSession("SIM-FILES-MODULE")
	plane, id, owner := arriveListing(t, session, session.patches)

	answered := make(chan httpapi.DeviceListing, 1)

	go func() {
		listing, err := plane.FilesNow(t.Context(), owner, id, "usb")
		assert.NoError(t, err)

		answered <- listing
	}()

	select {
	case <-answered:
		t.Fatal("FilesNow answered before the printer did")
	case <-time.After(50 * time.Millisecond):
	}

	close(session.release)
	require.NotEmpty(t, (<-answered).Items)
}

// A stick pulled out after the printer connected reaches a reader who asks
// again, and the answer that moved the list is published once, not again for an
// answer that repeats it.
func TestAPulledStickIsReportedWhenThePrinterIsAskedAgain(t *testing.T) {
	t.Parallel()

	running := start(t, t.TempDir(), "core:\n  log:\n    level: error\n")

	printer := dialInPrinter(t, running, "SIM-STICK-1")
	id := printers(t, running)[0].ID

	seen := watch(t, running)

	require.True(t, printer.RemoveStorage("/usb"))

	asked := askStorages(t, running, id)
	require.True(t, asked.Reported, "the printer answered and the route says it has not")
	require.Empty(t, asked.Items, "the stick was pulled out and the printer still lists it")

	require.Eventually(t, func() bool { return seen.saw(event.TypeDeviceStoragesChanged) },
		settle, 25*time.Millisecond, "the pulled stick was never published")

	askStorages(t, running, id)
	time.Sleep(200 * time.Millisecond)
	require.Equal(t, 1, seen.count(event.TypeDeviceStoragesChanged),
		"an answer repeating the held list was published")
}

func askStorages(t *testing.T, running *process, id string) httpapi.DeviceStorages {
	t.Helper()

	read := running.get(t, "/api/v1/devices/"+id+"/storages?refresh=true")
	require.Equal(t, http.StatusOK, read.status, read.body)

	var body httpapi.DeviceStorages
	require.NoError(t, json.Unmarshal([]byte(read.body), &body))

	return body
}

func storagesOf(t *testing.T, running *process, id string) []httpapi.DeviceStorage {
	t.Helper()

	read := running.get(t, "/api/v1/devices/"+id+"/storages")
	require.Equal(t, http.StatusOK, read.status, read.body)

	var body struct {
		Items []httpapi.DeviceStorage `json:"items"`
	}

	require.NoError(t, json.Unmarshal([]byte(read.body), &body))

	return body.Items
}

func filesOf(t *testing.T, running *process, id, path string) httpapi.DeviceListing {
	t.Helper()

	read := running.get(t, "/api/v1/devices/"+id+"/files?path="+url.QueryEscape(path))
	require.Equal(t, http.StatusOK, read.status, read.body)

	var listing httpapi.DeviceListing

	require.NoError(t, json.Unmarshal([]byte(read.body), &listing))

	return listing
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()

	encoded, err := json.Marshal(value)
	require.NoError(t, err)

	return string(encoded)
}

// holdListing puts rows for path into plane's cache, as a read that landed would.
func holdListing(plane *devices, id, path string, items ...httpapi.DeviceFile) {
	plane.listings.put(id, path, httpapi.DeviceListing{
		Directory: httpapi.DeviceFile{Path: path, Folder: true}, Items: items,
	})
}

func itemPaths(listing httpapi.DeviceListing) []string {
	paths := make([]string, 0, len(listing.Items))
	for _, item := range listing.Items {
		paths = append(paths, item.Path)
	}

	return paths
}

// heldPaths is the folder held for path, without asking the printer.
func heldPaths(t *testing.T, plane *devices, owner, id, path string) []string {
	t.Helper()

	listing, err := plane.Files(t.Context(), owner, id, path, false)
	require.NoError(t, err)

	return itemPaths(listing)
}

// A file deleted through this server leaves its folder's held listing without
// that one entry, and no folder is read again.
func TestADeleteRemovesOneEntryAndReadsNothing(t *testing.T) {
	t.Parallel()

	session := newListingSession("SIM-WRITE-DELETE")
	plane, id, owner := arriveListing(t, session, session.patches)

	holdListing(plane, id, "usb", httpapi.DeviceFile{Path: "usb/a.gcode"}, httpapi.DeviceFile{Path: "usb/b.gcode"})
	holdListing(plane, id, "usb/x")

	require.NoError(t, plane.DeleteFile(t.Context(), owner, id, "usb/a.gcode"))

	require.Equal(t, []string{"usb/b.gcode"}, heldPaths(t, plane, owner, id, "usb"))
	_, held := plane.listings.get(id, "usb/x")
	require.True(t, held, "a delete forgot a folder it did not touch")
	require.Zero(t, session.lists.Load(), "a delete made the printer list a folder")
}

// A folder asked with a leading slash is the folder a write names without
// one, so the delete patches what that request is answered with.
func TestAListingAskedWithALeadingSlashIsPatched(t *testing.T) {
	t.Parallel()

	session := newListingSession("SIM-WRITE-SLASH")
	plane, id, owner := arriveListing(t, session, session.patches)

	holdListing(plane, id, "usb", httpapi.DeviceFile{Path: "usb/a.gcode"}, httpapi.DeviceFile{Path: "usb/b.gcode"})

	require.NoError(t, plane.DeleteFile(t.Context(), owner, id, "usb/a.gcode"))

	require.Equal(t, []string{"usb/b.gcode"}, heldPaths(t, plane, owner, id, "/usb"))
	require.Zero(t, session.lists.Load(), "a slashed path missed the held listing")
}

// A delete sent as a command patches the held listing as the route does.
func TestADeleteThroughTheCommandRoutePatches(t *testing.T) {
	t.Parallel()

	session := newListingSession("SIM-WRITE-COMMAND")
	plane, id, owner := arriveListing(t, session, session.patches)

	holdListing(plane, id, "usb", httpapi.DeviceFile{Path: "usb/a.gcode"}, httpapi.DeviceFile{Path: "usb/b.gcode"})

	_, err := plane.Command(t.Context(), owner, id, httpapi.PrinterCommand{
		Verb: string(connectorapi.VerbDeleteFile),
		Args: map[string]string{"path": "/usb/a.gcode"},
	})
	require.NoError(t, err)

	require.Equal(t, []string{"usb/b.gcode"}, heldPaths(t, plane, owner, id, "usb"))
	require.Zero(t, session.lists.Load())
}

// The storage is FAT, so a delete naming a file by its short name removes the
// entry listed under its long one.
func TestADeleteNamedByItsShortNameRemovesTheListedEntry(t *testing.T) {
	t.Parallel()

	session := newListingSession("SIM-WRITE-SHORT")
	plane, id, owner := arriveListing(t, session, session.patches)

	holdListing(plane, id, "usb", httpapi.DeviceFile{Path: "usb/a.gcode", Name: "A~1.GCO"})

	require.NoError(t, plane.DeleteFile(t.Context(), owner, id, "usb/A~1.GCO"))

	require.Empty(t, heldPaths(t, plane, owner, id, "usb"))
}

func TestANewFolderIsAddedToItsParent(t *testing.T) {
	t.Parallel()

	session := newListingSession("SIM-WRITE-FOLDER")
	plane, id, owner := arriveListing(t, session, session.patches)

	holdListing(plane, id, "usb", httpapi.DeviceFile{Path: "usb/a.gcode"})

	require.NoError(t, plane.CreateFolder(t.Context(), owner, id, "usb/new"))

	listing, err := plane.Files(t.Context(), owner, id, "usb", false)
	require.NoError(t, err)
	require.Contains(t, listing.Items, httpapi.DeviceFile{Path: "usb/new", Name: "new", Folder: true})
	require.Zero(t, session.lists.Load())
}

// A deleted folder leaves its parent, and what was held for it and under it
// is forgotten.
func TestDeletingAFolderForgetsItsOwnListing(t *testing.T) {
	t.Parallel()

	session := newListingSession("SIM-WRITE-RMDIR")
	plane, id, owner := arriveListing(t, session, session.patches)

	holdListing(plane, id, "usb",
		httpapi.DeviceFile{Path: "usb/a.gcode"}, httpapi.DeviceFile{Path: "usb/x", Folder: true})
	holdListing(plane, id, "usb/x", httpapi.DeviceFile{Path: "usb/x/y", Folder: true})
	holdListing(plane, id, "usb/x/y")

	require.NoError(t, plane.DeleteFolder(t.Context(), owner, id, "usb/x"))

	require.Equal(t, []string{"usb/a.gcode"}, heldPaths(t, plane, owner, id, "usb"))

	for _, gone := range []string{"usb/x", "usb/x/y"} {
		_, held := plane.listings.get(id, gone)
		require.False(t, held, "%s is still held after its folder was deleted", gone)
	}
}

// A write that failed leaves its folder unknown, and only that folder.
func TestAFailedWriteForgetsOnlyThatFolder(t *testing.T) {
	t.Parallel()

	session := newListingSession("SIM-WRITE-FAILED")
	session.refuseWrite = connectorapi.ListingRefused.New("FILE_NOT_FOUND")
	plane, id, owner := arriveListing(t, session, session.patches)

	holdListing(plane, id, "usb", httpapi.DeviceFile{Path: "usb/a.gcode"})
	holdListing(plane, id, "local", httpapi.DeviceFile{Path: "local/c.gcode"})

	require.Error(t, plane.DeleteFile(t.Context(), owner, id, "usb/a.gcode"))

	_, held := plane.listings.get(id, "usb")
	require.False(t, held, "a failed write kept its folder")
	_, held = plane.listings.get(id, "local")
	require.True(t, held, "a failed write forgot another folder")
}

// A file a send landed is added to its held folder as the printer reports it,
// and the change is published.
func TestALandedSendIsAddedToTheHeldFolder(t *testing.T) {
	t.Parallel()

	session := newListingSession("SIM-WRITE-SEND")
	plane, id, owner := arriveListing(t, session, session.patches)
	entry, ok := plane.entry(id)
	require.True(t, ok)

	seen := watchFolders(t, plane, owner)

	holdListing(plane, id, "usb", httpapi.DeviceFile{Path: "usb/a.gcode"})

	plane.fileChanged(entry, verbSend, "usb/n.bgcode", &connectorapi.File{Size: 42}, nil)

	listing, err := plane.Files(t.Context(), owner, id, "usb", false)
	require.NoError(t, err)
	require.Equal(t, []string{"usb/a.gcode", "usb/n.bgcode"}, itemPaths(listing))
	require.Equal(t, int64(42), listing.Items[1].Size)

	seen.requireLanded(t, "usb")
	require.Zero(t, session.lists.Load())
}

// A send that lands while its folder is being read is not lost: the printer may
// have listed the folder before the file arrived, so the send is applied to the
// rows the read lands.
func TestASendDuringAWarmUpKeepsItsEntry(t *testing.T) {
	t.Parallel()

	session := newSlowListingSession("SIM-WRITE-WARM")
	plane, id, owner := arriveListing(t, session, session.patches)
	entry, ok := plane.entry(id)
	require.True(t, ok)

	seen := watchFolders(t, plane, owner)

	warming, err := plane.Files(t.Context(), owner, id, "usb", false)
	require.NoError(t, err)
	require.True(t, warming.Reading)

	plane.fileChanged(entry, verbSend, "usb/n.bgcode", &connectorapi.File{Size: 42}, nil)
	seen.requireLanded(t, "usb")

	close(session.release)
	require.Eventually(t, func() bool {
		_, reading := plane.listings.inFlight(id, "usb")

		return !reading
	}, settle, time.Millisecond)

	listing, held := plane.listings.get(id, "usb")
	require.True(t, held, "a read across the send was thrown away")
	require.Contains(t, itemPaths(listing), "usb/n.bgcode")
}

// folderWatcher is one person's subscription to files.changed.
type folderWatcher struct {
	mu     sync.Mutex
	frames []string
}

func watchFolders(t *testing.T, plane *devices, principal string) *folderWatcher {
	t.Helper()

	sub, err := plane.hub.Subscribe(context.WithoutCancel(t.Context()), stream.SubscribeRequest{
		Principal: principal,
		Encoding:  stream.EncodingJSON,
		Types:     []string{event.TypeDeviceFilesChanged},
	})
	require.NoError(t, err)

	t.Cleanup(sub.Close)

	watcher := &folderWatcher{}

	go func() {
		for batch := range sub.Batches() {
			watcher.mu.Lock()

			for _, frame := range batch.Events {
				watcher.frames = append(watcher.frames, string(frame.Bytes))
			}

			watcher.mu.Unlock()
		}
	}()

	return watcher
}

// requireLanded waits for a frame saying path changed and is not being read.
func (w *folderWatcher) requireLanded(t *testing.T, path string) {
	t.Helper()

	require.Eventually(t, func() bool {
		w.mu.Lock()
		defer w.mu.Unlock()

		for _, frame := range w.frames {
			if strings.Contains(frame, `"path":"`+path+`"`) && strings.Contains(frame, `"reading":false`) {
				return true
			}
		}

		return false
	}, settle, 5*time.Millisecond, "no files.changed for %s", path)
}
