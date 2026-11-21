package runner_test

import (
	"errors"
	"fmt"
	"path"
	"sync"
	"testing"
	"time"

	"github.com/prusa3d/prusa-connect/sdk/deviceapi"
	"github.com/prusa3d/prusa-connect/sdk/event"

	"github.com/prusa3d/simple-queue/internal/dbtest"
	"github.com/prusa3d/simple-queue/internal/hostfake"
	"github.com/prusa3d/simple-queue/internal/queue"
	"github.com/prusa3d/simple-queue/internal/runner"
	"github.com/prusa3d/simple-queue/internal/settings"
)

const (
	alice   = "user:alice"
	printer = "printer-1"
)

// clock is a time a test moves by hand.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.now
}

func (c *clock) Add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.now = c.now.Add(d)
}

// bench is one runner over a fake host with one printer, which alice may
// print on and the module may watch. alice queued the files the test names.
type bench struct {
	host   *hostfake.Host
	store  *queue.Store
	runner *runner.Runner
	clock  *clock
}

func newBench(t *testing.T, files ...string) bench {
	t.Helper()

	host := hostfake.New()
	host.AddPrinter(printer, hostfake.Printer{
		Name: "MK4", State: "IDLE", Connected: true, CanBeReady: true,
		Storages: []deviceapi.Storage{{Name: "flash", ReadOnly: true}, {Name: "usb"}},
	})
	host.Grant(hostfake.Self, "observe", "device", printer)
	host.Grant(alice, "observe", "device", printer)
	host.Grant(alice, "operate", "device", printer)
	host.Grant(alice, "read_storage", "resource_kind", "storage")

	ticking := &clock{now: time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)}
	db, grant := dbtest.SQLite(t)
	store := queue.NewStore(db, grant.Engine)

	for _, file := range files {
		if _, err := store.Update(t.Context(), printer, func(q *queue.Queue) error {
			return q.Add(queue.Entry{
				ID: file, Storage: "local", Path: "parts/" + file + ".bgcode", Name: file + ".bgcode",
				AddedBy: alice, AddedAt: ticking.Now(),
			}, 10)
		}); err != nil {
			t.Fatal(err)
		}
	}

	return bench{
		host:  host,
		store: store,
		clock: ticking,
		runner: runner.New(store, host.Fleet(), host, settings.Defaults, ticking.Now,
			func(string, ...any) {}),
	}
}

// allowAlone lets the module start prints on the printer by itself.
func (b bench) allowAlone() { b.host.Grant(hostfake.Self, "operate", "device", printer) }

func (b bench) queue(t *testing.T) *queue.Queue {
	t.Helper()

	held, err := b.store.Read(t.Context(), printer)
	if err != nil {
		t.Fatal(err)
	}

	return &held
}

func TestTheQueueStartsByItselfOnlyOnAReadyPrinter(t *testing.T) {
	t.Parallel()

	b := newBench(t, "a", "b")
	b.allowAlone()

	for _, state := range []string{"IDLE", "FINISHED", "STOPPED", "PRINTING", "ATTENTION"} {
		b.host.SetState(printer, state)
		b.runner.Sweep(t.Context())

		if sent := b.host.Sent(); len(sent) != 0 {
			t.Fatalf("a %s printer was sent %+v", state, sent)
		}
	}

	b.host.SetState(printer, "READY")
	b.runner.Sweep(t.Context())

	sent := b.host.Sent()
	if len(sent) != 1 || sent[0].Principal != "" || sent[0].Path != "usb/a.bgcode" ||
		sent[0].Storage != "local" || sent[0].Source != "parts/a.bgcode" {
		t.Fatalf("a ready printer was sent %+v", sent)
	}

	active := b.queue(t).Active()
	if active == nil || active.ID != "a" || active.State != queue.Sending || active.TransferID == "" {
		t.Fatalf("after the send the active entry reads %+v", active)
	}

	b.runner.Sweep(t.Context())

	if len(b.host.Sent()) != 1 {
		t.Fatal("a second sweep sent another file while one was on its way")
	}
}

// The queue reads whether a printer takes a print and is ready from what the
// server says of its state, so a printer with words of its own is served.
func TestAPrinterWithItsOwnWordsIsReadByWhatItAccepts(t *testing.T) {
	t.Parallel()

	b := newBench(t, "a")
	b.allowAlone()

	const start = "core.command.start_print"

	b.host.Says(printer, "WORKING", false)

	if _, err := b.runner.Start(t.Context(), printer, alice, ""); !errors.Is(err, runner.ErrNotFree) {
		t.Fatalf("a start on a printer that takes no print answered %v", err)
	}

	if next := b.queue(t).Next(); next == nil || next.Note != queue.NotePrinterNotFree || len(b.host.Sent()) != 0 {
		t.Fatalf("a printer that takes no print was sent %+v, the entry reads %+v", b.host.Sent(), next)
	}

	b.host.Says(printer, "STANDBY", false, start)
	b.runner.Sweep(t.Context())

	if next := b.queue(t).Next(); next == nil || next.Note != "" || len(b.host.Sent()) != 0 {
		t.Fatalf("a printer not ready was sent %+v, the entry reads %+v", b.host.Sent(), next)
	}

	b.host.Says(printer, "ARMED", true, start)
	b.runner.Sweep(t.Context())

	if sent := b.host.Sent(); len(sent) != 1 {
		t.Fatalf("a ready printer was sent %+v", sent)
	}
}

// A printer whose connector says nothing of what its states accept is ready
// when the host says so, whatever its state is called, and the queue reads no
// state by its name.
func TestAPrinterWhoseConnectorDeclaresNothingIsReadyWhenTheHostSaysSo(t *testing.T) {
	t.Parallel()

	b := newBench(t, "a")
	b.allowAlone()

	b.host.Silent(printer, "READY", false)
	b.runner.Sweep(t.Context())

	if sent := b.host.Sent(); len(sent) != 0 {
		t.Fatalf("a printer the host calls not ready was sent %+v", sent)
	}

	b.host.Silent(printer, "ARMED", true)
	b.runner.Sweep(t.Context())

	if sent := b.host.Sent(); len(sent) != 1 {
		t.Fatalf("a printer the host calls ready was sent %+v", sent)
	}
}

// A person may start a file on such a printer in any state, and the printer's
// own answer decides.
func TestAPersonStartsOnAPrinterWhoseConnectorDeclaresNothing(t *testing.T) {
	t.Parallel()

	b := newBench(t, "a")
	b.host.Silent(printer, "WORKING", false)

	if _, err := b.runner.Start(t.Context(), printer, alice, ""); err != nil {
		t.Fatal(err)
	}

	if sent := b.host.Sent(); len(sent) != 1 {
		t.Fatalf("the start sent %+v", sent)
	}
}

// A printer marked ready says so with an event, and the queue starts its next
// file then, not on the next sweep a minute later.
func TestAPrinterThatBecomesReadyStartsItsNextFileAtOnce(t *testing.T) {
	t.Parallel()

	b := newBench(t, "a")
	b.allowAlone()
	b.host.SetState(printer, "FINISHED")

	go func() { _ = b.runner.Run(t.Context(), b.host) }()

	// The sweep the runner makes when it starts reads the printer not ready.
	eventually(t, "the first sweep reads the printer", func() bool { return b.host.Looks() > 0 })

	if sent := b.host.Sent(); len(sent) != 0 {
		t.Fatalf("a finished printer was sent %+v", sent)
	}

	b.host.SetState(printer, "READY")
	b.host.Emit(printer, event.TypeDeviceStateChanged, event.StateChanged{
		DeviceID: printer, From: "FINISHED", To: "READY",
	})

	eventually(t, "the next file is sent", func() bool { return len(b.host.Sent()) == 1 })
}

func TestWithoutItsOwnGrantTheQueueWaitsForAPerson(t *testing.T) {
	t.Parallel()

	b := newBench(t, "a")
	b.host.SetState(printer, "READY")

	b.runner.Sweep(t.Context())

	if len(b.host.Sent()) != 0 {
		t.Fatal("the module started a print it was not allowed to")
	}

	if b.runner.MayStartAlone(t.Context(), printer) {
		t.Fatal("the module says it may start alone without the grant")
	}

	if _, err := b.runner.Start(t.Context(), printer, alice, ""); err != nil {
		t.Fatal(err)
	}

	if sent := b.host.Sent(); len(sent) != 1 || sent[0].Principal != alice {
		t.Fatalf("Print next sent %+v", sent)
	}
}

func TestAnEntryWhoseOwnerLostThePrinterIsHeld(t *testing.T) {
	t.Parallel()

	b := newBench(t, "a")
	b.allowAlone()
	b.host.SetState(printer, "READY")
	b.host.Revoke(alice, "operate", "device", printer)

	b.runner.Sweep(t.Context())

	if len(b.host.Sent()) != 0 {
		t.Fatal("the file of a person who may no longer print was sent")
	}

	if next := b.queue(t).Next(); next.Note != queue.NoteOwnerMayNot {
		t.Fatalf("the held entry reads %+v", next)
	}

	b.host.Grant(alice, "operate", "device", printer)
	b.runner.Sweep(t.Context())

	if len(b.host.Sent()) != 1 {
		t.Fatal("the entry did not start once its owner had the printer again")
	}
}

// A server with an OpenID Connect login or in open mode keeps no list of
// people. It answers about a person the module served since it started from
// that person's roles, so their file starts by itself.
func TestAServerWithoutAListOfPeopleAnswersFromTheRoles(t *testing.T) {
	t.Parallel()

	b := newBench(t, "a")
	b.allowAlone()
	b.host.SetState(printer, "READY")
	b.host.NoPeople(alice)

	b.runner.Sweep(t.Context())

	if len(b.host.Sent()) != 1 {
		t.Fatal("the file of a person the server answers about did not start")
	}
}

// About a person the module has not served since it started, such as after a
// restart, that server does not answer. The file waits with a note, the queue
// does not ask again, and a person starts it.
func TestAServerThatCannotCheckTheOwnerWaitsForAPerson(t *testing.T) {
	t.Parallel()

	b := newBench(t, "a")
	b.allowAlone()
	b.host.SetState(printer, "READY")
	b.host.NoPeople()

	b.runner.Sweep(t.Context())
	b.runner.Sweep(t.Context())

	if len(b.host.Sent()) != 0 {
		t.Fatal("the queue started a file whose owner the server cannot check")
	}

	if next := b.queue(t).Next(); next == nil || next.Note != queue.NoteOwnerUnchecked {
		t.Fatalf("the held entry reads %+v", next)
	}

	if _, err := b.runner.Start(t.Context(), printer, alice, ""); err != nil {
		t.Fatal(err)
	}

	if len(b.host.Sent()) != 1 {
		t.Fatal("Print did not start the held file")
	}
}

// A printer that is printing, paused or away cannot take the next file. The
// file waits with the printer's state on it, and the note goes once the
// printer is free.
func TestAStartOnAPrinterThatIsNotFreeHoldsTheFile(t *testing.T) {
	t.Parallel()

	b := newBench(t, "a")
	b.host.SetState(printer, "PRINTING")

	if _, err := b.runner.Start(t.Context(), printer, alice, ""); !errors.Is(err, runner.ErrNotFree) {
		t.Fatalf("a start on a printing printer answered %v", err)
	}

	if len(b.host.Sent()) != 0 {
		t.Fatal("the file was sent to a printer that is printing")
	}

	next := b.queue(t).Next()
	if next == nil || next.Note != queue.NotePrinterNotFree || next.Detail != "PRINTING" {
		t.Fatalf("the held entry reads %+v", next)
	}

	b.host.SetState(printer, "IDLE")
	b.runner.Sweep(t.Context())

	if next = b.queue(t).Next(); next == nil || next.Note != "" {
		t.Fatalf("the note stayed once the printer was free: %+v", next)
	}
}

// A print error that does not say nothing was sent, such as a session that
// closed mid-call, leaves the entry sent: the server may still print it, and
// giving it back would print it twice.
func TestAnUnclearSendErrorLeavesTheEntrySent(t *testing.T) {
	t.Parallel()

	b := newBench(t, "a")
	b.host.SetRefuse(printer, errors.New("the session closed"))

	if _, err := b.runner.Start(t.Context(), printer, alice, ""); !errors.Is(err, runner.ErrSendFailed) {
		t.Fatalf("an unclear send answered %v", err)
	}

	if active := b.queue(t).Active(); active == nil || active.ID != "a" || active.State != queue.Sending {
		t.Fatalf("after an unclear send the entry reads %+v", active)
	}
}

// A failed send is not tried again by the queue itself. A person's start
// sends it under its own name again, and the server picks another only when
// the printer holds a file there.
func TestAFailedSendWaitsForAPerson(t *testing.T) {
	t.Parallel()

	b := newBench(t, "a")
	b.allowAlone()
	b.host.SetState(printer, "READY")
	b.host.SetRefuse(printer, fmt.Errorf("%w: the printer is busy", deviceapi.ErrPrintRefused))

	b.runner.Sweep(t.Context())

	next := b.queue(t).Next()
	if next == nil || next.Note != queue.NoteSendFailed || next.Detail == "" {
		t.Fatalf("after a refused send the entry reads %+v", next)
	}

	b.host.SetRefuse(printer, nil)
	b.runner.Sweep(t.Context())

	if len(b.host.Sent()) != 0 {
		t.Fatal("the queue sent a failed file again by itself")
	}

	if _, err := b.runner.Start(t.Context(), printer, alice, ""); err != nil {
		t.Fatal(err)
	}

	sent := b.host.Sent()
	if len(sent) != 1 || sent[0].Path != "usb/a.bgcode" {
		t.Fatalf("the second send was %+v", sent)
	}
}

// The start command of somebody else, for a file of the same name, may end as
// refused while the queue's own start is on its way. The queue knows its own
// command by its id, so the other one's end does not give the entry back, and
// the print that follows is the queue's.
func TestTheEndOfAnotherStartOfTheSameFileIsNotTheQueues(t *testing.T) {
	t.Parallel()

	b := newBench(t)
	if _, err := b.store.Update(t.Context(), printer, func(q *queue.Queue) error {
		return q.Add(queue.Entry{
			ID: "x", Path: "usb/plate.bgcode", Name: "plate.bgcode", AddedBy: alice, AddedAt: b.clock.Now(),
		}, 10)
	}); err != nil {
		t.Fatal(err)
	}

	b.host.PutPrinterFile(printer, "usb/plate.bgcode")

	go func() { _ = b.runner.Run(t.Context(), b.host) }()

	// A person started the same file from the Files tab a moment earlier.
	b.host.Emit(printer, event.TypeDeviceCommandRequested, event.CommandRequested{
		CommandID: "foreign", Verb: runner.VerbStartPrint, Args: map[string]string{"path": "usb/plate.bgcode"},
	})

	held, err := b.runner.Start(t.Context(), printer, alice, "")
	if err != nil {
		t.Fatal(err)
	}

	own := held.Active().CommandID
	if own == "" || own == "foreign" {
		t.Fatalf("the queue's start has the command id %q", own)
	}

	b.host.Emit(printer, event.TypeDeviceCommandSettled, event.CommandSettled{
		CommandID: "foreign", Verb: runner.VerbStartPrint, Status: "refused", MachineReason: "busy",
	})
	b.host.Emit(printer, event.TypeDeviceJobStarted, event.JobStarted{
		JobID: "j1", Path: "/usb/plate.bgcode", Started: b.clock.Now(),
	})
	b.host.Emit(printer, event.TypeDeviceJobSettled, event.JobSettled{
		JobID: "j1", Path: "/usb/plate.bgcode", Outcome: event.JobFinished, Ended: b.clock.Now(),
	})

	eventually(t, "the print is in the history", func() bool { return len(b.queue(t).History) == 1 })

	if q := b.queue(t); len(q.Entries) != 0 || q.History[0].Outcome != queue.Finished {
		t.Fatalf("after the print the queue reads %+v", q)
	}

	// The queue's own start that the printer refuses still gives the entry back.
	if _, err = b.store.Update(t.Context(), printer, func(q *queue.Queue) error {
		return q.Add(queue.Entry{ID: "y", Path: "usb/plate.bgcode", Name: "plate.bgcode", AddedBy: alice}, 10)
	}); err != nil {
		t.Fatal(err)
	}

	if held, err = b.runner.Start(t.Context(), printer, alice, ""); err != nil {
		t.Fatal(err)
	}

	b.host.Emit(printer, event.TypeDeviceCommandSettled, event.CommandSettled{
		CommandID:     held.Active().CommandID,
		Verb:          runner.VerbStartPrint,
		Status:        "refused",
		MachineReason: "no filament",
	})
	eventually(t, "the refused entry is given back", func() bool {
		next := b.queue(t).Next()

		return next != nil && next.Note == queue.NoteSendFailed && next.Detail == "no filament"
	})
}

// A file dropped on the printer's page is queued while the server still
// copies it there. The queue hears how the copy ended: a copy that landed is
// a file on the printer like any other, and one that failed is said on the
// entry, which then waits for a person and is not asked about every sweep.
func TestAQueuedCopyIsFollowedToItsEnd(t *testing.T) {
	t.Parallel()

	for _, landed := range []bool{true, false} {
		t.Run(fmt.Sprintf("landed=%t", landed), func(t *testing.T) {
			t.Parallel()

			b := newBench(t)
			b.allowAlone()

			var (
				mu    sync.Mutex
				lines []string
			)

			b.runner = runner.New(b.store, b.host.Fleet(), b.host, settings.Defaults, b.clock.Now,
				func(format string, args ...any) {
					mu.Lock()
					defer mu.Unlock()

					lines = append(lines, fmt.Sprintf(format, args...))
				})

			go func() { _ = b.runner.Run(t.Context(), b.host) }()

			// The copy starts, and the page adds the entry a moment later.
			b.host.Emit(printer, event.TypeDeviceTransferStarted, event.TransferStarted{
				TransferID: "t-1", Storage: "local", Source: "tmp/plate.bgcode", Destination: "usb/plate.bgcode",
			})

			if _, err := b.store.Update(t.Context(), printer, func(q *queue.Queue) error {
				return q.Add(queue.Entry{
					ID: "coming", Path: "usb/plate.bgcode", Name: "plate.bgcode", AddedBy: alice, Arriving: true,
				}, 10)
			}); err != nil {
				t.Fatal(err)
			}

			settled := event.TransferSettled{TransferID: "t-1", State: event.TransferDone}
			if landed {
				b.host.PutPrinterFile(printer, "usb/plate.bgcode")
			} else {
				settled.State, settled.Reason = event.TransferFailed, "the printer went away"
			}

			b.host.Emit(printer, event.TypeDeviceTransferSettled, settled)

			eventually(t, "the entry knows how its copy ended", func() bool {
				next := b.queue(t).Next()
				if landed {
					return next != nil && !next.Arriving
				}

				return next != nil && next.Note == queue.NoteTransferFailed && next.Detail == "the printer went away"
			})

			b.host.SetState(printer, "READY")

			for range 3 {
				b.runner.Sweep(t.Context())
			}

			mu.Lock()
			defer mu.Unlock()

			if sent := len(b.host.Sent()); (landed && sent != 1) || (!landed && sent != 0) {
				t.Fatalf("landed=%t and the queue sent %d files", landed, sent)
			}

			if len(lines) != 0 {
				t.Fatalf("the sweep logged %q", lines)
			}
		})
	}
}

// The module may have missed how a copy ended, after a restart for one. The
// file is then looked for on each start, quietly, and an entry whose file is
// still not there after the send timeout stops being asked about.
func TestACopyNobodyHeardTheEndOfIsGivenUpOn(t *testing.T) {
	t.Parallel()

	b := newBench(t)
	b.allowAlone()
	b.host.SetState(printer, "READY")

	if _, err := b.store.Update(t.Context(), printer, func(q *queue.Queue) error {
		return q.Add(queue.Entry{
			ID: "coming", Path: "usb/plate.bgcode", Name: "plate.bgcode", AddedBy: alice,
			AddedAt: b.clock.Now(), Arriving: true,
		}, 10)
	}); err != nil {
		t.Fatal(err)
	}

	b.runner.Sweep(t.Context())

	if next := b.queue(t).Next(); next == nil || next.Note != queue.NoteNotArrived {
		t.Fatalf("a file that is not there yet reads %+v", next)
	}

	b.clock.Add(settings.Defaults().SendTimeout + time.Minute)
	b.runner.Sweep(t.Context())

	if next := b.queue(t).Next(); next == nil || next.Note != queue.NoteTransferFailed {
		t.Fatalf("after the send timeout the entry reads %+v", next)
	}
}

// A printer that is taking another file is not a failed send: the entry waits
// with no note, the queue tries again by itself, and the file keeps its name.
func TestABusyPrinterIsTriedAgainByItself(t *testing.T) {
	t.Parallel()

	b := newBench(t, "a")
	b.allowAlone()
	b.host.SetState(printer, "READY")
	b.host.SetRefuse(printer, fmt.Errorf("%w: another transfer runs", deviceapi.ErrBusy))

	b.runner.Sweep(t.Context())

	held := b.queue(t)
	if next := held.Next(); held.Active() != nil || next == nil || next.Note != "" || next.Attempts != 0 {
		t.Fatalf("after a busy printer the entry reads %+v with %+v active", next, held.Active())
	}

	if _, err := b.runner.Start(t.Context(), printer, alice, ""); !errors.Is(err, queue.ErrBusy) {
		t.Fatalf("a person's start on a busy printer answered %v", err)
	}

	b.host.SetRefuse(printer, nil)
	b.runner.Sweep(t.Context())

	sent := b.host.Sent()
	if len(sent) != 1 || sent[0].Path != "usb/a.bgcode" {
		t.Fatalf("once the printer was free the queue sent %+v", sent)
	}
}

// A name the printer already holds is not a failure a person has to answer:
// the start asks the server to keep both, and the entry follows the print
// under the name the server answered.
func TestANameThePrinterHoldsIsSentBesideIt(t *testing.T) {
	t.Parallel()

	b := newBench(t, "a")
	b.host.SetState(printer, "READY")
	b.host.SetHolds(printer, "usb/a.bgcode")

	held, err := b.runner.Start(t.Context(), printer, alice, "")
	if err != nil {
		t.Fatal(err)
	}

	sent := b.host.Sent()
	if len(sent) != 1 || sent[0].Path != "usb/a[1].bgcode" {
		t.Fatalf("the file was sent as %+v", sent)
	}

	if active := held.Active(); active == nil || active.Note != "" || active.Landing != "usb/a[1].bgcode" {
		t.Fatalf("after the send the entry reads %+v", active)
	}
}

// A file added while it is copied to the printer is not started before it is
// there: the entry waits with a note, and starts by itself once it landed.
func TestAFileOnItsWayStartsOnlyOnceItIsOnThePrinter(t *testing.T) {
	t.Parallel()

	b := newBench(t)
	b.allowAlone()

	if _, err := b.store.Update(t.Context(), printer, func(q *queue.Queue) error {
		return q.Add(queue.Entry{
			ID: "coming", Path: "usb/plate.bgcode", Name: "plate.bgcode",
			AddedBy: alice, AddedAt: b.clock.Now(), Arriving: true,
		}, 10)
	}); err != nil {
		t.Fatal(err)
	}

	b.host.SetState(printer, "READY")
	b.runner.Sweep(t.Context())

	if len(b.host.Sent()) != 0 {
		t.Fatal("a file that is not on the printer was started")
	}

	if next := b.queue(t).Next(); next == nil || next.Note != queue.NoteNotArrived || !next.Arriving {
		t.Fatalf("the entry of a file on its way reads %+v", next)
	}

	b.host.PutPrinterFile(printer, "usb/plate.bgcode")
	b.runner.Sweep(t.Context())

	sent := b.host.Sent()
	if len(sent) != 1 || sent[0].Storage != "" || sent[0].Path != "usb/plate.bgcode" {
		t.Fatalf("the landed file was started as %+v", sent)
	}

	if active := b.queue(t).Active(); active == nil || active.Arriving || active.Note != "" {
		t.Fatalf("the started entry reads %+v", active)
	}
}

// A printer lists a file it is still taking as read-only at its full length.
// The queue starts it only once the copy has ended.
func TestAFileStillBeingCopiedIsNotStarted(t *testing.T) {
	t.Parallel()

	b := newBench(t)
	b.allowAlone()

	if _, err := b.store.Update(t.Context(), printer, func(q *queue.Queue) error {
		return q.Add(queue.Entry{
			ID: "coming", Path: "usb/plate.bgcode", Name: "plate.bgcode",
			AddedBy: alice, AddedAt: b.clock.Now(), Arriving: true,
		}, 10)
	}); err != nil {
		t.Fatal(err)
	}

	b.host.PutArrivingFile(printer, "usb/plate.bgcode")
	b.host.SetState(printer, "READY")
	b.runner.Sweep(t.Context())

	if sent := b.host.Sent(); len(sent) != 0 {
		t.Fatalf("a file still being copied was started: %+v", sent)
	}

	if next := b.queue(t).Next(); next == nil || next.Note != queue.NoteNotArrived || !next.Arriving {
		t.Fatalf("the entry of a file still being copied reads %+v", next)
	}

	b.host.PutPrinterFile(printer, "usb/plate.bgcode")
	b.runner.Sweep(t.Context())

	if sent := b.host.Sent(); len(sent) != 1 || sent[0].Path != "usb/plate.bgcode" {
		t.Fatalf("the landed file was started as %+v", sent)
	}
}

func TestAPrinterWithNoWritableStorageHoldsTheEntry(t *testing.T) {
	t.Parallel()

	b := newBench(t, "a")
	b.host.AddPrinter(printer, hostfake.Printer{State: "READY", Connected: true})

	if _, err := b.runner.Start(t.Context(), printer, alice, ""); !errors.Is(err, runner.ErrNoStorage) {
		t.Fatalf("a printer without storage answered %v", err)
	}

	if next := b.queue(t).Next(); next.Note != queue.NoteNoStorage {
		t.Fatalf("the entry reads %+v", next)
	}
}

func TestEventsFollowThePrint(t *testing.T) {
	t.Parallel()

	b := newBench(t, "a", "b")

	go func() { _ = b.runner.Run(t.Context(), b.host) }()

	if _, err := b.runner.Start(t.Context(), printer, alice, ""); err != nil {
		t.Fatal(err)
	}

	b.host.Emit(printer, event.TypeDeviceJobStarted, event.JobStarted{
		JobID: "job-1", Path: "/usb/A.BGCODE", Started: b.clock.Now(),
	})
	eventually(t, "the entry is printing", func() bool {
		active := b.queue(t).Active()

		return active != nil && active.State == queue.Printing && active.JobID == "job-1"
	})

	b.host.Emit(printer, event.TypeDeviceJobSettled, event.JobSettled{
		JobID: "job-1", Outcome: event.JobFinished, Ended: b.clock.Now(),
	})
	eventually(t, "the print is in the history", func() bool {
		held := b.queue(t)

		return held.Active() == nil && len(held.History) == 1 && held.History[0].Outcome == queue.Finished
	})
}

// The end of an earlier print of the same file can arrive after the next
// send. It began before that send, so it is not the next file's print.
func TestALateEndOfAnEarlierPrintDoesNotEndTheNextFile(t *testing.T) {
	t.Parallel()

	b := newBench(t, "a")

	if _, err := b.store.Update(t.Context(), printer, func(q *queue.Queue) error {
		return q.Add(queue.Entry{
			ID: "again", Storage: "local", Path: "parts/a.bgcode", Name: "a.bgcode",
			AddedBy: alice, AddedAt: b.clock.Now(),
		}, 10)
	}); err != nil {
		t.Fatal(err)
	}

	go func() { _ = b.runner.Run(t.Context(), b.host) }()

	if _, err := b.runner.Start(t.Context(), printer, alice, ""); err != nil {
		t.Fatal(err)
	}

	first := b.clock.Now()
	b.host.Emit(printer, event.TypeDeviceJobStarted, event.JobStarted{
		JobID: "job-1", Path: "/usb/a.bgcode", Started: first,
	})
	eventually(t, "the first file is printing", func() bool {
		active := b.queue(t).Active()

		return active != nil && active.ID == "a" && active.JobID == "job-1"
	})

	// The first print ends an hour later, as the sweep would record it, and
	// a person starts the next file of the same name.
	b.clock.Add(time.Hour)

	if _, err := b.store.Update(t.Context(), printer, func(q *queue.Queue) error {
		if !q.Settled("job-1", "/usb/a.bgcode", queue.Finished, b.clock.Now(), 10) {
			return queue.ErrUnchanged
		}

		return nil
	}); err != nil {
		t.Fatal(err)
	}

	b.clock.Add(time.Minute)

	if _, err := b.runner.Start(t.Context(), printer, alice, ""); err != nil {
		t.Fatal(err)
	}

	// The first print's own end arrives late, then the next print starts.
	b.host.Emit(printer, event.TypeDeviceJobSettled, event.JobSettled{
		JobID: "job-1", Path: "/usb/a.bgcode", Outcome: event.JobFinished, Started: first, Ended: b.clock.Now(),
	})
	b.host.Emit(printer, event.TypeDeviceJobStarted, event.JobStarted{
		JobID: "job-2", Path: "/usb/a.bgcode", Started: b.clock.Now(),
	})
	eventually(t, "the next file is printing as job-2", func() bool {
		active := b.queue(t).Active()

		return active != nil && active.ID == "again" && active.JobID == "job-2"
	})

	if held := b.queue(t); len(held.History) != 1 {
		t.Fatalf("the late end moved the next file to the history: %+v", held.History)
	}
}

func TestAFileOnThePrinterStartsAloneWithoutReadStorage(t *testing.T) {
	t.Parallel()

	b := newBench(t)
	b.allowAlone()
	b.host.Revoke(alice, "read_storage", "resource_kind", "storage")

	if _, err := b.store.Update(t.Context(), printer, func(q *queue.Queue) error {
		return q.Add(queue.Entry{
			ID: "held", Path: "usb/plate.bgcode", Name: "plate.bgcode", AddedBy: alice, AddedAt: b.clock.Now(),
		}, 10)
	}); err != nil {
		t.Fatal(err)
	}

	b.host.SetState(printer, "READY")
	b.runner.Sweep(t.Context())

	sent := b.host.Sent()
	if len(sent) != 1 || sent[0].Storage != "" || sent[0].Source != "" || sent[0].Path != "usb/plate.bgcode" {
		t.Fatalf("the file on the printer was started as %+v", sent)
	}

	go func() { _ = b.runner.Run(t.Context(), b.host) }()

	b.host.Emit(printer, event.TypeDeviceJobStarted, event.JobStarted{
		JobID: "job-1", Path: "/usb/plate.bgcode", Started: b.clock.Now(),
	})
	eventually(t, "the entry is printing", func() bool {
		active := b.queue(t).Active()

		return active != nil && active.State == queue.Printing && active.JobID == "job-1"
	})
}

func TestATransferThatEndedMovesTheEntry(t *testing.T) {
	t.Parallel()

	b := newBench(t, "a", "b")

	go func() { _ = b.runner.Run(t.Context(), b.host) }()

	if _, err := b.runner.Start(t.Context(), printer, alice, ""); err != nil {
		t.Fatal(err)
	}

	transfer := b.queue(t).Active().TransferID
	b.host.Emit(printer, event.TypeDeviceTransferSettled, event.TransferSettled{
		TransferID: transfer, State: event.TransferFailed, Reason: "the drive is full",
	})
	eventually(t, "the entry is given back", func() bool {
		next := b.queue(t).Next()

		return next != nil && next.ID == "a" && next.Note == queue.NoteTransferFailed &&
			next.Detail == "the drive is full"
	})

	if _, err := b.runner.Start(t.Context(), printer, alice, ""); err != nil {
		t.Fatal(err)
	}

	transfer = b.queue(t).Active().TransferID
	b.host.Emit(printer, event.TypeDeviceTransferSettled, event.TransferSettled{
		TransferID: transfer, State: event.TransferCancelled,
	})
	eventually(t, "the stopped entry is back at the head and waits for a person", func() bool {
		held := b.queue(t)
		next := held.Next()

		return held.Active() == nil && len(held.History) == 0 &&
			next != nil && next.ID == "a" && next.Note == queue.NoteTakenBack
	})

	b.allowAlone()
	b.host.SetState(printer, "READY")
	b.runner.Sweep(t.Context())

	if held := b.queue(t); held.Active() != nil {
		t.Fatal("the queue started a file a person had stopped")
	}
}

// The sweep checks the owner of one entry and then starts. A person may move
// another entry to the head in between, and that one is not started.
func TestAStartForOneEntryDoesNotStartAnother(t *testing.T) {
	t.Parallel()

	b := newBench(t, "a", "b")

	held, err := b.runner.Start(t.Context(), printer, "", "b")
	if !errors.Is(err, queue.ErrBusy) || held.Active() != nil {
		t.Fatalf("a start for b answered %v with %+v", err, held.Active())
	}

	if len(b.host.Sent()) != 0 {
		t.Fatal("a start for b sent a")
	}
}

// A transfer can fail before the queue has written its id on the entry. The
// failure is matched by where the transfer writes, and the late id finds the
// entry already given back.
func TestATransferThatFailedBeforeItsIDWasKeptMovesTheEntry(t *testing.T) {
	t.Parallel()

	b := newBench(t, "a")

	go func() { _ = b.runner.Run(t.Context(), b.host) }()

	if _, err := b.store.Update(t.Context(), printer, func(q *queue.Queue) error {
		return q.Begin("a", alice, "usb/a.bgcode", b.clock.Now())
	}); err != nil {
		t.Fatal(err)
	}

	b.host.Emit(printer, event.TypeDeviceTransferStarted, event.TransferStarted{
		TransferID: "t-9", Storage: "local", Source: "parts/a.bgcode", Destination: "usb/a.bgcode",
	})
	b.host.Emit(printer, event.TypeDeviceTransferSettled, event.TransferSettled{
		TransferID: "t-9", State: event.TransferFailed, Reason: "the drive is full",
	})
	eventually(t, "the entry is given back", func() bool {
		next := b.queue(t).Next()

		return next != nil && next.ID == "a" && next.Note == queue.NoteTransferFailed
	})

	if _, err := b.store.Update(t.Context(), printer, func(q *queue.Queue) error {
		return q.Sent("a", "t-9", "c-9", "")
	}); !errors.Is(err, queue.ErrNoEntry) {
		t.Fatalf("the late id found an entry to write on: %v", err)
	}
}

func TestARefusedStartGivesTheEntryBack(t *testing.T) {
	t.Parallel()

	b := newBench(t, "a")

	go func() { _ = b.runner.Run(t.Context(), b.host) }()

	if _, err := b.runner.Start(t.Context(), printer, alice, ""); err != nil {
		t.Fatal(err)
	}

	landing := b.queue(t).Active().Landing
	start := func(id string, settled event.CommandSettled) {
		b.host.Emit(printer, event.TypeDeviceCommandRequested, event.CommandRequested{
			CommandID: id, Verb: runner.VerbStartPrint, Args: map[string]string{"path": landing},
		})
		settled.CommandID, settled.Verb = id, runner.VerbStartPrint
		b.host.Emit(printer, event.TypeDeviceCommandSettled, settled)
	}

	// A start whose end the host never saw may be printing, so it waits.
	start("c-unknown", event.CommandSettled{Status: "abandoned", OutcomeUnknown: true})
	// The end of a start the queue never heard asked for is not its own.
	b.host.Emit(printer, event.TypeDeviceCommandSettled, event.CommandSettled{
		CommandID: "c-other", Verb: runner.VerbStartPrint, Status: "refused",
	})
	start("c-refused", event.CommandSettled{Status: "refused", MachineReason: "the bed is not clear"})

	eventually(t, "the entry is given back with the printer's reason", func() bool {
		next := b.queue(t).Next()

		return b.queue(t).Active() == nil && next != nil && next.ID == "a" &&
			next.Note == queue.NoteSendFailed && next.Detail == "the bed is not clear"
	})
}

// A printer shows the file on its screen as soon as it has landed, and a
// person standing at it may start the print there before the server's own
// start command arrives. That print is the queue's file, so the entry follows
// it, and the server's start, which the printing machine then refuses, does
// not put the entry back to be printed a second time.
func TestAPrintStartedAtThePrinterIsTheQueuesFile(t *testing.T) {
	t.Parallel()

	b := newBench(t, "a", "b")

	go func() { _ = b.runner.Run(t.Context(), b.host) }()

	if _, err := b.runner.Start(t.Context(), printer, alice, ""); err != nil {
		t.Fatal(err)
	}

	landing := b.queue(t).Active().Landing

	// The person starts it at the printer: a job with no command of the
	// queue's. The printer's screen opens a file by its FAT short name, so
	// that is the path, and the name the queue wrote is the display name.
	b.host.Emit(printer, event.TypeDeviceJobStarted, event.JobStarted{
		JobID: "job-1", Path: "/usb/A~1.BGC", DisplayName: path.Base(landing), Started: b.clock.Now(),
	})
	eventually(t, "the entry is printing", func() bool {
		active := b.queue(t).Active()

		return active != nil && active.ID == "a" && active.State == queue.Printing
	})

	// The file has landed, so the server sends its start, and the printer,
	// already printing, refuses it.
	b.host.Emit(printer, event.TypeDeviceCommandRequested, event.CommandRequested{
		CommandID: "c-late", Verb: runner.VerbStartPrint, Args: map[string]string{"path": landing},
	})
	b.host.Emit(printer, event.TypeDeviceCommandSettled, event.CommandSettled{
		CommandID: "c-late", Verb: runner.VerbStartPrint, Status: "refused", MachineReason: "the printer is printing",
	})
	b.host.Emit(printer, event.TypeDeviceJobSettled, event.JobSettled{
		JobID: "job-1", Outcome: event.JobFinished, Ended: b.clock.Now(),
	})

	eventually(t, "the print is in the history", func() bool {
		return len(b.queue(t).History) == 1
	})

	held := b.queue(t)
	if held.Active() != nil || held.History[0].ID != "a" || held.History[0].Outcome != queue.Finished {
		t.Fatalf("the print ended as %+v with %+v active", held.History[0], held.Active())
	}

	if next := held.Next(); next == nil || next.ID != "b" || held.WaitingCount() != 1 {
		t.Fatalf(
			"after the print %+v is next of %d waiting; the printed file must not wait again",
			next,
			held.WaitingCount(),
		)
	}
}

func TestTheSweepCatchesAPrintWhoseEventsWereMissed(t *testing.T) {
	t.Parallel()

	b := newBench(t, "a")

	if _, err := b.runner.Start(t.Context(), printer, alice, ""); err != nil {
		t.Fatal(err)
	}

	b.clock.Add(time.Minute)
	b.host.SetJobs(printer, deviceapi.Jobs{Current: &deviceapi.Job{
		ID: "job-1", Path: "/usb/a.bgcode", State: "PRINTING", Started: b.clock.Now(),
	}})
	b.runner.Sweep(t.Context())

	if active := b.queue(t).Active(); active == nil || active.State != queue.Printing || active.JobID != "job-1" {
		t.Fatalf("after the sweep the entry reads %+v", active)
	}

	ended := b.clock.Now().Add(time.Hour)
	b.host.SetJobs(printer, deviceapi.Jobs{Last: &deviceapi.Job{
		ID: "job-1", Path: "/usb/a.bgcode", State: "STOPPED", Outcome: "cancelled", Ended: &ended,
	}})
	b.runner.Sweep(t.Context())

	held := b.queue(t)
	if held.Active() != nil || len(held.History) != 1 || held.History[0].Outcome != queue.Cancelled {
		t.Fatalf("after the print ended the queue reads %+v", held)
	}
}

// The sweep reads the same two names the events carry. A print a person
// started on the printer's screen has the FAT short name as its path, so the
// sweep knows it by its display name when the started event was missed.
func TestTheSweepKnowsAPrintStartedAtThePrinterByItsDisplayName(t *testing.T) {
	t.Parallel()

	b := newBench(t, "a")

	if _, err := b.runner.Start(t.Context(), printer, alice, ""); err != nil {
		t.Fatal(err)
	}

	b.clock.Add(time.Minute)
	b.host.SetJobs(printer, deviceapi.Jobs{Current: &deviceapi.Job{
		ID: "job-1", Path: "/usb/A~1.BGC", DisplayName: "a.bgcode", State: "PRINTING", Started: b.clock.Now(),
	}})
	b.runner.Sweep(t.Context())

	if active := b.queue(t).Active(); active == nil || active.State != queue.Printing || active.JobID != "job-1" {
		t.Fatalf("after the sweep the entry reads %+v", active)
	}
}

func TestAPrintThatStartedBeforeTheSendIsNotTheQueues(t *testing.T) {
	t.Parallel()

	b := newBench(t, "a")
	b.clock.Add(time.Hour)

	if _, err := b.runner.Start(t.Context(), printer, alice, ""); err != nil {
		t.Fatal(err)
	}

	b.host.SetJobs(printer, deviceapi.Jobs{Current: &deviceapi.Job{
		ID: "old", Path: "/usb/a.bgcode", State: "PRINTING", Started: b.clock.Now().Add(-time.Hour),
	}})
	b.runner.Sweep(t.Context())

	if active := b.queue(t).Active(); active.State != queue.Sending {
		t.Fatalf("an earlier print of the same name was taken for the queue's: %+v", active)
	}
}

// A print of a file with the queue's name that began before the queue sent
// its own is somebody else's, in another folder or of an earlier send. Its
// start and its end leave the entry the queue is still sending alone.
func TestTheEventsOfAnEarlierPrintOfTheSameNameAreNotTheQueues(t *testing.T) {
	t.Parallel()

	b := newBench(t, "a")
	b.clock.Add(time.Hour)

	go func() { _ = b.runner.Run(t.Context(), b.host) }()

	if _, err := b.runner.Start(t.Context(), printer, alice, ""); err != nil {
		t.Fatal(err)
	}

	earlier := b.clock.Now().Add(-time.Hour)
	b.host.Emit(printer, event.TypeDeviceJobStarted, event.JobStarted{
		JobID: "foreign", Path: "/usb/archive/a.bgcode", Started: earlier,
	})
	b.host.Emit(printer, event.TypeDeviceJobSettled, event.JobSettled{
		JobID: "foreign", Path: "/usb/archive/a.bgcode", Outcome: event.JobCancelled, Ended: earlier,
	})

	// The queue's own print, which the loop reads after the two above.
	b.host.Emit(printer, event.TypeDeviceJobStarted, event.JobStarted{
		JobID: "own", Path: "/usb/a.bgcode", Started: b.clock.Now(),
	})
	eventually(t, "the entry prints as the queue's own job", func() bool {
		held := b.queue(t)
		active := held.Active()

		return len(held.History) == 0 && active != nil && active.JobID == "own"
	})
}

// A send whose time has passed is given back even when the server will not
// say what the printer prints, such as after the module lost observe.
func TestASendIsGivenBackWhenThePrintersJobsCannotBeRead(t *testing.T) {
	t.Parallel()

	b := newBench(t, "a")

	if _, err := b.runner.Start(t.Context(), printer, alice, ""); err != nil {
		t.Fatal(err)
	}

	b.host.Revoke(hostfake.Self, "observe", "device", printer)
	b.clock.Add(settings.DefaultSendTimeout + time.Minute)
	b.runner.Sweep(t.Context())

	if next := b.queue(t).Next(); next == nil || next.Note != queue.NoteNotStarted {
		t.Fatalf("after the timeout the entry reads %+v", next)
	}
}

// The queue sent a file, and the whole print started and ended while no event
// reached it, such as while the server was down. The printer's last print is
// that file, so the entry goes to the history as printed and is not sent again.
func TestAPrintThatStartedAndEndedUnseenIsTheQueues(t *testing.T) {
	t.Parallel()

	b := newBench(t, "a")

	if _, err := b.runner.Start(t.Context(), printer, alice, ""); err != nil {
		t.Fatal(err)
	}

	sent := b.clock.Now()
	ended := sent.Add(40 * time.Minute)
	b.host.SetJobs(printer, deviceapi.Jobs{Last: &deviceapi.Job{
		ID:      "job-1",
		Path:    "/usb/a.bgcode",
		State:   "FINISHED",
		Outcome: "finished",
		Started: sent.Add(time.Minute),
		Ended:   &ended,
	}})
	b.clock.Add(settings.DefaultSendTimeout + time.Minute)
	b.runner.Sweep(t.Context())

	held := b.queue(t)
	if held.Active() != nil || held.Next() != nil {
		t.Fatalf("the printed file is still on the queue: %+v", held.Entries)
	}

	if len(held.History) != 1 || held.History[0].Outcome != queue.Finished || held.History[0].JobID != "job-1" {
		t.Fatalf("the history reads %+v", held.History)
	}
}

func TestASendThePrinterNeverAnswersIsGivenBack(t *testing.T) {
	t.Parallel()

	b := newBench(t, "a")

	if _, err := b.runner.Start(t.Context(), printer, alice, ""); err != nil {
		t.Fatal(err)
	}

	b.clock.Add(settings.DefaultSendTimeout + time.Minute)
	b.runner.Sweep(t.Context())

	if next := b.queue(t).Next(); next == nil || next.Note != queue.NoteNotStarted {
		t.Fatalf("after the timeout the entry reads %+v", next)
	}
}

func TestSetReadySendsTheVerbAsThePerson(t *testing.T) {
	t.Parallel()

	b := newBench(t)

	if _, err := b.runner.SetReady(t.Context(), printer, alice, true); err != nil {
		t.Fatal(err)
	}

	if _, err := b.runner.SetReady(t.Context(), printer, alice, false); err != nil {
		t.Fatal(err)
	}

	commands := b.host.Commands()
	if len(commands) != 2 || commands[0].Verb != runner.VerbSetReady || commands[1].Verb != runner.VerbCancelReady ||
		commands[0].Principal != alice {
		t.Fatalf("the commands were %+v", commands)
	}
}

// A file on the printer that names no print time and no printer is asked
// about once, and not again on every read of the queue.
func TestAFileThatSaysNothingIsAskedAboutOnce(t *testing.T) {
	t.Parallel()

	b := newBench(t)
	b.host.PutPrinterFile(printer, "usb/plain.gcode")
	b.host.Read(printer, "usb/plain.gcode")
	b.host.Read(printer, "usb")

	if _, err := b.store.Update(t.Context(), printer, func(q *queue.Queue) error {
		return q.Add(queue.Entry{
			ID: "plain", Path: "usb/plain.gcode", Name: "plain.gcode", AddedBy: alice, AddedAt: b.clock.Now(),
		}, 10)
	}); err != nil {
		t.Fatal(err)
	}

	first := b.runner.Fill(t.Context(), alice, printer, *b.queue(t))
	if next := first.Next(); next == nil || next.EstimatedSeconds != 0 {
		t.Fatalf("the file that says nothing reads %+v", next)
	}

	asked := b.host.FileAsks()
	if asked == 0 {
		t.Fatal("the first read asked nothing about the file")
	}

	b.runner.Fill(t.Context(), alice, printer, *b.queue(t))

	if again := b.host.FileAsks(); again != asked {
		t.Fatalf("the second read asked %d more questions about the file", again-asked)
	}
}

// Files of one folder read the folder's listing once: each file is asked
// about, and the folder once.
func TestFilesOfOneFolderReadItsListingOnce(t *testing.T) {
	t.Parallel()

	b := newBench(t)
	b.host.Read(printer, "usb")

	for _, id := range []string{"a", "b", "c"} {
		b.host.PutPrinterFile(printer, "usb/"+id+".bgcode")
		if _, err := b.store.Update(t.Context(), printer, func(q *queue.Queue) error {
			return q.Add(queue.Entry{
				ID: id, Path: "usb/" + id + ".bgcode", Name: id + ".bgcode", AddedBy: alice, AddedAt: b.clock.Now(),
			}, 10)
		}); err != nil {
			t.Fatal(err)
		}
	}

	before := b.host.FileAsks()
	b.runner.Fill(t.Context(), alice, printer, *b.queue(t))

	if asked := b.host.FileAsks() - before; asked != 4 {
		t.Fatalf("three files of one folder took %d questions, want 4", asked)
	}
}

// A file the server knows nothing about is not asked about on every read of
// the queue. A landed read of the printer's files has it asked about again.
func TestAnUnheardFileWaitsForAReadOfThePrinterFiles(t *testing.T) {
	t.Parallel()

	b := newBench(t)

	go func() { _ = b.runner.Run(t.Context(), b.host) }()

	if _, err := b.store.Update(t.Context(), printer, func(q *queue.Queue) error {
		return q.Add(queue.Entry{
			ID: "gone", Path: "usb/gone.bgcode", Name: "gone.bgcode", AddedBy: alice, AddedAt: b.clock.Now(),
		}, 10)
	}); err != nil {
		t.Fatal(err)
	}

	before := b.host.FileAsks()
	b.runner.Fill(t.Context(), alice, printer, *b.queue(t))

	asked := b.host.FileAsks()
	if asked == before {
		t.Fatal("the first read asked nothing about the file")
	}

	b.runner.Fill(t.Context(), alice, printer, *b.queue(t))

	if again := b.host.FileAsks(); again != asked {
		t.Fatalf("the second read asked %d more questions about the file", again-asked)
	}

	b.host.Emit(printer, event.TypeDeviceFilesChanged, map[string]any{"path": "/usb", "reading": false})
	eventually(t, "the file is asked about again", func() bool {
		held := b.host.FileAsks()
		b.runner.Fill(t.Context(), alice, printer, *b.queue(t))

		return b.host.FileAsks() > held
	})
}

func eventually(t *testing.T, what string, done func() bool) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)

	for time.Now().Before(deadline) {
		if done() {
			return
		}

		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("timed out waiting until %s", what)
}
