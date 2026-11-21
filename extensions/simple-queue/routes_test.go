package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/prusa3d/prusa-connect/sdk/deviceapi"
	"github.com/prusa3d/prusa-connect/sdk/routesapi"
	"github.com/prusa3d/prusa-connect/sdk/wirerpc"

	"github.com/prusa3d/simple-queue/internal/hostfake"
	"github.com/prusa3d/simple-queue/internal/queue"
	"github.com/prusa3d/simple-queue/internal/routes"
)

const (
	queuePath = "/printers/" + printer
	bracket   = `{"storage":"local","path":"parts/bracket.bgcode"}`
)

// refusal is the part of a problem body a test reads.
type refusal struct {
	Code string `json:"code"`
}

// refused makes one call and checks it was refused with code.
func (r rig) refused(t *testing.T, who, method, path, body string, code routes.Code) {
	t.Helper()

	var got refusal

	status := r.as(t, who, method, path, body, &got)
	if status != code.Status || got.Code != code.Name {
		t.Fatalf("%s %s as %s answered %d %s, want %d %s", method, path, who, status, got.Code, code.Status, code.Name)
	}
}

// add queues the bracket as alice and answers the queue.
func (r rig) add(t *testing.T) routes.View {
	t.Helper()

	var view routes.View
	if status := r.as(t, alice, http.MethodPost, queuePath+"/entries", bracket, &view); status != http.StatusCreated {
		t.Fatalf("adding the bracket answered %d", status)
	}

	return view
}

func TestAPersonWhoMaySeeThePrinterSeesItsQueue(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	r.add(t)

	var view routes.View
	if status := r.as(t, bob, http.MethodGet, queuePath, "", &view); status != http.StatusOK {
		t.Fatalf("bob reading the queue answered %d", status)
	}

	if view.MayChange || len(view.Entries) != 1 || view.Printer.Name != "MK4" || view.Limits.MaxWaiting != 3 {
		t.Fatalf("bob reads %+v", view)
	}

	r.refused(t, bob, http.MethodPost, queuePath+"/entries", bracket, routes.NotOperator)
	r.refused(t, carol, http.MethodGet, queuePath, "", routes.PrinterNotFound)
	r.refused(t, "", http.MethodGet, queuePath, "", routes.NoPrincipal)
}

// The queue checks again before it starts a file that its owner may still
// print, and the server answers that only about a person. So a service
// account may not queue, even with operate on the printer.
func TestAServiceAccountMayNotQueue(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	robot := "service_account:farm"
	r.host.Grant(robot, "observe", "device", printer)
	r.host.Grant(robot, "operate", "device", printer)

	r.refused(t, robot, http.MethodPost, queuePath+"/entries", bracket, routes.NotAPerson)
}

func TestOnlyAPrintFileThatIsThereIsQueued(t *testing.T) {
	t.Parallel()

	r := newRig(t)

	r.refused(t, alice, http.MethodPost, queuePath+"/entries",
		`{"storage":"local","path":"parts/model.stl"}`, routes.NotPrintable)
	r.refused(t, alice, http.MethodPost, queuePath+"/entries",
		`{"storage":"local","path":"parts/missing.bgcode"}`, routes.FileNotFound)
	r.refused(t, alice, http.MethodPost, queuePath+"/entries", `{"storage":"local"}`, routes.Invalid)
	r.refused(t, alice, http.MethodPost, queuePath+"/entries", `{"storage":"local","pth":"x"}`, routes.Invalid)

	r.host.Revoke(alice, "read_storage", "resource_kind", "storage")
	r.refused(t, alice, http.MethodPost, queuePath+"/entries", bracket, routes.FileDenied)
}

func TestAFileOnThePrinterIsQueuedAndPrintedWhereItIs(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	entries := queuePath + "/entries"

	r.refused(t, alice, http.MethodPost, entries, `{"path":"usb/missing.bgcode"}`, routes.FileNotFound)
	r.refused(t, alice, http.MethodPost, entries, `{"path":"usb/models"}`, routes.NotPrintable)
	r.refused(t, bob, http.MethodPost, entries, `{"path":"usb/plate.bgcode"}`, routes.NotOperator)

	// The file on the printer takes no read_storage.
	r.host.Revoke(alice, "read_storage", "resource_kind", "storage")
	r.host.SetState(printer, "READY")

	var view routes.View

	status := r.as(t, alice, http.MethodPost, entries, `{"path":"usb/plate.bgcode"}`, &view)
	if status != http.StatusCreated {
		t.Fatalf("adding the file on the printer answered %d", status)
	}

	sent := r.host.Sent()
	if len(sent) != 1 || sent[0].Storage != "" || sent[0].Source != "" || sent[0].Path != "usb/plate.bgcode" {
		t.Fatalf("the file on the printer was started as %+v", sent)
	}

	if len(view.Entries) != 1 || view.Entries[0].Storage != "" || view.Entries[0].Name != "plate.bgcode" ||
		view.Entries[0].Landing != "usb/plate.bgcode" {
		t.Fatalf("the answer reads %+v", view.Entries)
	}
}

// A file dropped on the printer's page is queued while it is still copied
// there, so the add names a path the printer does not hold yet. Only that
// check is left out: the person must still be allowed to print, the name
// must be a print file, and a file in a storage cannot be on its way. The
// file is not started on a ready printer, because it is not there.
func TestAFileOnItsWayToThePrinterIsQueuedWithoutBeingThere(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	r.host.SetState(printer, "READY")

	entries := queuePath + "/entries"
	coming := `{"path":"usb/coming.bgcode","arriving":true}`

	r.refused(t, alice, http.MethodPost, entries, `{"path":"usb/coming.bgcode"}`, routes.FileNotFound)
	r.refused(t, bob, http.MethodPost, entries, coming, routes.NotOperator)
	r.refused(t, alice, http.MethodPost, entries, `{"path":"usb/coming.stl","arriving":true}`, routes.NotPrintable)
	r.refused(t, alice, http.MethodPost, entries,
		`{"storage":"local","path":"parts/bracket.bgcode","arriving":true}`, routes.Invalid)
	// A file on its way is not looked for, so a path no printer holds is
	// refused when it is added.
	r.refused(t, alice, http.MethodPost, entries, `{"path":"usb/../etc/coming.bgcode","arriving":true}`,
		routes.Invalid)
	r.refused(t, alice, http.MethodPost, entries,
		`{"path":"usb/`+strings.Repeat("a", 1100)+`.bgcode","arriving":true}`, routes.Invalid)

	var view routes.View

	if status := r.as(t, alice, http.MethodPost, entries, coming, &view); status != http.StatusCreated {
		t.Fatalf("adding a file on its way answered %d", status)
	}

	if len(view.Entries) != 1 || !view.Entries[0].Arriving || view.Entries[0].State != queue.Waiting ||
		view.Entries[0].Storage != "" || view.Entries[0].Path != "usb/coming.bgcode" {
		t.Fatalf("the answer reads %+v", view.Entries)
	}

	if sent := r.host.Sent(); len(sent) != 0 {
		t.Fatalf("a file that is not on the printer yet was started: %+v", sent)
	}
}

// What a file says about itself is read when it is added, from the storage
// for a file there and from the printer for a file it holds. The page draws
// the queue's times and each file's details from it.
func TestAQueuedFileKnowsWhatItsSlicerWrote(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	inStorage := queue.Facts{
		EstimatedSeconds: 7503, PrinterModel: "XL2IS", LayerHeightMM: 0.2, Material: "PLA", Size: 4_760_000,
	}
	onPrinter := queue.Facts{
		EstimatedSeconds: 930, PrinterModel: "MK4S", LayerHeightMM: 0.15, Material: "PETG;PLA", Size: 512_000,
		Modified: 1_794_000_000,
	}
	r.host.SetFileFacts("local", "parts/bracket.bgcode", inStorage)
	r.host.SetPrinterFileFacts(printer, "usb/plate.bgcode", onPrinter)
	// The listing of the folder is read first, as the dialog that picks the
	// file reads it.
	r.as(t, alice, http.MethodGet, queuePath, "", nil)

	entries := queuePath + "/entries"

	var view routes.View

	for _, body := range []string{bracket, `{"path":"usb/plate.bgcode"}`} {
		if status := r.as(t, alice, http.MethodPost, entries, body, &view); status != http.StatusCreated {
			t.Fatalf("adding %s answered %d", body, status)
		}
	}

	if len(view.Entries) != 2 || view.Entries[0].Facts != inStorage {
		t.Fatalf("the queued files say %+v", view.Entries)
	}

	// The server had read nothing about the printer's file when it was
	// added, and has by the time somebody reads the queue.
	if status := r.as(t, alice, http.MethodGet, queuePath, "", &view); status != http.StatusOK {
		t.Fatalf("reading the queue answered %d", status)
	}

	if view.Entries[1].Facts != onPrinter {
		t.Fatalf("the printer's file says %+v", view.Entries[1].Facts)
	}

	if view.RemainingSeconds != 0 {
		t.Fatalf("nothing prints, and the view says %d s remain", view.RemainingSeconds)
	}
}

// The picture of a file on the printer is kept under its size and the time
// the listing of its folder names. The printer's answer about the file alone
// carries no time, so a queued file keeps the listing's, and its picture is
// found without asking the printer again, also when the server had already
// read the file, as the dialog that picks it does.
func TestAQueuedPrinterFileKeepsTheTimeItsPictureIsKeptUnder(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	r.host.SetPrinterFileFacts(printer, "usb/plate.bgcode", queue.Facts{
		EstimatedSeconds: 930, Size: 1024, Modified: 1_794_000_000,
	})
	r.host.Read(printer, "usb/plate.bgcode")

	var view routes.View
	if status := r.as(
		t, alice, http.MethodPost, queuePath+"/entries", `{"path":"usb/plate.bgcode"}`, &view,
	); status != http.StatusCreated {
		t.Fatalf("adding the file answered %d", status)
	}

	if got := view.Entries[0].Facts; got.Modified != 1_794_000_000 || got.EstimatedSeconds != 930 {
		t.Fatalf("the queued file says %+v", got)
	}
}

// Asking a printer about one file takes seconds, so adding a file on the
// printer never waits for the printer. The file is found in the listing of
// its folder the server holds, and what it says about itself is filled in
// from what the server learns, when somebody next reads the queue.
func TestAddingAPrinterFileDoesNotWaitForThePrinter(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	r.host.SetPrinterFileFacts(printer, "usb/plate.bgcode", queue.Facts{EstimatedSeconds: 930, Size: 1024})
	release := r.host.HoldFileInfo()
	t.Cleanup(release)

	var view routes.View
	if status := r.as(
		t,
		alice,
		http.MethodPost,
		queuePath+"/entries",
		`{"path":"usb/plate.bgcode"}`,
		&view,
	); status != http.StatusCreated {
		t.Fatalf("adding a file on the printer answered %d", status)
	}

	if len(view.Entries) != 1 || view.Entries[0].Size != 1024 || view.Entries[0].EstimatedSeconds != 0 {
		t.Fatalf("the file was added as %+v", view.Entries)
	}

	if status := r.as(t, alice, http.MethodGet, queuePath, "", &view); status != http.StatusOK {
		t.Fatalf("reading the queue answered %d", status)
	}

	if view.Entries[0].EstimatedSeconds != 930 {
		t.Fatalf("the file was not filled in: %+v", view.Entries[0])
	}
}

// A file dropped on the printer's page starts as soon as it lands, before
// anybody read the queue. What it says about itself is filled in while it
// prints too, so the timeline knows how long it takes.
func TestThePrintingFileLearnsWhatItsSlicerWrote(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	r.host.SetState(printer, "READY")
	release := r.host.HoldFileInfo()

	var view routes.View
	if status := r.as(
		t, alice, http.MethodPost, queuePath+"/entries", `{"path":"usb/plate.bgcode"}`, &view,
	); status != http.StatusCreated {
		t.Fatalf("adding the file answered %d", status)
	}

	if len(view.Entries) != 1 || !view.Entries[0].Active() || view.Entries[0].EstimatedSeconds != 0 {
		t.Fatalf("the file was added as %+v", view.Entries)
	}

	release()
	r.host.SetPrinterFileFacts(printer, "usb/plate.bgcode", queue.Facts{EstimatedSeconds: 45, Size: 1024})

	if status := r.as(t, alice, http.MethodGet, queuePath, "", &view); status != http.StatusOK {
		t.Fatalf("reading the queue answered %d", status)
	}

	if view.Entries[0].EstimatedSeconds != 45 {
		t.Fatalf("the printing file still says %+v", view.Entries[0].Facts)
	}
}

// While a file of the queue prints, the view says how long the printer says
// it still takes, which is where the times of the files behind it start.
func TestTheViewSaysHowLongThePrintStillTakes(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	r.add(t)

	if _, err := r.module.store.Update(t.Context(), printer, func(q *queue.Queue) error {
		if err := q.Begin(q.Next().ID, alice, "usb/bracket.bgcode", time.Now()); err != nil {
			return err
		}

		if !q.Started("job-1", "/usb/bracket.bgcode", time.Now()) {
			return queue.ErrUnchanged
		}

		return nil
	}); err != nil {
		t.Fatal(err)
	}

	r.host.SetState(printer, "PRINTING")
	r.host.SetJobs(
		printer,
		deviceapi.Jobs{Current: &deviceapi.Job{ID: "job-1", State: "PRINTING", TimeRemaining: 4020, Progress: 37}},
	)

	var view routes.View
	if status := r.as(t, alice, http.MethodGet, queuePath, "", &view); status != http.StatusOK {
		t.Fatalf("reading the queue answered %d", status)
	}

	if view.RemainingSeconds != 4020 || view.Progress != 37 {
		t.Fatalf("the view says %d s remain at %v %%, want 4020 at 37", view.RemainingSeconds, view.Progress)
	}
}

func TestAFullQueueRefusesTheNextFile(t *testing.T) {
	t.Parallel()

	r := newRig(t)

	for range 3 {
		r.add(t)
	}

	r.refused(t, alice, http.MethodPost, queuePath+"/entries", bracket, routes.QueueFull)
}

func TestAFileAddedToAReadyPrinterStartsAtOnce(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	r.host.SetState(printer, "READY")

	view := r.add(t)

	sent := r.host.Sent()
	if len(sent) != 1 || sent[0].Principal != alice || sent[0].Path != "usb/bracket.bgcode" {
		t.Fatalf("adding to a ready printer sent %+v", sent)
	}

	if len(view.Entries) != 1 || view.Entries[0].State != queue.Sending {
		t.Fatalf("the answer reads %+v", view.Entries)
	}
}

// The switch that lets the queue start alone is a grant the page writes to
// the host, and the page reads the queue right after. That read is what
// starts the waiting file on a ready printer, not the sweep a minute later.
func TestTurningTheSwitchOnStartsTheNextFileOnAReadyPrinter(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	r.add(t)
	r.host.SetState(printer, "READY")

	// With the switch off a read of the queue starts nothing.
	var view routes.View
	if status := r.as(t, alice, http.MethodGet, queuePath, "", &view); status != http.StatusOK {
		t.Fatalf("reading the queue answered %d", status)
	}

	// The runner looks at one printer after another, so once it has begun
	// two looks asked for here, any look the read asked for has ended.
	for range 2 {
		looked := r.host.Looks()
		r.module.runner.WakePrinter(printer)

		for deadline := time.Now().Add(5 * time.Second); r.host.Looks() == looked; {
			if time.Now().After(deadline) {
				t.Fatal("the runner did not look at the printer")
			}

			time.Sleep(time.Millisecond)
		}
	}

	if sent := r.host.Sent(); len(sent) != 0 {
		t.Fatalf("with the switch off the queue sent %+v", sent)
	}

	r.host.Grant(hostfake.Self, "operate", "device", printer)

	if status := r.as(t, alice, http.MethodGet, queuePath, "", &view); status != http.StatusOK {
		t.Fatalf("reading the queue answered %d", status)
	}

	deadline := time.Now().Add(5 * time.Second)
	for len(r.host.Sent()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the waiting file did not start after the switch was turned on")
		}

		time.Sleep(10 * time.Millisecond)
	}
}

func TestPrintNext(t *testing.T) {
	t.Parallel()

	r := newRig(t)

	r.refused(t, alice, http.MethodPost, queuePath+"/start", "", routes.QueueEmpty)

	r.add(t)
	r.add(t)

	var view routes.View
	if status := r.as(t, alice, http.MethodPost, queuePath+"/start", "", &view); status != http.StatusOK {
		t.Fatalf("Print next answered %d", status)
	}

	active := view.Entries[0]
	if active.State != queue.Sending || active.StartedBy != alice {
		t.Fatalf("after Print next the first entry reads %+v", active)
	}

	r.refused(t, alice, http.MethodPost, queuePath+"/start", "", routes.PrinterBusy)
	r.refused(t, alice, http.MethodDelete, queuePath+"/entries/"+active.ID, "", routes.EntryActive)
}

func TestMoveAndRemove(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	first := r.add(t).Entries[0].ID
	second := r.add(t).Entries[1].ID

	var view routes.View
	if status := r.as(
		t,
		alice,
		http.MethodPost,
		queuePath+"/entries/"+second+"/move",
		`{"position":0}`,
		&view,
	); status != http.StatusOK {
		t.Fatalf("move answered %d", status)
	}

	if view.Entries[0].ID != second || view.Entries[1].ID != first {
		t.Fatalf("after the move the queue reads %+v", view.Entries)
	}

	if status := r.as(t, alice, http.MethodDelete, queuePath+"/entries/"+second, "", &view); status != http.StatusOK {
		t.Fatalf("remove answered %d", status)
	}

	if len(view.Entries) != 1 || view.Entries[0].ID != first {
		t.Fatalf("after the remove the queue reads %+v", view.Entries)
	}

	r.refused(t, alice, http.MethodDelete, queuePath+"/entries/"+second, "", routes.EntryNotFound)
	r.refused(t, alice, http.MethodPost, queuePath+"/entries/"+second+"/move", `{"position":0}`, routes.EntryNotFound)
}

func TestMarkingThePrinterReady(t *testing.T) {
	t.Parallel()

	r := newRig(t)

	var view routes.View
	if status := r.as(t, alice, http.MethodPost, queuePath+"/ready", `{"ready":true}`, &view); status != http.StatusOK {
		t.Fatalf("ready answered %d", status)
	}

	if commands := r.host.Commands(); len(commands) != 1 || commands[0].Principal != alice {
		t.Fatalf("ready sent %+v", commands)
	}

	if view.Printer.State != "READY" {
		t.Fatalf("after ready the printer reads %+v", view.Printer)
	}

	r.host.AddPrinter(printer, hostfake.Printer{State: "IDLE", Connected: true})
	r.refused(t, alice, http.MethodPost, queuePath+"/ready", `{"ready":true}`, routes.ReadyUnsupported)
}

func TestTheOverviewListsThePrintersAPersonMaySee(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	r.add(t)

	// A printer nobody queued a file for is listed too, so a person finds the
	// queue of every printer from the page.
	r.host.AddPrinter("printer-2", hostfake.Printer{Name: "Second", State: "IDLE", Connected: true})
	r.host.Grant(bob, "observe", "device", "printer-2")

	var overview routes.Overview
	if status := r.as(t, bob, http.MethodGet, "/queues/summary", "", &overview); status != http.StatusOK {
		t.Fatalf("the overview answered %d", status)
	}

	if len(overview.Printers) != 2 || overview.Printers[0].Waiting != 1 || overview.Printers[0].Next == nil {
		t.Fatalf("bob's overview reads %+v", overview)
	}

	if second := overview.Printers[1]; second.Device != "printer-2" || second.Waiting != 0 || second.Next != nil ||
		second.Printer.Name != "Second" {
		t.Fatalf("the printer without a queue reads %+v", second)
	}

	if r.as(t, carol, http.MethodGet, "/queues/summary", "", &overview); len(overview.Printers) != 0 {
		t.Fatalf("carol's overview reads %+v", overview)
	}
}

func TestTheBoardAnswersEveryQueueWholeInOneRead(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	r.add(t)
	r.host.AddPrinter("printer-2", hostfake.Printer{Name: "Second", State: "IDLE", Connected: true})
	r.host.Grant(bob, "observe", "device", "printer-2")
	r.host.Grant(alice, "observe", "device", "printer-2")

	before := r.host.Checks()

	var board routes.Board
	if status := r.as(t, alice, http.MethodGet, "/queues", "", &board); status != http.StatusOK {
		t.Fatalf("the board answered %d", status)
	}

	// Two checks for the queue with a file waiting, whether alice may change
	// it and whether it starts by itself, and one for the empty queue.
	if asked := r.host.Checks() - before; asked != 3 {
		t.Fatalf("the board asked %d access checks, want 3", asked)
	}

	if len(board.Queues) != 2 {
		t.Fatalf("alice's board reads %+v", board)
	}

	first, second := board.Queues[0], board.Queues[1]
	if first.Device != printer || len(first.Entries) != 1 || !first.MayChange || first.Limits.MaxWaiting == 0 {
		t.Fatalf("the queue with a file reads %+v", first)
	}

	// The page draws each printer's picture by the machine it says it is.
	if first.Printer.Model != "MK4S" || second.Printer.Model != "" {
		t.Fatalf("the printers read as %q and %q", first.Printer.Model, second.Printer.Model)
	}

	if second.Device != "printer-2" || len(second.Entries) != 0 || second.Entries == nil || second.MayChange {
		t.Fatalf("the printer without a queue reads %+v", second)
	}

	if r.as(t, bob, http.MethodGet, "/queues", "", &board); len(board.Queues) != 2 || board.Queues[0].MayChange {
		t.Fatalf("bob's board reads %+v", board)
	}

	if r.as(t, carol, http.MethodGet, "/queues", "", &board); len(board.Queues) != 0 {
		t.Fatalf("carol's board reads %+v", board)
	}
}

func TestClearingTheHistory(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	r.add(t)

	var view routes.View
	if status := r.as(t, alice, http.MethodDelete, queuePath+"/history", "", &view); status != http.StatusOK {
		t.Fatalf("clearing an empty history answered %d", status)
	}

	if len(view.Entries) != 1 {
		t.Fatalf("after clearing the queue reads %+v", view)
	}
}

// The ended prints are read on their own route, so a queue or a board is
// answered without them, and the history of one printer has them all.
func TestTheHistoryIsReadOnItsOwn(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	r.add(t)
	r.add(t)

	if _, err := r.module.store.Update(t.Context(), printer, func(q *queue.Queue) error {
		ended := q.Entries[0]
		ended.State = queue.Ended
		q.Entries = q.Entries[1:]
		q.History = []queue.Entry{ended}

		return nil
	}); err != nil {
		t.Fatal(err)
	}

	var raw map[string]any
	if status := r.as(t, bob, http.MethodGet, queuePath, "", &raw); status != http.StatusOK {
		t.Fatalf("reading the queue answered %d", status)
	}

	if _, ok := raw["history"]; ok {
		t.Fatalf("the queue carries its history: %v", raw["history"])
	}

	var ended routes.History
	if status := r.as(t, bob, http.MethodGet, queuePath+"/history", "", &ended); status != http.StatusOK {
		t.Fatalf("reading the history answered %d", status)
	}

	if len(ended.History) != 1 || ended.History[0].State != queue.Ended {
		t.Fatalf("the history reads %+v", ended.History)
	}

	r.refused(t, carol, http.MethodGet, queuePath+"/history", "", routes.PrinterNotFound)
}

func TestARequestBeforeTheSettingsIsRefused(t *testing.T) {
	t.Parallel()

	fake := hostfake.New()
	held := newModule(
		host{fleet: fake.Fleet(), access: fake, files: fake, feed: fake},
		func(string, ...any) {},
	)
	r := rig{module: held, serves: held.methods(), host: fake}

	response := r.call(t, routesapi.Request{Method: http.MethodGet, Path: "/queues/summary", Principal: alice})
	if response.Status != routes.NotReady.Status {
		t.Fatalf("a request before the settings answered %d", response.Status)
	}
}

// The queues are kept in a database the operator grants. Without it the
// module refuses its settings with words the operator reads on the instance,
// and never reports ready, so no request is served out of nothing.
func TestAStartWithNoDatabaseIsRefused(t *testing.T) {
	t.Parallel()

	fake := hostfake.New()
	held := newModule(host{fleet: fake.Fleet(), access: fake, files: fake, feed: fake}, func(string, ...any) {})
	r := rig{module: held, serves: held.methods(), host: fake}

	params, err := json.Marshal(
		wirerpc.Settings{Config: json.RawMessage(`{"max_waiting": 3, "keep_history": 5, "send_timeout": "1h"}`)},
	)
	if err != nil {
		t.Fatal(err)
	}

	_, err = r.serves[wirerpc.MethodConfigure](
		t.Context(),
		wirerpc.Call{Method: wirerpc.MethodConfigure, Params: params},
	)
	if err == nil || !strings.Contains(err.Error(), "needs.database") {
		t.Fatalf("settings with no database grant answered %v", err)
	}

	response := r.call(t, routesapi.Request{Method: http.MethodGet, Path: "/queues/summary", Principal: alice})
	if response.Status != routes.NotReady.Status {
		t.Fatalf("a request with no database answered %d", response.Status)
	}
}

func TestABodyOverTheBoundIsRefused(t *testing.T) {
	t.Parallel()

	r := newRig(t)
	huge := `{"path":"` + strings.Repeat("a", routes.MaxBodyBytes) + `"}`

	r.refused(t, alice, http.MethodPost, queuePath+"/entries", huge, routes.BodyTooLarge)
}
