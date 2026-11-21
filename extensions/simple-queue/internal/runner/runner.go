// Package runner sends the files of the queues to the printers and follows
// each print until it ends.
//
// A file starts in one of two ways. A person presses Print, and the file
// is sent as that person. Or the printer is ready, which means a person said
// its bed is clear, and the queue sends the next file as itself. A printer
// that finished a print is not ready, so the queue never starts a print on a
// bed nobody cleared.
//
// A send that failed is not tried again by itself. The entry goes back to the
// head of the queue with the reason, and a person decides.
package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/prusa3d/prusa-connect/sdk/accessapi"
	"github.com/prusa3d/prusa-connect/sdk/connectorapi"
	"github.com/prusa3d/prusa-connect/sdk/deviceapi"

	"github.com/prusa3d/simple-queue/internal/queue"
	"github.com/prusa3d/simple-queue/internal/settings"
)

// The verbs and the state the queue reads on a printer.
const (
	// VerbSetReady says the bed is clear and the printer may take the next
	// print. VerbCancelReady takes that back.
	VerbSetReady    = string(connectorapi.VerbSetPrinterReady)
	VerbCancelReady = "core.command.cancel_printer_ready"
	// VerbStartPrint starts a file, and verbPathArgument names it.
	VerbStartPrint = "core.command.start_print"
	// VerbResumePrint goes on with a paused print.
	VerbResumePrint  = "core.command.resume_print"
	verbPathArgument = "path"
	// stateOffline is what a printer that is not connected is said to be.
	stateOffline = "OFFLINE"
	// statePath is where a device's state tree carries its state.
	statePath = "/state"
)

// listPage is how many entries of a folder one read of it asks for.
const listPage = 200

// The questions the queue asks the server, about a person or about itself.
// Each action is in the permissions list of module.yaml, because the server
// answers only about those.
const (
	AskOperate     = "operate"
	AskReadStorage = "read_storage"
	ObjectDevice   = "device"
	ObjectKind     = "resource_kind"
	KindStorage    = "storage"
)

// SweepInterval is how often the queue looks at every printer that has
// entries: whether a ready printer can take the next file, and what became of
// the print it sent. A printer whose state changes is looked at when the event
// arrives, so the sweep is what catches an event the queue never heard.
const SweepInterval = time.Minute

// The refusals Start answers besides the ones of the queue package.
var (
	// ErrNoStorage is a printer that reports no storage the file can be
	// written to.
	ErrNoStorage = errors.New("runner: the printer reports no storage the file can be written to")
	// ErrSendFailed is a print the server refused to start.
	ErrSendFailed = errors.New("runner: the server did not start the print")
	// ErrPrinter is a printer the server would not answer about.
	ErrPrinter = errors.New("runner: the server did not answer about the printer")
	// ErrNotFree is a printer in a state no print starts from.
	ErrNotFree = errors.New("runner: the printer cannot start a print now")
)

// busyAs answers the state that keeps a printer from starting a print, and
// empty for a printer that takes one. A person who presses Print there says
// the bed is clear, as the Queue tab offers. The queue alone starts a file
// only on a ready printer.
func busyAs(printer Printer) string {
	switch {
	case !printer.Connected:
		return stateOffline
	case printer.Free:
		return ""
	default:
		return printer.State
	}
}

// Fleet is the part of the host's fleet the queue uses. `*deviceapi.Client`
// satisfies it.
type Fleet interface {
	List(ctx context.Context, params deviceapi.ListParams) (deviceapi.Page, error)
	Get(ctx context.Context, params deviceapi.GetParams) (deviceapi.Device, error)
	Jobs(ctx context.Context, params deviceapi.JobsParams) (deviceapi.Jobs, error)
	Files(ctx context.Context, params deviceapi.FilesParams) (deviceapi.Listing, error)
	Print(ctx context.Context, params deviceapi.PrintParams) (deviceapi.Started, error)
	Command(ctx context.Context, params deviceapi.CommandParams) (deviceapi.Result, error)
}

// Access asks the server about a person, or about this module.
// `*accessapi.Client` satisfies it.
type Access interface {
	Check(ctx context.Context, params accessapi.CheckParams) (accessapi.Decision, error)
}

// Runner sends and follows the prints of every queue.
type Runner struct {
	// silent holds the entries whose file the server read whole and that
	// names nothing the queue draws, by printer and entry id.
	silent sync.Map
	// unheard holds the entries whose file the server knew nothing about when
	// asked, by printer and entry id. A landed read of the printer's files
	// drops the printer's marks, and its files are asked about again.
	unheard sync.Map

	store    *queue.Store
	fleet    Fleet
	access   Access
	settings func() settings.Values
	now      func() time.Time
	log      func(format string, args ...any)

	// wakeOne asks the loop to look at one printer now.
	wakeOne chan string

	// starting is the printers a start is running for, so a pass and a person
	// pressing Print do not both read the storages and race to the store.
	mu       sync.Mutex
	starting map[string]bool

	stream streamState

	// starts is the file each start command names, by command id, so the
	// frame that says how the command ended can be matched to an entry. Only
	// the loop reads and writes it.
	starts map[string]string
	// copies is where each transfer lands on its printer, by transfer id, so
	// the frame that says how a transfer ended can be matched to a file that
	// was queued while it was copied there. Only the loop reads and writes it.
	copies map[string]string
}

// New answers a runner. now and log are the clock and the place a line for
// the operator goes, so a test holds both.
func New(
	store *queue.Store, fleet Fleet, access Access, values func() settings.Values,
	now func() time.Time, log func(format string, args ...any),
) *Runner {
	return &Runner{
		store:    store,
		fleet:    fleet,
		access:   access,
		settings: values,
		now:      now,
		log:      log,
		wakeOne:  make(chan string, wakeOneBuffer),
		starting: map[string]bool{},
		starts:   map[string]string{},
		copies:   map[string]string{},
	}
}

// Listed answers a file the printer holds as the listing of its folder names
// it. The folder is asked for as the server last read it, which is quick and
// puts nothing on the printer's line of commands, and a file that listing
// does not name yet is asked of the printer itself.
func (r *Runner) Listed(ctx context.Context, principal, device, file string) (deviceapi.File, error) {
	if item, ok := r.inListing(ctx, principal, device, file); ok {
		if item.Folder {
			return deviceapi.File{}, ErrPrinterFolder
		}

		return item, nil
	}

	return r.PrinterFile(ctx, principal, device, file)
}

// inListing finds a path in the listing of its folder as the server last read
// it, and answers false when that listing does not name it. It never waits for
// the printer.
func (r *Runner) inListing(ctx context.Context, principal, device, file string) (deviceapi.File, bool) {
	return findIn(r.folderItems(ctx, principal, device, path.Dir(file)), file)
}

// folderItems is the listing of a printer folder as the server last read it,
// every page of it, and none when the server holds nothing of the folder.
func (r *Runner) folderItems(ctx context.Context, principal, device, folder string) []deviceapi.File {
	var items []deviceapi.File
	for {
		listing, err := r.fleet.Files(ctx, deviceapi.FilesParams{
			Principal: principal, ID: device, Path: folder, Limit: listPage, Offset: len(items), Cached: true,
		})
		if err != nil {
			return items
		}

		items = append(items, listing.Items...)
		if len(listing.Items) == 0 || len(items) >= listing.Total {
			return items
		}
	}
}

// findIn finds a path in the rows of a listing.
func findIn(items []deviceapi.File, file string) (deviceapi.File, bool) {
	for _, item := range items {
		if item.Path == file {
			return item, true
		}
	}

	return deviceapi.File{}, false
}

// Known is what the server knows about a file on the printer without asking
// it: the slicer's facts from the answer the printer last gave about the
// file, and its size and time from the listing of its folder. The size and the
// time name the version the server keeps the file's picture under, and the
// answer about one file does not always carry the time.
func (r *Runner) Known(ctx context.Context, principal, device, file string) (queue.Facts, bool) {
	facts, held, inListing := r.known(ctx, principal, device, file)

	return facts, held || inListing
}

// known is Known, telling apart the answer about the file and its row in the
// listing of its folder.
func (r *Runner) known(ctx context.Context, principal, device, file string) (queue.Facts, bool, bool) {
	return r.knownIn(ctx, principal, device, file, r.folderItems(ctx, principal, device, path.Dir(file)))
}

// knownIn is known with the listing of the file's folder already read.
func (r *Runner) knownIn(
	ctx context.Context, principal, device, file string, items []deviceapi.File,
) (queue.Facts, bool, bool) {
	about, held := r.HeldFile(ctx, principal, device, file)
	listed, inListing := findIn(items, file)

	facts := FactsOf(about)
	if inListing && !listed.Folder {
		facts.Size, facts.Modified = listed.Size, listed.Modified
	}

	return facts, held, inListing
}

// HeldFile answers what the server holds about one file on the printer, at
// once: the answer the printer last gave about it, slicer's facts included. A
// file the server has read nothing about answers false, and the read that
// starts lets a later ask find it.
func (r *Runner) HeldFile(ctx context.Context, principal, device, file string) (deviceapi.File, bool) {
	listing, err := r.fleet.Files(ctx, deviceapi.FilesParams{
		Principal: principal, ID: device, Path: file, Limit: 1, Cached: true,
	})
	if err != nil || listing.Entry == nil || listing.Entry.Folder {
		return deviceapi.File{}, false
	}

	return *listing.Entry, true
}

// Fill writes on the files of a queue that are on the printer what the server
// has learned about them since they were added, such as how long they print.
// That covers the file the queue sent too: a file dropped on the printer's
// page starts once it lands, before anybody read the queue. It asks as
// principal, who reads the queue now, and answers the queue as it is after.
func (r *Runner) Fill(ctx context.Context, principal, device string, held queue.Queue) queue.Queue {
	r.forgetSilent(device, held)

	learned := map[string]queue.Facts{}
	folders := map[string][]deviceapi.File{}
	for _, entry := range held.Entries {
		if !entry.OnPrinter() || entry.Arriving || entry.State == queue.Ended || factsKnown(entry.Facts) {
			continue
		}

		if facts, ok := r.learn(ctx, principal, device, entry, folders); ok {
			learned[entry.ID] = facts
		}
	}

	if len(learned) == 0 {
		return held
	}

	filled, err := r.store.Update(ctx, device, func(q *queue.Queue) error {
		changed := false
		for id, facts := range learned {
			changed = q.Learned(id, facts) || changed
		}

		if !changed {
			return queue.ErrUnchanged
		}

		return nil
	})
	if err != nil {
		return held
	}

	return filled
}

// learn is what the server holds about the file of one entry, and false when
// there is nothing to ask or the server knew nothing. A file the server has
// read whole and that names no print time and no printer has nothing more to
// say, so it is asked about once. One the server knew nothing about waits for
// a read of the printer's files to land. folders keeps the listings read in
// one pass, by folder.
func (r *Runner) learn(
	ctx context.Context, principal, device string, entry queue.Entry, folders map[string][]deviceapi.File,
) (queue.Facts, bool) {
	mark := device + "\x00" + entry.ID
	if _, said := r.silent.Load(mark); said {
		return queue.Facts{}, false
	}

	if _, unheard := r.unheard.Load(mark); unheard {
		return queue.Facts{}, false
	}

	file := strings.TrimPrefix(entry.Path, "/")
	folder := path.Dir(file)
	items, read := folders[folder]
	if !read {
		items = r.folderItems(ctx, principal, device, folder)
		folders[folder] = items
	}

	facts, held, inListing := r.knownIn(ctx, principal, device, file, items)
	if held && inListing && !factsKnown(facts) {
		r.silent.Store(mark, struct{}{})
	}

	if !held && !inListing {
		r.unheard.Store(mark, struct{}{})

		return queue.Facts{}, false
	}

	return facts, true
}

// forgetSilent drops the marks of a printer's entries that have left its
// queue, so the marks last only as long as the entries.
func (r *Runner) forgetSilent(device string, held queue.Queue) {
	kept := map[string]bool{}
	for _, entry := range held.Entries {
		kept[device+"\x00"+entry.ID] = true
	}

	for _, marks := range []*sync.Map{&r.silent, &r.unheard} {
		marks.Range(func(key, _ any) bool {
			name, _ := key.(string)
			if strings.HasPrefix(name, device+"\x00") && !kept[name] {
				marks.Delete(key)
			}

			return true
		})
	}
}

// forgetUnheard drops a printer's marks of files the server knew nothing
// about, so the next read of its queue asks about them again.
func (r *Runner) forgetUnheard(device string) {
	r.unheard.Range(func(key, _ any) bool {
		if name, _ := key.(string); strings.HasPrefix(name, device+"\x00") {
			r.unheard.Delete(key)
		}

		return true
	})
}

// factsKnown reports whether a file on the printer has said what the queue
// draws of it: what the slicer wrote, and the version its picture is kept
// under.
func factsKnown(facts queue.Facts) bool {
	return facts.Modified != 0 && (facts.EstimatedSeconds != 0 || facts.PrinterModel != "")
}

// wakeOneBuffer is how many printers may wait to be looked at. An ask past
// it is dropped: the sweep looks at that printer within a minute anyway.
const wakeOneBuffer = 16

// WakePrinter asks for a look at one printer soon, and at no other.
func (r *Runner) WakePrinter(device string) {
	select {
	case r.wakeOne <- device:
	default:
	}
}

// Printer is what the queue knows about one printer at this moment.
type Printer struct {
	Name string `json:"name"`
	// Model is the machine the printer says it is, such as MK4S, which its
	// picture is drawn for. Empty for one that said nothing.
	Model string `json:"model,omitempty"`
	// State is the printer's own word for what it is doing, such as READY.
	State string `json:"state"`
	// Connected is false for a printer that is away.
	Connected bool `json:"connected"`
	// CanBeReady says the printer takes the verbs that make it ready.
	CanBeReady bool `json:"can_be_ready"`
	// Free says the printer takes a print now.
	Free bool `json:"free"`
	// Ready says a person told the printer its bed is clear.
	Ready bool `json:"ready"`
	// Paused says the printer takes the verb that resumes a print.
	Paused bool `json:"paused,omitempty"`
}

// Look answers the state of one printer, read as principal.
func (r *Runner) Look(ctx context.Context, principal, device string) (Printer, error) {
	held, err := r.fleet.Get(ctx, deviceapi.GetParams{Principal: principal, ID: device})
	if err != nil {
		return Printer{}, fmt.Errorf("%w: %w", ErrPrinter, err)
	}

	return printerOf(held), nil
}

// Seen is one printer a person sees, by its id.
type Seen struct {
	ID      string
	Printer Printer
}

// maxSeenPages bounds how many pages Visible reads, so a server with a very
// large fleet answers the overview with its first thousand printers rather
// than not at all.
const maxSeenPages = 5

// Visible answers every printer the principal sees, in the server's order.
func (r *Runner) Visible(ctx context.Context, principal string) ([]Seen, error) {
	var out []Seen

	for page := range maxSeenPages {
		held, err := r.fleet.List(ctx, deviceapi.ListParams{
			Principal: principal, Limit: deviceapi.MaxPageLimit, Offset: page * deviceapi.MaxPageLimit,
		})
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrPrinter, err)
		}

		for _, device := range held.Items {
			if device.Kind == kindPrinter {
				out = append(out, Seen{ID: device.ID, Printer: printerOf(device)})
			}
		}

		if len(held.Items) < deviceapi.MaxPageLimit || held.Offset+len(held.Items) >= held.Total {
			break
		}
	}

	return out, nil
}

// kindPrinter is the device kind the queue prints on.
const kindPrinter = "printer"

func printerOf(device deviceapi.Device) Printer {
	free := slices.Contains(device.Accepts, VerbStartPrint)

	// The connector said nothing of what its states accept. The queue reads
	// no state by its name, so a connected printer is taken as free and its
	// own answer to the start decides.
	if device.Accepts == nil {
		free = device.Connected
	}

	return Printer{
		Name:       device.Name,
		Model:      device.Model,
		State:      StateOf(device),
		Connected:  device.Connected,
		CanBeReady: slices.Contains(device.Capabilities, VerbSetReady),
		Free:       free,
		Ready:      device.Ready,
		Paused:     slices.Contains(device.Accepts, VerbResumePrint),
	}
}

// StateOf reads the state machine value out of a device's state tree.
func StateOf(device deviceapi.Device) string {
	var state string
	if raw, ok := device.State[statePath]; ok {
		// A state that is not a word reads as no state.
		_ = json.Unmarshal(raw, &state)
	}

	return state
}

// MayStartAlone reports whether this module may start a print on a printer
// with nobody behind the call. Without it the queue starts a file only when a
// person presses Print.
func (r *Runner) MayStartAlone(ctx context.Context, device string) bool {
	decision, err := r.access.Check(ctx, accessapi.CheckParams{
		Action: AskOperate, ObjectType: ObjectDevice, ObjectID: device,
	})

	return err == nil && decision.Allowed
}

// Start sends the next file of a printer's queue and starts it. by is the
// person who pressed Print, and empty for the queue acting by itself.
// want is the entry the caller checked, and empty starts whatever is next.
// When another entry is next, nothing starts and the answer is ErrBusy.
//
// The entry is marked as sent in the store before the server is asked, so two
// copies of this module never send one file twice. When the server refuses,
// the entry goes back to the head of the queue with the reason.
func (r *Runner) Start(ctx context.Context, device, by, want string) (queue.Queue, error) {
	if !r.claim(device) {
		return queue.Queue{}, queue.ErrBusy
	}
	defer r.release(device)

	held, err := r.store.Read(ctx, device)
	if err != nil {
		return queue.Queue{}, err //nolint:wrapcheck // the store names the printer.
	}

	if held.Active() != nil {
		return held, queue.ErrBusy
	}

	next := held.Next()
	if next == nil {
		return held, queue.ErrEmpty
	}

	if want != "" && next.ID != want {
		return held, fmt.Errorf("%w: the queue changed and %s is next", queue.ErrBusy, next.ID)
	}

	entry := *next

	if held, err = r.free(ctx, device, by, held, entry); err != nil {
		return held, err
	}

	if held, err = r.arrived(ctx, device, by, held, entry); err != nil {
		return held, err
	}

	started, held, err := r.send(ctx, device, by, entry)
	if err != nil {
		return held, err
	}

	return r.store.Update(ctx, device, func(q *queue.Queue) error { //nolint:wrapcheck // the store names the printer.
		if q.Sent(entry.ID, started.TransferID, started.CommandID, started.Path) != nil {
			// The print already ended, or somebody took the entry back.
			return queue.ErrUnchanged
		}

		return nil
	})
}

// free checks that the printer can take a file now. A printer that cannot
// holds the entry with its state, and the answer is the queue as it then
// reads and ErrNotFree.
func (r *Runner) free(
	ctx context.Context, device, by string, held queue.Queue, entry queue.Entry,
) (queue.Queue, error) {
	printer, err := r.Look(ctx, by, device)
	if err != nil {
		return held, err
	}

	state := busyAs(printer)
	if state == "" {
		return held, nil
	}

	r.hold(ctx, device, entry.ID, queue.NotePrinterNotFree, state)

	if fresh, unread := r.store.Read(ctx, device); unread == nil {
		held = fresh
	}

	return held, fmt.Errorf("%w: it is %s", ErrNotFree, state)
}

// arrived checks that a file added while it was copied to the printer is
// there now, and records that it is. A start command for a file that is not
// there would be a refused print a person has to answer, so the printer is
// asked first.
func (r *Runner) arrived(
	ctx context.Context, device, by string, held queue.Queue, entry queue.Entry,
) (queue.Queue, error) {
	if !entry.Arriving {
		return held, nil
	}

	file, err := r.PrinterFile(ctx, by, device, entry.Path)
	// A printer still taking a file lists it read-only at its full length,
	// and starts it only once the copy has ended.
	if err == nil && file.ReadOnly {
		err = ErrNoPrinterFile
	}

	if err != nil {
		r.notThere(ctx, device, entry, err)

		return held, err
	}

	return r.store.Update(ctx, device, func(q *queue.Queue) error { //nolint:wrapcheck // the store names the printer.
		if !q.Arrived(entry.ID) {
			return queue.ErrUnchanged
		}

		return nil
	})
}

// send marks the entry as sent and asks the server to send and start it. A
// refusal gives the entry back with the reason, and answers the queue as it
// then reads.
func (r *Runner) send(
	ctx context.Context, device, by string, entry queue.Entry,
) (deviceapi.Started, queue.Queue, error) {
	landing, err := r.landing(ctx, by, device, entry)
	if err != nil {
		held, _ := r.store.Read(ctx, device)

		return deviceapi.Started{}, held, err
	}

	now := r.now()

	held, err := r.store.Update(ctx, device, func(q *queue.Queue) error {
		return q.Begin(entry.ID, by, landing, now)
	})
	if err != nil {
		return deviceapi.Started{}, queue.Queue{}, err //nolint:wrapcheck // the queue's own refusal.
	}

	// A request that names no storage prints the file the printer holds at
	// Path, which is how a file already on the printer is started.
	started, err := r.fleet.Print(ctx, deviceapi.PrintParams{
		Principal: by,
		ID:        device,
		Storage:   entry.Storage,
		Source:    sourceOf(entry),
		Path:      landing,
		// The printer may hold another file of that name, or a part of this
		// one a failed send left. The server then sends under a free name and
		// answers it.
		KeepBoth: !entry.OnPrinter(),
	})
	if errors.Is(err, deviceapi.ErrBusy) {
		// The printer is taking another file, such as one a person is copying
		// to it, or the host is at its limit of calls. Neither is a failure of
		// this file: the entry waits as it did, with no note and no send
		// counted, and the next sweep or the next press tries again.
		held, _ = r.store.Update(ctx, device, func(q *queue.Queue) error {
			if back := q.GiveBack(entry.ID, "", "", r.now()); back != nil {
				return back
			}

			if next := q.Next(); next != nil && next.ID == entry.ID {
				next.Attempts = entry.Attempts
			}

			return nil
		})

		return deviceapi.Started{}, held, fmt.Errorf("%w: %w", queue.ErrBusy, err)
	}

	if err != nil {
		r.log("simple-queue: %s did not start %s: %v\n", device, entry.Name, err)

		// Only a refusal says nothing was sent. Any other error, such as a
		// session that closed mid-call, may have left the server sending and
		// starting the file, so the entry stays sent and the sweep or the send
		// timeout decides what became of it: giving it back could print it
		// twice.
		if refused(err) {
			held, _ = r.store.Update(ctx, device, func(q *queue.Queue) error {
				return q.GiveBack(entry.ID, queue.NoteSendFailed, err.Error(), r.now())
			})
		}

		return deviceapi.Started{}, held, fmt.Errorf("%w: %w", ErrSendFailed, err)
	}

	return started, held, nil
}

// refused reports whether a print error says the server sent and started
// nothing.
func refused(err error) bool {
	for _, nothing := range []error{
		deviceapi.ErrNoDevice, deviceapi.ErrInvalidQuery, deviceapi.ErrVerbUnknown, deviceapi.ErrArgument,
		deviceapi.ErrNotReady, deviceapi.ErrDenied, deviceapi.ErrNoFile, deviceapi.ErrNameTaken,
		deviceapi.ErrPrintRefused, deviceapi.ErrPrintFailed,
	} {
		if errors.Is(err, nothing) {
			return true
		}
	}

	return false
}

// landing answers where on the printer the entry is printed from. A file
// already there is printed where it is. Any other file is written to the
// first storage the printer can take it on, and a printer with none holds the
// entry with a note.
func (r *Runner) landing(ctx context.Context, by, device string, entry queue.Entry) (string, error) {
	if entry.OnPrinter() {
		return entry.Path, nil
	}

	storage, err := r.writable(ctx, by, device)
	if err != nil {
		if errors.Is(err, ErrNoStorage) {
			r.hold(ctx, device, entry.ID, queue.NoteNoStorage, "")
		}

		return "", err
	}

	return storage + "/" + path.Base(entry.Path), nil
}

// The refusals FileOn answers.
var (
	// ErrNoPrinterFile is a path the printer holds no file at.
	ErrNoPrinterFile = errors.New("runner: the printer holds no file at that path")
	// ErrPrinterFolder is a path the printer holds a folder at.
	ErrPrinterFolder = errors.New("runner: the printer holds a folder at that path")
)

// FileOn checks that the printer holds a file at file, asked as principal.
func (r *Runner) FileOn(ctx context.Context, principal, device, file string) error {
	_, err := r.PrinterFile(ctx, principal, device, file)

	return err
}

// FactsOf is what a file on a printer says about itself.
func FactsOf(file deviceapi.File) queue.Facts {
	return queue.Facts{
		EstimatedSeconds: file.EstimatedSeconds, PrinterModel: file.PrinterModel,
		LayerHeightMM: file.LayerHeightMM, Material: file.Material, Size: file.Size,
		Modified: file.Modified,
	}
}

// PrinterFile answers what the printer says about one file it holds: the
// refusals of FileOn, and with the file how long the slicer said it prints,
// where the printer reads that out of it.
func (r *Runner) PrinterFile(ctx context.Context, principal, device, file string) (deviceapi.File, error) {
	listing, err := r.fleet.Files(ctx, deviceapi.FilesParams{
		Principal: principal, ID: device, Path: file, Limit: 1,
	})

	switch {
	case errors.Is(err, deviceapi.ErrNoFile):
		return deviceapi.File{}, ErrNoPrinterFile
	case err != nil:
		return deviceapi.File{}, fmt.Errorf("%w: %w", ErrPrinter, err)
	case listing.Entry == nil:
		return deviceapi.File{}, ErrNoPrinterFile
	case listing.Entry.Folder:
		return deviceapi.File{}, ErrPrinterFolder
	}

	return *listing.Entry, nil
}

// Printing answers what the printer says of its print: how many seconds it
// still takes, and how far it is in whole percent. Each is zero when it prints
// nothing or does not say.
func (r *Runner) Printing(ctx context.Context, device string) (int64, float64) {
	jobs, err := r.fleet.Jobs(ctx, deviceapi.JobsParams{ID: device})
	if err != nil || jobs.Current == nil {
		return 0, 0
	}

	return max(jobs.Current.TimeRemaining, 0), max(jobs.Current.Progress, 0)
}

// sourceOf is the file's path in its storage, and empty for a file already on
// the printer, which has no storage on this server.
func sourceOf(entry queue.Entry) string {
	if entry.OnPrinter() {
		return ""
	}

	return entry.Path
}

// SetReady sends the verb that says the bed is clear, or the one that takes
// it back, as the person who asked.
func (r *Runner) SetReady(ctx context.Context, device, by string, ready bool) (deviceapi.Result, error) {
	verb := VerbSetReady
	if !ready {
		verb = VerbCancelReady
	}

	result, err := r.fleet.Command(ctx, deviceapi.CommandParams{Principal: by, ID: device, Verb: verb})
	if err != nil {
		return deviceapi.Result{}, fmt.Errorf("%w: %w", ErrSendFailed, err)
	}

	if ready && result.OK {
		r.WakePrinter(device)
	}

	return result, nil
}

// writable answers the first storage of the printer a file can be written to,
// asked as principal. The printer is asked again each time, so a drive put in
// a moment ago counts.
func (r *Runner) writable(ctx context.Context, principal, device string) (string, error) {
	listing, err := r.fleet.Files(ctx, deviceapi.FilesParams{Principal: principal, ID: device})
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrPrinter, err)
	}

	for _, storage := range listing.Storages {
		if !storage.ReadOnly && storage.Name != "" {
			return storage.Name, nil
		}
	}

	return "", ErrNoStorage
}

// hold writes a note on a waiting entry. A failure to write it is logged: the
// entry still waits, only without the reason.
func (r *Runner) hold(ctx context.Context, device, id string, note queue.Note, detail string) {
	_, err := r.store.Update(ctx, device, func(q *queue.Queue) error {
		if !q.Hold(id, note, detail) {
			return queue.ErrUnchanged
		}

		return nil
	})
	if err != nil {
		r.log("simple-queue: the note on %s could not be written: %v\n", device, err)
	}
}

// notThere writes on an entry queued during its copy why it does not start.
// Only a printer that answered that it holds no such file says so: a printer
// that did not answer says nothing about the file.
//
// A copy the queue heard fail keeps that note. One the queue heard nothing
// of, after a restart for one, is asked about on each start until the send
// timeout, and then counts as failed too, so it is not asked about for ever.
func (r *Runner) notThere(ctx context.Context, device string, entry queue.Entry, err error) {
	if !errors.Is(err, ErrNoPrinterFile) || entry.Note == queue.NoteTransferFailed {
		return
	}

	if r.now().Sub(entry.AddedAt) > r.settings().SendTimeout {
		r.hold(ctx, device, entry.ID, queue.NoteTransferFailed, "")

		return
	}

	r.hold(ctx, device, entry.ID, queue.NoteNotArrived, "")
}

// claim marks a printer as having a start in progress, and answers false when
// one already is.
func (r *Runner) claim(device string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.starting[device] {
		return false
	}

	r.starting[device] = true

	return true
}

// release ends the start claim reserved.
func (r *Runner) release(device string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.starting, device)
}
