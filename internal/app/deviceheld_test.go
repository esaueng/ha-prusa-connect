package app

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/prusa3d/prusa-connect/internal/app/config"
	"github.com/prusa3d/prusa-connect/internal/authz"
	"github.com/prusa3d/prusa-connect/internal/printer/capability"
	"github.com/prusa3d/prusa-connect/internal/printer/profile"
	"github.com/prusa3d/prusa-connect/internal/transport/http/httpapi"
	"github.com/prusa3d/prusa-connect/internal/transport/http/httpfault"
	"github.com/prusa3d/prusa-connect/sdk/connectorapi"
	"github.com/prusa3d/prusa-connect/sdk/deviceapi"
	"github.com/prusa3d/prusa-connect/sdk/fault"
)

// heldLength is how long the file the stick below starts with is, and the
// stored part.gcode too.
const heldLength = 999

// stickSession is a printer whose stick holds usb/part.gcode, put there by
// hand, and a folder named usb/dir.gcode. It answers a question about one path
// the way the firmware does: the node itself, or a refusal for a name it does
// not hold. A file it is sent lands with a modification time of its own clock,
// and it records every delete, send and command.
type stickSession struct {
	filesSession

	mu       sync.Mutex
	files    map[string]connectorapi.File
	clock    int64
	deleted  []string
	sent     []string
	commands []connectorapi.Command
	// hold, when set, keeps a send open until it is closed.
	hold chan struct{}
	// mute makes every question about a path go unanswered.
	mute bool
	// refuse, when set, is the machine reason every command is refused with.
	refuse string
	// fail, when set, is the machine reason every command is taken and then
	// fails with.
	fail string
}

func newStickSession(serial string) *stickSession {
	return &stickSession{
		filesSession: filesSession{countedSession{
			identity: connectorapi.Identity{Vendor: "Prusa", Model: "MK4", Serial: serial},
			patches:  make(chan connectorapi.StatePatch),
		}},
		files: map[string]connectorapi.File{
			"usb/part.gcode": {
				Path:        "usb/part.gcode",
				Name:        "PART.GCO",
				DisplayName: "part.gcode",
				Size:        heldLength,
				Modified:    100,
			},
			"usb/dir.gcode": {Path: "usb/dir.gcode", Name: "DIR.GCO", DisplayName: "dir.gcode", Folder: true},
		},
		clock: 1000,
	}
}

func (s *stickSession) List(_ context.Context, path string) (connectorapi.Listing, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	file, found := s.files[path]
	if !found || s.mute {
		return connectorapi.Listing{}, errors.New("FILE_NOT_FOUND")
	}

	return connectorapi.Listing{Directory: file}, nil
}

func (s *stickSession) DeleteFile(_ context.Context, path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.deleted = append(s.deleted, path)
	delete(s.files, path)

	return nil
}

func (*stickSession) CreateFolder(context.Context, string) error { return nil }

func (*stickSession) DeleteFolder(context.Context, string) error { return nil }

func (s *stickSession) Send(ctx context.Context, up connectorapi.Upload) error {
	s.mu.Lock()
	hold := s.hold
	s.mu.Unlock()

	if hold != nil {
		select {
		case <-hold:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	reader, err := up.Content.Open(ctx, 0)
	if err != nil {
		return err
	}

	_, _ = io.Copy(io.Discard, reader)
	_ = reader.Close()

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, taken := s.files[up.Path]; taken {
		return connectorapi.TransferRefused.New("the printer would not take the file").
			With(fault.StringParam("machine_reason", machineFileExists)).
			Wrap(connectorapi.ErrTransferRefused)
	}

	s.clock++
	s.sent = append(s.sent, up.Path)
	s.files[up.Path] = connectorapi.File{
		Path: up.Path, Name: filepath.Base(up.Path), DisplayName: filepath.Base(up.Path),
		Size: up.Content.Size(), Modified: s.clock,
	}

	return nil
}

func (s *stickSession) Do(_ context.Context, command connectorapi.Command) (connectorapi.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.commands = append(s.commands, command)

	if s.fail != "" {
		return connectorapi.Result{}, connectorapi.CommandFailed.
			New("the printer took the command and could not carry it out").
			With(fault.StringParam("verb", command.Verb.String()),
				fault.StringParam("machine_reason", s.fail))
	}

	if s.refuse != "" {
		return connectorapi.Result{
			FaultCode:     connectorapi.CommandRefused.Code().String(),
			MachineReason: s.refuse,
		}, nil
	}

	return connectorapi.Result{OK: true}, nil
}

// failing makes the printer take every command from now on and then fail it,
// giving reason.
func (s *stickSession) failing(reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.fail = reason
}

// refusing makes the printer refuse every command from now on, giving reason.
func (s *stickSession) refusing(reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.refuse = reason
}

// touch replaces the printer's file by hand: another file of the same length.
func (s *stickSession) touch(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.clock++
	file := s.files[path]
	file.Modified = s.clock
	s.files[path] = file
}

func (s *stickSession) deletes() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]string(nil), s.deleted...)
}

func (s *stickSession) sends() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]string(nil), s.sent...)
}

func (s *stickSession) given() []connectorapi.Command {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]connectorapi.Command(nil), s.commands...)
}

// stickRig is a plane with one stick printer on it and a storage named bench
// holding a part.gcode as long as the printer's copy.
type stickRig struct {
	plane   *devices
	session *stickSession
	id      string
	root    string
	owner   string
}

func newStickRig(t *testing.T) stickRig {
	t.Helper()

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "part.gcode"), make([]byte, heldLength), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "other.gcode"), make([]byte, heldLength), 0o600))

	graph := newGraph(t)
	plane := planeReading(t, graph, stageOne(t, root))

	baseline := connectorapi.Set{
		connectorapi.VerbSendInfo:                                 {Flag: true},
		connectorapi.VerbSendFileInfo:                             {Flag: true},
		connectorapi.VerbDeleteFile:                               {Flag: true},
		connectorapi.VerbFileUpload:                               {Flag: true},
		connectorapi.Capability(httpapi.StartPrintVerb):           {Flag: true},
		connectorapi.Capability(httpapi.StartPrintVerb + "#path"): {Flag: true, Kind: connectorapi.KindString},
	}

	host := plane.Arrivals("test", baseline, capability.NewResolver(profile.Registry{}, discard()), "")

	session := newStickSession("SIM-HELD")

	_, err := host.Admit(t.Context(), session.identity)
	require.NoError(t, err)
	require.NoError(t, host.Arrived(t.Context(), session))
	t.Cleanup(func() { close(session.patches) })

	id := plane.idOf(session.identity)

	require.NoError(t, graph.Write(t.Context(), tuple(t, authz.TypeDevice, id, authz.RelViewer, "user:watcher")))

	return stickRig{plane: plane, session: session, id: id, root: root, owner: subjectOf(config.Default().Principal)}
}

func partPrint() httpapi.DevicePrint {
	return httpapi.DevicePrint{Storage: "bench", Source: "part.gcode", Path: "usb/part.gcode"}
}

// sendOwnCopy replaces the hand-put file with this server's own copy of
// bench/part.gcode, as a person who answered Replace would.
func (r stickRig) sendOwnCopy(t *testing.T) {
	t.Helper()

	replacing := partPrint()
	replacing.Replace = true

	result, err := r.plane.Print(t.Context(), r.owner, r.id, replacing)
	require.NoError(t, err)
	require.NotNil(t, result.Transfer)
}

func isNameTaken(err error) bool {
	return fault.HasCode(err, fault.PrinterFileNameTaken.Code())
}

// A file under the name a send lands on is the person's to replace. Without
// replace the send is refused with FILE_NAME_TAKEN and the printer's file
// stays. With it the file is deleted and the new one sent, and only for
// somebody who may operate the printer.
func TestAFileOnThePrinterIsReplacedOnlyWhenAskedByAnOperator(t *testing.T) {
	t.Parallel()

	rig := newStickRig(t)
	upload := httpapi.DeviceUpload{Storage: "bench", Source: "part.gcode", Path: "usb/part.gcode"}

	_, err := rig.plane.Send(t.Context(), rig.owner, rig.id, upload)
	require.Truef(t, isNameTaken(err), "a send onto a held name answered %v, want FILE_NAME_TAKEN", err)
	require.Empty(t, rig.session.deletes(), "the printer's file was deleted without the person agreeing")

	replacing := upload
	replacing.Replace = true

	_, err = rig.plane.Send(t.Context(), "user:watcher", rig.id, replacing)
	requireClassRefusal(t, err, true)
	require.Empty(t, rig.session.deletes(), "a viewer had the printer's file deleted")

	_, err = rig.plane.Send(t.Context(), rig.owner, rig.id, replacing)
	require.NoError(t, err)
	require.Equal(t, []string{"usb/part.gcode"}, rig.session.deletes())
	require.Equal(t, []string{"usb/part.gcode"}, rig.session.sends())

	// A plain send never reuses the printer's copy, even this server's own.
	_, err = rig.plane.Send(t.Context(), rig.owner, rig.id, upload)
	require.True(t, isNameTaken(err), "a plain send reused the printer's copy: %v", err)
}

// keep_both sends beside the printer's file under the next free number and
// deletes nothing, and the transfer names where the file landed.
func TestKeepBothSendsUnderTheNextFreeName(t *testing.T) {
	t.Parallel()

	rig := newStickRig(t)
	upload := httpapi.DeviceUpload{Storage: "bench", Source: "part.gcode", Path: "usb/part.gcode", KeepBoth: true}

	first, err := rig.plane.Send(t.Context(), rig.owner, rig.id, upload)
	require.NoError(t, err)
	require.Equal(t, "usb/part[1].gcode", first.Path)

	second, err := rig.plane.Send(t.Context(), rig.owner, rig.id, upload)
	require.NoError(t, err)
	require.Equal(t, "usb/part[2].gcode", second.Path)

	require.Equal(t, []string{"usb/part[1].gcode", "usb/part[2].gcode"}, rig.session.sends())
	require.Empty(t, rig.session.deletes(), "keep_both deleted the printer's file")

	// A free name is sent where it was asked for.
	fresh := upload
	fresh.Path = "usb/new.gcode"
	landed, err := rig.plane.Send(t.Context(), rig.owner, rig.id, fresh)
	require.NoError(t, err)
	require.Equal(t, "usb/new.gcode", landed.Path)
}

// A print with keep_both starts the file it sent under the free name, never
// the printer's own file of the first name.
func TestKeepBothPrintStartsTheFileItSent(t *testing.T) {
	t.Parallel()

	rig := newStickRig(t)
	request := partPrint()
	request.KeepBoth = true

	result, err := rig.plane.Print(t.Context(), rig.owner, rig.id, request)
	require.NoError(t, err)
	require.Equal(t, "usb/part[1].gcode", result.Path)
	require.NotNil(t, result.Transfer)

	commands := rig.session.given()
	require.NotEmpty(t, commands)
	require.Equal(t, "usb/part[1].gcode", commands[len(commands)-1].Args[httpapi.StartPrintPathArgument])
}

// Both answers to a held name at once is refused before anything is asked.
func TestKeepBothAndReplaceTogetherAreRefused(t *testing.T) {
	t.Parallel()

	rig := newStickRig(t)

	_, err := rig.plane.Send(t.Context(), rig.owner, rig.id, httpapi.DeviceUpload{
		Storage: "bench", Source: "part.gcode", Path: "usb/part.gcode", Replace: true, KeepBoth: true,
	})
	require.Truef(t, fault.HasCode(err, httpfault.CodeRequestInvalid()), "the send answered %v", err)

	both := partPrint()
	both.Replace, both.KeepBoth = true, true

	_, err = rig.plane.Print(t.Context(), rig.owner, rig.id, both)
	require.Truef(t, fault.HasCode(err, httpfault.CodeRequestInvalid()), "the print answered %v", err)

	require.Empty(t, rig.session.sends())
	require.Empty(t, rig.session.deletes())
}

// A printer that holds every numbered name refuses the send as a taken name
// and is sent nothing.
func TestKeepBothWithEveryNumberTakenIsNameTaken(t *testing.T) {
	t.Parallel()

	rig := newStickRig(t)

	rig.session.mu.Lock()
	for n := 1; n <= freeNameTries; n++ {
		at := numberedName("usb/part.gcode", n)
		rig.session.files[at] = connectorapi.File{Path: at}
	}
	rig.session.mu.Unlock()

	_, err := rig.plane.Send(t.Context(), rig.owner, rig.id, httpapi.DeviceUpload{
		Storage: "bench", Source: "part.gcode", Path: "usb/part.gcode", KeepBoth: true,
	})
	require.Truef(t, isNameTaken(err), "the send answered %v, want FILE_NAME_TAKEN", err)
	require.Empty(t, rig.session.sends())
}

func TestNumberedNameKeepsTheExtension(t *testing.T) {
	t.Parallel()

	for at, want := range map[string]string{
		"usb/part.gcode":      "usb/part[3].gcode",
		"usb/a.b/cube.bgcode": "usb/a.b/cube[3].bgcode",
		"usb/part.GCODE.gz":   "usb/part[3].GCODE.gz",
		"usb/noextension":     "usb/noextension[3]",
		"usb/dir/x.y.z.gcode": "usb/dir/x.y.z[3].gcode",
	} {
		require.Equal(t, want, numberedName(at, 3), at)
	}
}

// A file the printer lists read-only is one it refuses to delete, so the
// refusal says so and the page asks without offering a replace.
func TestANameTakenByAReadOnlyFileSaysSo(t *testing.T) {
	t.Parallel()

	rig := newStickRig(t)
	upload := httpapi.DeviceUpload{Storage: "bench", Source: "part.gcode", Path: "usb/part.gcode"}

	_, err := rig.plane.Send(t.Context(), rig.owner, rig.id, upload)
	require.Falsef(t, readOnlyParam(t, err), "a writable file was said to be read-only: %v", err)

	rig.session.mu.Lock()
	held := rig.session.files["usb/part.gcode"]
	held.ReadOnly = true
	rig.session.files["usb/part.gcode"] = held
	rig.session.mu.Unlock()

	_, err = rig.plane.Send(t.Context(), rig.owner, rig.id, upload)
	require.Truef(t, readOnlyParam(t, err), "a read-only file was not said to be: %v", err)
}

// readOnlyParam is the read_only param of a FILE_NAME_TAKEN, false when it
// carries none.
func readOnlyParam(t *testing.T, err error) bool {
	t.Helper()

	require.Truef(t, isNameTaken(err), "answered %v, want FILE_NAME_TAKEN", err)

	carried, ok := errors.AsType[*fault.Fault](err)
	require.True(t, ok)

	for _, param := range carried.Params() {
		if param.Key == "read_only" {
			return param.Value.String() == "true"
		}
	}

	return false
}

// A replace confirmed while the printer is already fetching another file is
// refused as busy, and the printer's file is still there afterwards.
func TestAReplaceRefusedAsBusyKeepsThePrintersFile(t *testing.T) {
	t.Parallel()

	rig := newStickRig(t)

	hold := make(chan struct{})
	rig.session.mu.Lock()
	rig.session.hold = hold
	rig.session.mu.Unlock()

	other := make(chan error, 1)

	go func() {
		_, err := rig.plane.Send(context.WithoutCancel(t.Context()), rig.owner, rig.id,
			httpapi.DeviceUpload{Storage: "bench", Source: "other.gcode", Path: "usb/other.gcode"})
		other <- err
	}()

	require.Eventually(t, func() bool {
		return len(rig.plane.transfers.running("device:"+rig.id)) == 1
	}, 5*time.Second, 10*time.Millisecond, "the first transfer never started")

	_, err := rig.plane.Send(t.Context(), rig.owner, rig.id,
		httpapi.DeviceUpload{Storage: "bench", Source: "part.gcode", Path: "usb/part.gcode", Replace: true})
	require.ErrorIs(t, err, connectorapi.ErrTransferBusy)
	require.Empty(t, rig.session.deletes(), "a refused send deleted the printer's file")

	close(hold)
	require.NoError(t, <-other)
}

// A name the printer does not hold, or a printer that cannot say, is no reason
// to refuse: the transfer answers for itself.
func TestASendOntoAFreeNameIsNotAsked(t *testing.T) {
	t.Parallel()

	rig := newStickRig(t)

	_, err := rig.plane.Send(t.Context(), rig.owner, rig.id,
		httpapi.DeviceUpload{Storage: "bench", Source: "part.gcode", Path: "usb/new.gcode"})
	require.NoError(t, err)
	require.Empty(t, rig.session.deletes())
}

// A printer that does not describe a file it reported finished still has it,
// so the held folder gains the file as sent and is not read whole again: on a
// full USB drive that read held the printer for 15 s after every send.
func TestASendThePrinterDoesNotDescribeIsAddedAsSent(t *testing.T) {
	t.Parallel()

	rig := newStickRig(t)
	rig.plane.listings = newListingCache(1 << 20)
	holdListing(rig.plane, rig.id, "usb", httpapi.DeviceFile{Path: "usb/part.gcode"})

	rig.session.mu.Lock()
	rig.session.mute = true
	rig.session.mu.Unlock()

	_, err := rig.plane.Send(t.Context(), rig.owner, rig.id,
		httpapi.DeviceUpload{Storage: "bench", Source: "part.gcode", Path: "usb/new.gcode"})
	require.NoError(t, err)

	listing, held := rig.plane.listings.get(rig.id, "usb")
	require.True(t, held, "a send the printer did not describe forgot its folder")
	require.Equal(t, []string{"usb/part.gcode", "usb/new.gcode"}, itemPaths(listing))
	require.Equal(t, "new.gcode", listing.Items[1].Name)
	require.Equal(t, int64(heldLength), listing.Items[1].Size)
}

// A folder of that name is never deleted to make room, whatever the request says.
func TestAFolderIsNeverReplaced(t *testing.T) {
	t.Parallel()

	rig := newStickRig(t)

	_, err := rig.plane.Send(t.Context(), rig.owner, rig.id,
		httpapi.DeviceUpload{Storage: "bench", Source: "part.gcode", Path: "usb/dir.gcode", Replace: true})
	require.Truef(t, isNameTaken(err), "a replace of a folder answered %v, want FILE_NAME_TAKEN", err)
	require.Empty(t, rig.session.deletes())
}

// A print of the file this server sent to the printer, from the same version
// of the same source, with the printer's copy unchanged since, starts the copy
// there. Nothing is sent, deleted or asked about, by the HTTP route and by the
// call a module makes alike.
func TestPrintingAFileThisServerSentThereStartsItWithoutSending(t *testing.T) {
	t.Parallel()

	rig := newStickRig(t)
	rig.sendOwnCopy(t)

	result, err := rig.plane.Print(t.Context(), rig.owner, rig.id, partPrint())
	require.NoError(t, err)
	require.Nil(t, result.Transfer, "the file the printer holds was sent again")
	require.Len(t, rig.session.sends(), 1)
	require.Len(t, rig.session.deletes(), 1)

	started, err := rig.plane.StartPrint(t.Context(), rig.owner, rig.id, partPrint())
	require.NoError(t, err)
	require.Nil(t, started.Transfer, "a module's print of the held file opened a transfer")

	commands := rig.session.given()
	require.Len(t, commands, 3, "the printer was not given the three start commands")
	require.Equal(t, "usb/part.gcode", commands[2].Args[httpapi.StartPrintPathArgument])
}

// The printer's copy is taken for this file only while both sides are the
// versions the send left behind. Anything else asks.
func TestPrintingOverACopyThatIsNotThisFileAsks(t *testing.T) {
	t.Parallel()

	for name, change := range map[string]func(t *testing.T, rig stickRig){
		// A file this server has no record of sending, of the same length.
		"never sent": nil,
		// The stored file was sliced again and came out the same length.
		"source re-sliced": func(t *testing.T, rig stickRig) {
			t.Helper()
			rig.sendOwnCopy(t)

			stored := filepath.Join(rig.root, "part.gcode")
			sliced := make([]byte, heldLength)
			sliced[0] = 'G'
			require.NoError(t, os.WriteFile(stored, sliced, 0o600))
			later := time.Now().Add(time.Hour)
			require.NoError(t, os.Chtimes(stored, later, later))
		},
		// Somebody put another file of the same length there by hand.
		"printer copy replaced": func(t *testing.T, rig stickRig) {
			t.Helper()
			rig.sendOwnCopy(t)
			rig.session.touch("usb/part.gcode")
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rig := newStickRig(t)
			if change != nil {
				change(t, rig)
			}

			sends := len(rig.session.sends())
			commands := len(rig.session.given())

			_, err := rig.plane.Print(t.Context(), rig.owner, rig.id, partPrint())
			require.Truef(t, isNameTaken(err), "the print answered %v, want FILE_NAME_TAKEN", err)
			require.Len(t, rig.session.given(), commands, "the printer was told to print a file nobody chose")
			require.Len(t, rig.session.sends(), sends)
		})
	}
}

// A copy remembered under one spelling of the name is found by the other one,
// as the printer's FAT storage finds it.
func TestACopyIsFoundByEitherSpellingOfItsName(t *testing.T) {
	t.Parallel()

	rig := newStickRig(t)
	rig.sendOwnCopy(t)

	upper := partPrint()
	upper.Path = "usb/PART.GCODE"

	rig.session.mu.Lock()
	held := rig.session.files["usb/part.gcode"]
	rig.session.mu.Unlock()

	require.True(t, rig.plane.copies.holds(rig.id, httpapi.DeviceUpload(upper), identityOfStored(t, rig), held))
}

func identityOfStored(t *testing.T, rig stickRig) string {
	t.Helper()

	content, err := rig.plane.files.content(t.Context(), "bench", "part.gcode")
	require.NoError(t, err)

	return identityOfContent(content)
}

// A printer that does not answer the question before the send still refuses
// the name itself, and that refusal reaches the person as the same question.
func TestAPrintersOwnRefusalOfAHeldNameAsks(t *testing.T) {
	t.Parallel()

	rig := newStickRig(t)
	rig.session.mu.Lock()
	rig.session.mute = true
	rig.session.mu.Unlock()

	_, err := rig.plane.Send(t.Context(), rig.owner, rig.id,
		httpapi.DeviceUpload{Storage: "bench", Source: "part.gcode", Path: "usb/part.gcode"})
	require.Truef(t, isNameTaken(err), "the printer's refusal answered %v, want FILE_NAME_TAKEN", err)
}

// A module that prints onto a name the printer holds is told so by a code it
// can branch on, and may then choose another name.
func TestAModulePrintOntoAHeldNameIsNameTaken(t *testing.T) {
	t.Parallel()

	rig := newStickRig(t)
	require.NoError(t, rig.plane.graph.Write(t.Context(),
		tuple(t, authz.TypeDevice, rig.id, authz.RelOperator, printingModule)))
	require.NoError(t, rig.plane.graph.Write(t.Context(),
		tuple(t, authz.TypeResourceKind, "storage", authz.RelViewer, printingModule)))

	fleet := modulePrinting(t, rig.plane.graph, rig.plane,
		authz.ActionObserve, authz.ActionOperate, authz.ActionReadStorage)

	_, err := fleet.Print(t.Context(), deviceapi.PrintParams{
		ID: rig.id, Storage: "bench", Source: "part.gcode", Path: "usb/part.gcode",
	})
	require.ErrorIs(t, err, deviceapi.ErrNameTaken)
}

// A module that says keep_both is answered the free name its file is printed
// from, and the printer's own file stays.
func TestAModulePrintWithKeepBothAnswersTheFreeName(t *testing.T) {
	t.Parallel()

	rig := newStickRig(t)
	require.NoError(t, rig.plane.graph.Write(t.Context(),
		tuple(t, authz.TypeDevice, rig.id, authz.RelOperator, printingModule)))
	require.NoError(t, rig.plane.graph.Write(t.Context(),
		tuple(t, authz.TypeResourceKind, "storage", authz.RelViewer, printingModule)))

	fleet := modulePrinting(t, rig.plane.graph, rig.plane,
		authz.ActionObserve, authz.ActionOperate, authz.ActionReadStorage)

	started, err := fleet.Print(t.Context(), deviceapi.PrintParams{
		ID: rig.id, Storage: "bench", Source: "part.gcode", Path: "usb/part.gcode", KeepBoth: true,
	})
	require.NoError(t, err)
	require.Equal(t, "usb/part[1].gcode", started.Path)
	require.NotEmpty(t, started.TransferID)
	require.Empty(t, rig.session.deletes(), "keep_both deleted the printer's file")
}

// A module that prints the same file of its print folder again starts the copy
// it sent there, unchanged since, and sends nothing (ADR-20261023-sns rule 11).
func TestAModulePrintOfItsOwnFileAgainStartsTheHeldCopy(t *testing.T) {
	t.Parallel()

	rig := newStickRig(t)
	require.NoError(t, rig.plane.graph.Write(t.Context(),
		tuple(t, authz.TypeDevice, rig.id, authz.RelOperator, printingModule)))
	require.NoError(t, rig.plane.graph.Write(t.Context(),
		tuple(t, authz.TypeResourceKind, "storage", authz.RelViewer, printingModule)))

	fleet := modulePrinting(t, rig.plane.graph, rig.plane,
		authz.ActionObserve, authz.ActionOperate, authz.ActionReadStorage)
	fleet.data = dataHolding(t)

	first, err := fleet.Print(t.Context(), fromData(rig.id, "copy.gcode"))
	require.NoError(t, err)
	require.NotEmpty(t, first.TransferID)
	require.Eventually(t, func() bool { return len(rig.session.given()) == 1 },
		5*time.Second, 10*time.Millisecond, "the first print never started")

	again, err := fleet.Print(t.Context(), fromData(rig.id, "copy.gcode"))
	require.NoError(t, err)

	type result struct {
		TransferID string
		Sends      []string
		Commands   int
	}

	require.Equal(t, result{Sends: []string{"usb/copy.gcode"}, Commands: 2},
		result{TransferID: again.TransferID, Sends: rig.session.sends(), Commands: len(rig.session.given())})
}

// Two modules hold a file of the same name, length and time of change in their
// print folders. The copy one sent is never started for the other, which is
// refused as a print onto a held name (ADR-20261023-sns rules 7 and 11).
func TestAModulePrintNeverStartsAnotherModulesCopy(t *testing.T) {
	t.Parallel()

	const otherModule = "sink:other"

	rig := newStickRig(t)
	for _, module := range []string{printingModule, otherModule} {
		require.NoError(t, rig.plane.graph.Write(t.Context(),
			tuple(t, authz.TypeDevice, rig.id, authz.RelOperator, module)))
		require.NoError(t, rig.plane.graph.Write(t.Context(),
			tuple(t, authz.TypeResourceKind, "storage", authz.RelViewer, module)))
	}

	first := modulePrinting(t, rig.plane.graph, rig.plane,
		authz.ActionObserve, authz.ActionOperate, authz.ActionReadStorage)
	first.data = dataHolding(t)

	second := first
	second.standing.principal = otherModule
	second.data = dataHolding(t)

	changed := time.Unix(1_700_000_000, 0)
	for _, data := range []string{first.data, second.data} {
		require.NoError(t, os.Chtimes(filepath.Join(data, "print", "copy.gcode"), changed, changed))
	}

	_, err := first.Print(t.Context(), fromData(rig.id, "copy.gcode"))
	require.NoError(t, err)
	require.Eventually(t, func() bool { return len(rig.session.given()) == 1 },
		5*time.Second, 10*time.Millisecond, "the first print never started")

	_, err = second.Print(t.Context(), fromData(rig.id, "copy.gcode"))
	require.ErrorIs(t, err, deviceapi.ErrNameTaken)
	require.Equal(t, []string{"usb/copy.gcode"}, rig.session.sends())
}

// grantPrinting lets printingModule operate the rig's printer and read the
// storage kind, and is the module's fleet view.
func (r stickRig) grantPrinting(t *testing.T) moduleFleet {
	t.Helper()

	require.NoError(t, r.plane.graph.Write(t.Context(),
		tuple(t, authz.TypeDevice, r.id, authz.RelOperator, printingModule)))
	require.NoError(t, r.plane.graph.Write(t.Context(),
		tuple(t, authz.TypeResourceKind, "storage", authz.RelViewer, printingModule)))

	return modulePrinting(t, r.plane.graph, r.plane,
		authz.ActionObserve, authz.ActionOperate, authz.ActionReadStorage)
}

// A module that prints a file the printer already holds, and that the printer
// refuses to start, is told so by a code it can branch on. It is not answered
// with a command id as if a job had begun.
func TestAModulePrintThePrinterRefusesToStartIsPrintRefused(t *testing.T) {
	t.Parallel()

	rig := newStickRig(t)
	fleet := rig.grantPrinting(t)
	held := deviceapi.PrintParams{ID: rig.id, Path: "usb/part.gcode"}

	started, err := fleet.Print(t.Context(), held)
	require.NoError(t, err)
	require.NotEmpty(t, started.CommandID)

	rig.session.refusing("INVALID_STATE")

	_, err = fleet.Print(t.Context(), held)
	require.ErrorIs(t, err, deviceapi.ErrPrintRefused)
	require.ErrorContains(t, err, "INVALID_STATE")
	require.Len(t, rig.session.given(), 2)
}

// A print from a storage of the copy the printer already holds sends nothing
// and starts that copy. A refusal of that start reaches the module the same
// way.
func TestAModulePrintOfAHeldCopyThePrinterRefusesIsPrintRefused(t *testing.T) {
	t.Parallel()

	rig := newStickRig(t)
	fleet := rig.grantPrinting(t)
	rig.sendOwnCopy(t)
	rig.session.refusing("INVALID_STATE")

	_, err := fleet.Print(t.Context(), deviceapi.PrintParams{
		ID: rig.id, Storage: "bench", Source: "part.gcode", Path: "usb/part.gcode",
	})
	require.ErrorIs(t, err, deviceapi.ErrPrintRefused)
	require.Len(t, rig.session.sends(), 1, "the file the printer holds was sent again")
}

// A start the printer took and then failed reaches the module as a failed
// start, apart from a refusal: the printer may have begun, so the module reads
// its state before it starts the print again. Both answers that carry the
// printer's reply say so: a file it holds and a copy it already has.
func TestAModulePrintThePrinterTookAndFailedIsPrintFailed(t *testing.T) {
	t.Parallel()

	for name, params := range map[string]func(rig stickRig) deviceapi.PrintParams{
		"held file": func(rig stickRig) deviceapi.PrintParams {
			return deviceapi.PrintParams{ID: rig.id, Path: "usb/part.gcode"}
		},
		"held copy": func(rig stickRig) deviceapi.PrintParams {
			return deviceapi.PrintParams{
				ID: rig.id, Storage: "bench", Source: "part.gcode", Path: "usb/part.gcode",
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			rig := newStickRig(t)
			fleet := rig.grantPrinting(t)
			rig.sendOwnCopy(t)
			rig.session.failing("MEDIA_ERROR")

			_, err := fleet.Print(t.Context(), params(rig))
			require.ErrorIs(t, err, deviceapi.ErrPrintFailed)
			require.NotErrorIs(t, err, deviceapi.ErrPrintRefused)
			require.ErrorContains(t, err, connectorapi.CommandFailed.Code().String()+": MEDIA_ERROR")
		})
	}
}

// A failed command of another verb is not a failed start, and is left to the
// mapping every other refusal takes.
func TestAFailedCommandOfAnotherVerbIsNoFailedStart(t *testing.T) {
	t.Parallel()

	failed := connectorapi.CommandFailed.New("the printer could not carry it out").
		With(fault.StringParam("verb", connectorapi.VerbFileUpload.String()))

	require.NoError(t, failedStart(failed, "usb/part.gcode"))
}
