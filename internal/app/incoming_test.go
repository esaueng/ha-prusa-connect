package app

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/prusa3d/prusa-connect/internal/app/config"
	"github.com/prusa3d/prusa-connect/sdk/connectorapi"
	"github.com/prusa3d/prusa-connect/sdk/fault"
)

// planeWithCopies is the storage plane over a real storage, with the
// temporary copies wired in and a clock the case moves by hand.
func planeWithCopies(t *testing.T, root string) (storagePlane, *incomingCopies, *time.Time) {
	t.Helper()

	plane := planeOver(t, root)
	clock := time.Unix(1_761_300_000, 0)

	copies := newIncomingCopies(plane.stores, slog.New(slog.DiscardHandler))
	copies.now = func() time.Time { return clock }
	plane.incoming = copies

	return plane, copies, &clock
}

// onDisk reports whether a path inside the storage exists on its disk.
func onDisk(root, name string) bool {
	_, err := os.Stat(filepath.Join(root, filepath.FromSlash(name)))

	return err == nil
}

// A temporary upload lands in a folder of its own under the temporary area,
// and the storage listing does not show that area.
func TestATemporaryCopyLandsApartAndIsNotListed(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		root := t.TempDir()
		plane, _, _ := planeWithCopies(t, root)
		who := subjectOf(config.Default().Principal)

		saved, err := plane.SaveTemporary(t.Context(), who, "bench", "part.gcode", bytes.NewReader([]byte("G28\n")))
		require.NoError(t, err)
		t.Logf("observed: the copy landed at %s", saved.Path)

		require.True(t, saved.Temporary)
		require.True(t, strings.HasPrefix(saved.Path, incomingDir+"/"), saved.Path)
		require.Equal(t, "part.gcode", filepath.Base(saved.Path))
		require.True(t, onDisk(root, saved.Path))

		_, err = plane.Save(t.Context(), who, "bench", "kept.gcode", bytes.NewReader([]byte("G28\n")))
		require.NoError(t, err)

		listed, err := plane.Files(t.Context(), who, "bench", "")
		require.NoError(t, err)

		names := make([]string, 0, len(listed))
		for _, entry := range listed {
			names = append(names, entry.Path)
		}

		require.Equal(t, []string{"kept.gcode"}, names)
	})
}

// An ordinary upload, a new folder and a move into the temporary area are all
// refused, so nothing a person keeps can land where copies are deleted.
func TestNothingAPersonKeepsCanLandInTheTemporaryArea(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		plane, _, _ := planeWithCopies(t, t.TempDir())
		who := subjectOf(config.Default().Principal)

		_, err := plane.Save(t.Context(), who, "bench", incomingDir+"/x/part.gcode", bytes.NewReader([]byte("G28\n")))
		requireInvalidPath(t, err)

		requireInvalidPath(t, plane.CreateFolder(t.Context(), who, "bench", incomingDir+"/x"))

		_, err = plane.Save(t.Context(), who, "bench", "part.gcode", bytes.NewReader([]byte("G28\n")))
		require.NoError(t, err)

		requireInvalidPath(t, plane.Move(t.Context(), who, "bench", "part.gcode", incomingDir+"/part.gcode"))
	})
}

func requireInvalidPath(t *testing.T, err error) {
	t.Helper()

	carried, ok := errors.AsType[*fault.Fault](err)
	require.True(t, ok, "not a fault: %v", err)
	require.Equal(t, fault.StorageInvalidPath.Code(), carried.Code())
}

// A send that ended is told apart from one that can still be tried again.
func TestASendThatEndedIsToldFromOneToTryAgain(t *testing.T) {
	t.Parallel()

	stopped := connectorapi.TransferFailed.New("stopped").
		With(fault.StringParam("event", "stopped"))
	failed := connectorapi.TransferFailed.New("failed").
		With(fault.StringParam("event", "aborted"))

	require.True(t, sendSettled(nil), "a file that landed")
	require.True(t, sendSettled(errHeldSame), "a printer that held the same file")
	require.True(t, sendSettled(stopped), "a transfer a person stopped")
	require.False(t, sendSettled(failed), "a transfer that failed")
	require.False(t, sendSettled(nameTaken("printer", "usb/part.gcode", false)), "a name the printer holds")
	require.False(t, sendSettled(errors.New("the printer went away")), "any other failure")
}

// After a send that ended the copy is gone. After one that failed it stays,
// so the same copy can be sent again.
func TestACopyIsDeletedWhenItsSendEndedAndKeptWhenItFailed(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		root := t.TempDir()
		plane, copies, _ := planeWithCopies(t, root)
		who := subjectOf(config.Default().Principal)

		landed, err := plane.SaveTemporary(t.Context(), who, "bench", "a.gcode", bytes.NewReader([]byte("G28\n")))
		require.NoError(t, err)

		failed, err := plane.SaveTemporary(t.Context(), who, "bench", "b.gcode", bytes.NewReader([]byte("G28\n")))
		require.NoError(t, err)

		copies.relayed(t.Context(), "bench", copyFolder(landed.Path), nil)
		copies.relayed(t.Context(), "bench", copyFolder(failed.Path), errors.New("the printer went away"))

		require.False(t, onDisk(root, copyFolder(landed.Path)), "the copy of a file that landed is still there")
		require.True(t, onDisk(root, failed.Path), "the copy of a failed send is gone before anyone could try again")
	})
}

// A copy another send is still relaying is not deleted when one send of it
// ends.
func TestACopyInUseIsNotDeleted(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		root := t.TempDir()
		plane, copies, clock := planeWithCopies(t, root)
		who := subjectOf(config.Default().Principal)

		saved, err := plane.SaveTemporary(t.Context(), who, "bench", "a.gcode", bytes.NewReader([]byte("G28\n")))
		require.NoError(t, err)

		folder := copyFolder(saved.Path)
		release := copies.hold("bench", folder)

		copies.relayed(t.Context(), "bench", folder, nil)
		require.True(t, onDisk(root, saved.Path))

		*clock = clock.Add(24 * time.Hour)
		copies.sweep(t.Context(), false)
		copies.sweep(t.Context(), false)
		require.True(t, onDisk(root, saved.Path), "a sweep deleted a copy that is being relayed")

		release()
		copies.sweep(t.Context(), false)
		require.False(t, onDisk(root, saved.Path))
	})
}

// The first sweep after a start deletes every copy made before the start,
// because nothing that used one outlives a restart, and leaves one made since.
// Later sweeps delete the copies older than incomingKeep and leave the
// younger ones and every kept file alone.
func TestTheSweepDeletesLeftoversAndNothingElse(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		root := t.TempDir()
		plane, copies, clock := planeWithCopies(t, root)
		who := subjectOf(config.Default().Principal)

		left, err := plane.SaveTemporary(t.Context(), who, "bench", "left.gcode", bytes.NewReader([]byte("G28\n")))
		require.NoError(t, err)

		_, err = plane.Save(t.Context(), who, "bench", "models/kept.gcode", bytes.NewReader([]byte("G28\n")))
		require.NoError(t, err)

		// The process starts again a minute later, and a copy is made at once.
		*clock = clock.Add(time.Minute)
		copies.started = *clock

		fresh, err := plane.SaveTemporary(t.Context(), who, "bench", "fresh.gcode", bytes.NewReader([]byte("G28\n")))
		require.NoError(t, err)

		copies.sweep(t.Context(), false)
		require.False(t, onDisk(root, left.Path), "a copy left from before the start survived the first sweep")
		require.True(t, onDisk(root, fresh.Path), "the first sweep deleted a copy made since the start")

		older, err := plane.SaveTemporary(t.Context(), who, "bench", "older.gcode", bytes.NewReader([]byte("G28\n")))
		require.NoError(t, err)

		*clock = clock.Add(incomingKeep / 2)

		younger, err := plane.SaveTemporary(
			t.Context(),
			who,
			"bench",
			"younger.gcode",
			bytes.NewReader([]byte("G28\n")),
		)
		require.NoError(t, err)

		*clock = clock.Add(incomingKeep/2 + time.Minute)
		copies.sweep(t.Context(), false)

		require.False(t, onDisk(root, older.Path), "a copy older than the keep survived")
		require.True(t, onDisk(root, younger.Path), "a copy younger than the keep was deleted")
		require.True(t, onDisk(root, "models/kept.gcode"), "the sweep deleted a file somebody kept")
	})
}
