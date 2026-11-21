package app

import (
	"context"
	"encoding/json"
	"maps"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/prusa3d/prusa-connect/internal/app/config"
	"github.com/prusa3d/prusa-connect/internal/authz"
	"github.com/prusa3d/prusa-connect/internal/printer/capability"
	"github.com/prusa3d/prusa-connect/internal/printer/profile"
	"github.com/prusa3d/prusa-connect/internal/transport/http/httpapi"
	"github.com/prusa3d/prusa-connect/sdk/connectorapi"
	"github.com/prusa3d/prusa-connect/sdk/transferapi"
)

// walkSession is a printer holding two print files, a model and a folder on
// its USB stick. It counts what it was asked about each file, and holds every
// answer about a file until block is closed.
type walkSession struct {
	countedSession

	block chan struct{}
	// skew is how far the time in an answer about a file is off its listing.
	skew int64

	mu    sync.Mutex
	asked map[string]int
	// open is how many questions about a file are waiting for block now.
	open int
}

func newWalkSession(serial string) *walkSession {
	block := make(chan struct{})
	close(block)

	return &walkSession{
		countedSession: countedSession{
			identity: connectorapi.Identity{Vendor: "Prusa", Model: "MK4", Serial: serial},
			patches:  make(chan connectorapi.StatePatch),
		},
		block: block,
		asked: map[string]int{},
	}
}

func (*walkSession) Storages(context.Context) ([]connectorapi.Storage, error) {
	return []connectorapi.Storage{{Mountpoint: "/usb", Name: "usb"}}, nil
}

//nolint:gochecknoglobals // a fixture, read-only.
var walkFiles = []connectorapi.File{
	{Path: "usb/a.bgcode", Name: "A~1.BGC", Size: 10, Modified: 1},
	{Path: "usb/b.gcode", Size: 20, Modified: 2},
	{Path: "usb/model.stl", Size: 30, Modified: 3},
	{Path: "usb/folder", Folder: true},
}

func (s *walkSession) List(ctx context.Context, path string) (connectorapi.Listing, error) {
	if path == "usb" {
		return connectorapi.Listing{Directory: connectorapi.File{Path: "usb", Folder: true}, Entries: walkFiles}, nil
	}

	s.mu.Lock()
	s.asked[path]++
	s.open++
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.open--
		s.mu.Unlock()
	}()

	select {
	case <-s.block:
	case <-ctx.Done():
		return connectorapi.Listing{}, ctx.Err()
	}

	for _, file := range walkFiles {
		if file.Path == path {
			file.Modified += s.skew

			return connectorapi.Listing{Directory: file}, nil
		}
	}

	return connectorapi.Listing{}, connectorapi.ErrNoFilesystem
}

func (s *walkSession) waiting() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.open
}

func (s *walkSession) askedAbout() map[string]int {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make(map[string]int, len(s.asked))
	maps.Copy(out, s.asked)

	return out
}

// arriveWalking admits the session on a plane with a preview cache, reports
// the printer's state, and returns the plane, the printer's id and an owner.
func arriveWalking(t *testing.T, session *walkSession, state string) (*devices, string, string) {
	t.Helper()

	plane := newPlane(t)
	plane.previews = newPreviewCache(1 << 20)
	plane.prefetchThumbnails = true

	baseline := connectorapi.Set{
		connectorapi.VerbSendInfo:                       {Flag: true},
		connectorapi.VerbSendFileInfo:                   {Flag: true},
		connectorapi.Capability(httpapi.StartPrintVerb): {Flag: true},
	}

	host := plane.Arrivals("test", baseline, capability.NewResolver(profile.Registry{}, discard()), "")

	_, err := host.Admit(t.Context(), session.identity)
	require.NoError(t, err)
	require.NoError(t, host.Arrived(t.Context(), session))
	t.Cleanup(func() { close(session.patches) })

	session.patches <- connectorapi.StatePatch{Path: printerStatePath, Value: json.RawMessage(`"` + state + `"`)}

	id := plane.idOf(session.identity)
	require.Eventually(t, func() bool { return plane.entries[id].idle() == (state == "IDLE") },
		time.Second, time.Millisecond)

	owner, err := authz.NewSubject(humanSubjectType, config.DefaultPrincipal)
	require.NoError(t, err)

	return plane, id, owner.String()
}

// walked waits for the printer's thumbnail walk to return.
func walked(t *testing.T, plane *devices, id string) {
	t.Helper()

	plane.mu.Lock()
	entry := plane.entries[id]
	plane.mu.Unlock()

	entry.mu.Lock()
	done := entry.prefetched
	entry.mu.Unlock()

	require.NotNil(t, done, "no walk started")

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the walk did not return")
	}
}

// An idle printer is asked about each print file once, and a file that carries
// no picture is remembered, so the next listing asks about nothing.
func TestAListingFetchesThePicturesOfItsPrintFilesOnce(t *testing.T) {
	t.Parallel()

	session := newWalkSession("SIM-WALK-1")
	plane, id, owner := arriveWalking(t, session, "IDLE")

	_, err := plane.Files(t.Context(), owner, id, "usb", false)
	require.NoError(t, err)
	walked(t, plane, id)

	require.Equal(t, map[string]int{"usb/a.bgcode": 1, "usb/b.gcode": 1}, session.askedAbout())

	_, err = plane.Files(t.Context(), owner, id, "usb", true)
	require.NoError(t, err)
	walked(t, plane, id)

	require.Equal(t, map[string]int{"usb/a.bgcode": 1, "usb/b.gcode": 1}, session.askedAbout(),
		"a file with no picture was asked about again")

	_, err = plane.Thumbnail(t.Context(), owner, id, "usb/a.bgcode", 10, 1)
	require.ErrorContains(t, err, "NO_THUMBNAIL")
	require.Equal(t, 1, session.askedAbout()["usb/a.bgcode"], "the remembered verdict was not used")
}

// A printer asked about a file this server is still sending it answers with no
// picture, under the length and the time the whole file will have. That is
// not a verdict about the file: nothing is remembered while the transfer
// runs, so the file is asked about again, and remembered once it has landed.
func TestAFileStillArrivingIsNotRememberedAsHavingNoPicture(t *testing.T) {
	t.Parallel()

	session := newWalkSession("SIM-WALK-ARRIVING")
	plane, id, owner := arriveWalking(t, session, "IDLE")

	origin, err := deviceOrigin(id)
	require.NoError(t, err)

	_, err = plane.transfers.begin(
		transferapi.Record{TransferID: "t-1", Device: origin.String(), Destination: "usb/a.bgcode"},
		owner, func() {})
	require.NoError(t, err)

	for range 2 {
		_, err = plane.Thumbnail(t.Context(), owner, id, "usb/a.bgcode", 10, 1)
		require.ErrorContains(t, err, "NO_THUMBNAIL")
	}

	require.Equal(t, 2, session.askedAbout()["usb/a.bgcode"],
		"no picture was remembered for a file that had not landed")

	plane.transfers.end("t-1")

	for range 2 {
		_, err = plane.Thumbnail(t.Context(), owner, id, "usb/a.bgcode", 10, 1)
		require.ErrorContains(t, err, "NO_THUMBNAIL")
	}

	require.Equal(t, 3, session.askedAbout()["usb/a.bgcode"],
		"a landed file with no picture was asked about again")
}

// A print names its file by the printer's short name, with no version. The
// listing that is held names the same file, so what the walk learned answers
// without asking the printer again (CL-266).
func TestAPrintsShortNameIsAnsweredFromTheHeldListing(t *testing.T) {
	t.Parallel()

	session := newWalkSession("SIM-WALK-5")
	plane, id, owner := arriveWalking(t, session, "IDLE")
	plane.listings = newListingCache(1 << 20)

	_, err := plane.Files(t.Context(), owner, id, "usb", false)
	require.NoError(t, err)
	walked(t, plane, id)

	_, err = plane.Thumbnail(t.Context(), owner, id, "/usb/A~1.BGC", 0, 0)
	require.ErrorContains(t, err, "NO_THUMBNAIL")
	require.Equal(t, map[string]int{"usb/a.bgcode": 1, "usb/b.gcode": 1}, session.askedAbout(),
		"the printer was asked again")
}

// A printer can name a file's time a second off its listing, because FAT keeps
// time in two-second steps. The picture is still found under the listed
// version, so the next listing and the Files tab ask about nothing (CL-266).
func TestAFileTimedASecondOffItsListingIsAskedAboutOnce(t *testing.T) {
	t.Parallel()

	session := newWalkSession("SIM-WALK-6")
	session.skew = 1
	plane, id, owner := arriveWalking(t, session, "IDLE")

	_, err := plane.Files(t.Context(), owner, id, "usb", false)
	require.NoError(t, err)
	walked(t, plane, id)

	_, err = plane.Files(t.Context(), owner, id, "usb", true)
	require.NoError(t, err)
	walked(t, plane, id)

	_, err = plane.Thumbnail(t.Context(), owner, id, "usb/a.bgcode", 10, 1)
	require.ErrorContains(t, err, "NO_THUMBNAIL")
	require.Equal(t, map[string]int{"usb/a.bgcode": 1, "usb/b.gcode": 1}, session.askedAbout(),
		"the printer was asked again")
}

// A printer that is printing is not asked about any picture.
func TestAPrintingPrinterIsNotWalked(t *testing.T) {
	t.Parallel()

	session := newWalkSession("SIM-WALK-2")
	plane, id, owner := arriveWalking(t, session, "PRINTING")

	_, err := plane.Files(t.Context(), owner, id, "usb", false)
	require.NoError(t, err)
	walked(t, plane, id)

	require.Empty(t, session.askedAbout())
}

// A page of pictures asked for at once reaches the printer one at a time, so a
// person's own command waits behind one picture and not behind the page.
func TestAPrinterIsAskedForOnePictureAtATime(t *testing.T) {
	t.Parallel()

	session := newWalkSession("SIM-WALK-4")
	session.block = make(chan struct{})
	plane, id, owner := arriveWalking(t, session, "IDLE")

	answered := make(chan error, 2)
	for _, path := range []string{"usb/a.bgcode", "usb/b.gcode"} {
		go func() {
			_, err := plane.Thumbnail(t.Context(), owner, id, path, 0, 0)
			answered <- err
		}()
	}

	require.Eventually(t, func() bool { return session.waiting() > 0 }, time.Second, time.Millisecond)
	require.Never(t, func() bool { return session.waiting() > 1 }, 100*time.Millisecond, time.Millisecond,
		"a second picture was asked for while the first was unanswered")

	close(session.block)

	for range 2 {
		require.ErrorContains(t, <-answered, "NO_THUMBNAIL")
	}

	require.Equal(t, map[string]int{"usb/a.bgcode": 1, "usb/b.gcode": 1}, session.askedAbout())
}

// A print start ends the walk while it waits on the printer.
func TestAPrintStartStopsTheWalk(t *testing.T) {
	t.Parallel()

	session := newWalkSession("SIM-WALK-3")
	session.block = make(chan struct{})
	plane, id, owner := arriveWalking(t, session, "IDLE")

	_, err := plane.Files(t.Context(), owner, id, "usb", false)
	require.NoError(t, err)
	require.Eventually(t, func() bool { return len(session.askedAbout()) == 1 }, time.Second, time.Millisecond)

	_, err = plane.Command(t.Context(), owner, id, httpapi.PrinterCommand{Verb: httpapi.StartPrintVerb})
	require.NoError(t, err)
	walked(t, plane, id)

	for path := range session.askedAbout() {
		require.True(t, strings.HasSuffix(path, "a.bgcode"), "the walk went on to %s", path)
	}
}
