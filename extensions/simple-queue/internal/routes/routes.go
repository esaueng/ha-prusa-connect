// Package routes is the HTTP surface of this module.
//
// A module opens no HTTP port. The server receives the request, works out who
// is calling, and decides whether that person may call this module: a read
// takes view_resource on the instance, and anything else operate_resource.
//
// The queue of a printer is shown to a person who may see the printer, and
// changed by a person who may print on it. Both are asked of the server for
// each request, so a person who lost a printer loses its queue too.
package routes

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/prusa3d/prusa-connect/sdk/accessapi"
	"github.com/prusa3d/prusa-connect/sdk/deviceapi"
	"github.com/prusa3d/prusa-connect/sdk/routesapi"
	"github.com/prusa3d/prusa-connect/sdk/storageapi"

	"github.com/prusa3d/simple-queue/internal/queue"
	"github.com/prusa3d/simple-queue/internal/runner"
	"github.com/prusa3d/simple-queue/internal/settings"
)

// MaxBodyBytes is the largest JSON body a handler reads.
const MaxBodyBytes = 16 << 10

// Access asks the server about a person. `*accessapi.Client` satisfies it.
type Access interface {
	Check(ctx context.Context, params accessapi.CheckParams) (accessapi.Decision, error)
}

// Files reads a file in one of this server's storages. `*storageapi.Client`
// satisfies it.
type Files interface {
	Stat(ctx context.Context, params storageapi.StatParams) (storageapi.FileInfo, error)
}

// Service is what the routes read.
type Service struct {
	// Ready reports whether the host sent the settings.
	Ready    func() bool
	Settings func() settings.Values
	Store    *queue.Store
	Runner   *runner.Runner
	Access   Access
	Files    Files
	Now      func() time.Time
}

// View is one printer's queue as a person sees it.
type View struct {
	queue.Queue

	Printer runner.Printer `json:"printer"`
	// StartsAlone says the queue may start the next file by itself once the
	// printer is ready. Without it a person starts each file.
	StartsAlone bool `json:"starts_alone"`
	// MayChange says the person asking may print on the printer, so may change
	// its queue.
	MayChange bool   `json:"may_change"`
	Limits    Limits `json:"limits"`
	// RemainingSeconds is how long the printer says the print from the queue
	// still takes. It is absent when nothing from the queue prints, and when
	// the printer does not say.
	RemainingSeconds int64 `json:"remaining_seconds,omitempty"`
	// Progress is how far the printer says the print from the queue is, in
	// whole percent. It is absent when nothing from the queue prints, and when
	// the printer does not say.
	Progress float64 `json:"progress,omitempty"`
}

// Limits are the bounds the operator set, as a page shows them.
type Limits struct {
	MaxWaiting  int `json:"max_waiting"`
	KeepHistory int `json:"keep_history"`
}

// History is the ended prints of one printer's queue, newest first.
type History struct {
	History []queue.Entry `json:"history"`
}

// Summary is one printer's queue in the overview.
type Summary struct {
	Device  string         `json:"device_id"`
	Printer runner.Printer `json:"printer"`
	Waiting int            `json:"waiting"`
	// Active is the entry on its way to the printer or printing, if any.
	Active *queue.Entry `json:"active,omitempty"`
	// Next is the entry that prints next, if any.
	Next *queue.Entry `json:"next,omitempty"`
}

// Overview is every queue the person may see.
type Overview struct {
	Printers []Summary `json:"printers"`
}

// Board is every queue the person may see, whole, for a page that lists the
// files of many printers at once.
type Board struct {
	Queues []View `json:"queues"`
}

// New is the whole HTTP surface of this module. Every path is relative to the
// mount the host assigned.
func New(s Service) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /queues/summary", s.acting(s.overview))
	mux.HandleFunc("GET /queues", s.acting(s.board))
	mux.HandleFunc("GET /printers/{device}", s.acting(s.seeing(s.show)))
	mux.HandleFunc("POST /printers/{device}/entries", s.acting(s.changing(s.add)))
	mux.HandleFunc("DELETE /printers/{device}/entries/{entry}", s.acting(s.changing(s.remove)))
	mux.HandleFunc("POST /printers/{device}/entries/{entry}/move", s.acting(s.changing(s.move)))
	mux.HandleFunc("POST /printers/{device}/start", s.acting(s.changing(s.start)))
	mux.HandleFunc("POST /printers/{device}/ready", s.acting(s.changing(s.ready)))
	mux.HandleFunc("GET /printers/{device}/history", s.acting(s.seeing(s.history)))
	mux.HandleFunc("DELETE /printers/{device}/history", s.acting(s.changing(s.clearHistory)))

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		answer(w, http.StatusNotFound, map[string]string{"module_saw": r.URL.Path})
	})

	return mux
}

// handler is one route, with the person already read.
type handler func(w http.ResponseWriter, r *http.Request, by string)

// printerHandler is one route about one printer, with the printer read as the
// person.
type printerHandler func(w http.ResponseWriter, r *http.Request, by string, at printerAt)

// printerAt is the printer a route is about.
type printerAt struct {
	device  string
	printer runner.Printer
	// mayChange is whether the person may print on it.
	mayChange bool
}

// acting refuses a request the server attributed to nobody, and one that
// arrived before the settings.
func (s Service) acting(next handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		by := routesapi.PrincipalOf(r)
		if strings.TrimSpace(by) == "" {
			refuse(w, NoPrincipal, "the host named no person for this request")

			return
		}

		if !s.Ready() {
			refuse(w, NotReady, "the host has not sent the settings yet. Try again in a moment.")

			return
		}

		next(w, r, by)
	}
}

// seeing reads the printer as the person, and refuses a printer they may not
// see as one that does not exist.
func (s Service) seeing(next printerHandler) handler {
	return func(w http.ResponseWriter, r *http.Request, by string) {
		device := r.PathValue("device")

		printer, err := s.Runner.Look(r.Context(), by, device)
		if err != nil {
			refusePrinter(w, device, err)

			return
		}

		may, err := s.operates(r.Context(), by, device)
		if err != nil {
			refuse(w, AccessUnknown, AccessUnknown.Title+". Try again in a moment.")

			return
		}

		next(w, r, by, printerAt{device: device, printer: printer, mayChange: may})
	}
}

// changing is seeing, and also refuses a person who may not print on the
// printer.
func (s Service) changing(next printerHandler) handler {
	return s.seeing(func(w http.ResponseWriter, r *http.Request, by string, at printerAt) {
		if !at.mayChange {
			refuse(w, NotOperator, "changing the queue of a printer takes operate on that printer")

			return
		}

		next(w, r, by, at)
	})
}

// operates asks whether by may print on device.
func (s Service) operates(ctx context.Context, by, device string) (bool, error) {
	decision, err := s.Access.Check(ctx, accessapi.CheckParams{
		Subject: by, Action: runner.AskOperate, ObjectType: runner.ObjectDevice, ObjectID: device,
	})
	if err != nil {
		return false, err //nolint:wrapcheck // the caller answers ACCESS_UNKNOWN.
	}

	return decision.Allowed, nil
}

// view answers a printer's queue as the person sees it.
func (s Service) view(ctx context.Context, held queue.Queue, at printerAt) View {
	return s.viewAsking(ctx, held, at, true)
}

// viewAsking is view. alone says whether to ask the host if the queue may
// start by itself there, which costs one check.
func (s Service) viewAsking(ctx context.Context, held queue.Queue, at printerAt, alone bool) View {
	values := s.Settings()
	held.Device = at.device

	if held.Entries == nil {
		held.Entries = []queue.Entry{}
	}

	// The ended prints are read on their own, from the history route.
	held.History = nil

	out := View{
		Queue:       held,
		Printer:     at.printer,
		StartsAlone: alone && s.Runner.MayStartAlone(ctx, at.device),
		MayChange:   at.mayChange,
		Limits:      Limits{MaxWaiting: values.MaxWaiting, KeepHistory: values.KeepHistory},
	}

	// One question more of the printer, and only while a file of the queue
	// prints: it is what the page draws the end of the print from.
	if active := held.Active(); active != nil && active.State == queue.Printing {
		out.RemainingSeconds, out.Progress = s.Runner.Printing(ctx, at.device)
	}

	return out
}

// answerQueue answers a printer's queue after a change. A change that was
// written is answered even when the printer could not be read again, with
// what the route already knew of it.
func (s Service) answerQueue(w http.ResponseWriter, r *http.Request, status int, held queue.Queue, at printerAt) {
	if printer, err := s.Runner.Look(r.Context(), "", at.device); err == nil {
		at.printer = printer
	}

	answer(w, status, s.view(r.Context(), held, at))
}

// overview answers every printer the person sees, with its queue in short.
// Only a printer whose queue was ever written is read from the store, so a
// large fleet with few queues costs one listing and a few reads.
func (s Service) overview(w http.ResponseWriter, r *http.Request, by string) {
	seen, err := s.Runner.Visible(r.Context(), by)
	if err != nil {
		refuse(w, PrinterUnknown, PrinterUnknown.Title+". Try again in a moment.")

		return
	}

	kept, err := s.kept(r.Context())
	if err != nil {
		refuse(w, StoreFailed, StoreFailed.Title+". Try again in a moment.")

		return
	}

	out := Overview{Printers: make([]Summary, 0, len(seen))}

	for _, printer := range seen {
		summary := Summary{Device: printer.ID, Printer: printer.Printer}

		if kept[printer.ID] {
			held, unread := s.Store.Read(r.Context(), printer.ID)
			if unread != nil {
				refuse(w, StoreFailed, StoreFailed.Title+". Try again in a moment.")

				return
			}

			summary.Waiting, summary.Active, summary.Next = held.WaitingCount(), held.Active(), held.Next()
		}

		out.Printers = append(out.Printers, summary)
	}

	answer(w, http.StatusOK, out)
}

// kept is every printer whose queue was ever written.
func (s Service) kept(ctx context.Context) (map[string]bool, error) {
	devices, err := s.Store.Devices(ctx)
	if err != nil {
		return nil, err //nolint:wrapcheck // the caller answers STORE_FAILED.
	}

	kept := make(map[string]bool, len(devices))
	for _, device := range devices {
		kept[device] = true
	}

	return kept, nil
}

// board answers every queue the person sees, whole, in one answer, so a page
// with many printers reads the server once and not once for each printer.
func (s Service) board(w http.ResponseWriter, r *http.Request, by string) {
	ctx := r.Context()

	seen, err := s.Runner.Visible(ctx, by)
	if err != nil {
		refuse(w, PrinterUnknown, PrinterUnknown.Title+". Try again in a moment.")

		return
	}

	kept, err := s.kept(ctx)
	if err != nil {
		refuse(w, StoreFailed, StoreFailed.Title+". Try again in a moment.")

		return
	}

	out := Board{Queues: make([]View, 0, len(seen))}

	for _, printer := range seen {
		var held queue.Queue

		if kept[printer.ID] {
			if held, err = s.Store.Read(ctx, printer.ID); err != nil {
				refuse(w, StoreFailed, StoreFailed.Title+". Try again in a moment.")

				return
			}
		}

		may, opErr := s.operates(ctx, by, printer.ID)
		if opErr != nil {
			refuse(w, AccessUnknown, AccessUnknown.Title+". Try again in a moment.")

			return
		}

		at := printerAt{device: printer.ID, printer: printer.Printer, mayChange: may}
		// The overview reads starts_alone only for a queue with a file
		// waiting, so an empty queue costs no check.
		out.Queues = append(out.Queues, s.viewAsking(ctx, held, at, held.Next() != nil))
	}

	answer(w, http.StatusOK, out)
}

func (s Service) show(w http.ResponseWriter, r *http.Request, by string, at printerAt) {
	held, err := s.Store.Read(r.Context(), at.device)
	if err != nil {
		refuse(w, StoreFailed, StoreFailed.Title+". Try again in a moment.")

		return
	}

	// What files on the printer say about themselves is read as a person who
	// may print there, since it has the printer read a file.
	if at.mayChange {
		held = s.Runner.Fill(r.Context(), by, at.device, held)
	}

	view := s.view(r.Context(), held, at)

	// The switch that lets the queue start alone is a grant the page writes
	// to the host, which tells the module nothing. A page reads the queue
	// again right after, so a ready printer with a file waiting that the
	// queue may now start is looked at now rather than on the next sweep.
	if view.StartsAlone && at.printer.Ready &&
		held.Active() == nil && held.Next() != nil {
		s.Runner.WakePrinter(at.device)
	}

	answer(w, http.StatusOK, view)
}

// history answers the ended prints of one printer's queue, newest first.
func (s Service) history(w http.ResponseWriter, r *http.Request, _ string, at printerAt) {
	held, err := s.Store.Read(r.Context(), at.device)
	if err != nil {
		refuse(w, StoreFailed, StoreFailed.Title+". Try again in a moment.")

		return
	}

	ended := held.History
	if ended == nil {
		ended = []queue.Entry{}
	}

	answer(w, http.StatusOK, History{History: ended})
}

// maxPathBytes bounds the path of a queued file.
const maxPathBytes = 1024

// queuedPath is the file a body names, without the slashes at its ends, or
// why the body names none. A file on its way to the printer is not looked
// for, so a path no printer or storage would hold is refused here.
func queuedPath(raw string) (file, wrong string) {
	file = strings.Trim(raw, "/")

	switch {
	case file == "":
		return "", "path names the file to queue, in storage or on the printer"
	case len(file) > maxPathBytes || slices.Contains(strings.Split(file, "/"), ".."):
		return "", "path is longer than 1024 bytes or climbs out of its folder with .."
	}

	return file, ""
}

// addBody is the body of the add route.
type addBody struct {
	// Storage names a storage of this server, and Path the file in it. With
	// no Storage, Path is a file already on the printer, such as
	// usb/bracket.bgcode.
	Storage string `json:"storage"`
	Path    string `json:"path"`
	// Position is where the file goes among the waiting files, 0 being the
	// next one. Absent is the end.
	Position *int `json:"position,omitempty"`
	// Arriving says the file is being copied to Path on the printer right
	// now, so it is taken without looking for it there. It goes with no
	// Storage. The queue looks for the file when its turn comes.
	Arriving bool `json:"arriving,omitempty"`
}

// add puts a file on the queue. When the printer is ready and prints nothing
// from the queue, the file starts at once, as the person who added it.
func (s Service) add(w http.ResponseWriter, r *http.Request, by string, at printerAt) {
	// Before it starts a file the queue asks whether the owner may still
	// print, and the server answers that only about a person.
	if !strings.HasPrefix(by, "user:") {
		refuse(w, NotAPerson, "a queue takes files from people only. A service account can print directly.")

		return
	}

	var body addBody
	if !readJSON(w, r, &body) {
		return
	}

	file, wrong := queuedPath(body.Path)
	if wrong != "" {
		refuse(w, Invalid, wrong)

		return
	}

	if !isPrintable(file) {
		refuse(w, NotPrintable, path.Base(file)+" is not a print file. A queue holds "+
			".bgcode, .gcode, .gco and .g files.")

		return
	}

	// What the file says about itself is read while the file is checked, from
	// the storage or from the printer. A file still on its way says nothing yet.
	var (
		facts queue.Facts
		ok    bool
	)

	switch {
	case body.Storage != "" && body.Arriving:
		refuse(w, Invalid, "arriving is a file on its way to the printer, so name no storage")

		return
	case body.Storage != "":
		if facts, ok = s.mayRead(r.Context(), w, by, body.Storage, file); !ok {
			return
		}
	case !body.Arriving:
		if facts, ok = s.onPrinter(r.Context(), w, by, at.device, file); !ok {
			return
		}
	}

	id, err := queue.NewID()
	if err != nil {
		refuse(w, StoreFailed, StoreFailed.Title+". Try again in a moment.")

		return
	}

	values := s.Settings()
	entry := queue.Entry{
		ID: id, Storage: body.Storage, Path: file, Name: path.Base(file),
		AddedBy: by, AddedAt: s.Now(), Arriving: body.Arriving, Facts: facts,
	}

	held, err := s.Store.Update(r.Context(), at.device, func(q *queue.Queue) error {
		if full := q.Add(entry, values.MaxWaiting); full != nil {
			return full //nolint:wrapcheck // the queue's own refusal.
		}

		if body.Position != nil {
			return q.Move(entry.ID, *body.Position) //nolint:wrapcheck // the queue's own refusal.
		}

		return nil
	})
	if err != nil {
		refuseQueue(w, err)

		return
	}

	// A file still on its way is not started here: it is not on the printer
	// yet, and the start would only write a note on it.
	if next := held.Next(); held.Active() == nil && next != nil && next.ID == entry.ID && !entry.Arriving &&
		at.printer.Ready {
		// The file is queued whatever the start answers. A refusal is written
		// on the entry, which the answer below carries. A start runs to its
		// end when the browser leaves, so no entry is left half sent.
		_, _ = s.Runner.Start(context.WithoutCancel(r.Context()), at.device, by, entry.ID)

		if fresh, unread := s.Store.Read(r.Context(), at.device); unread == nil {
			held = fresh
		}
	}

	s.answerQueue(w, r, http.StatusCreated, held, at)
}

// mayRead refuses a file the person may not read or that is not there. It
// answers false when it refused, and with a file how long the slicer said it
// prints, where the file says.
func (s Service) mayRead(
	ctx context.Context, w http.ResponseWriter, by, storage, file string,
) (queue.Facts, bool) {
	decision, err := s.Access.Check(ctx, accessapi.CheckParams{
		Subject: by, Action: runner.AskReadStorage, ObjectType: runner.ObjectKind, ObjectID: runner.KindStorage,
	})

	switch {
	case err != nil:
		refuse(w, AccessUnknown, AccessUnknown.Title+". Try again in a moment.")

		return queue.Facts{}, false
	case !decision.Allowed:
		refuse(w, FileDenied, "queueing a file takes read_storage on storages")

		return queue.Facts{}, false
	}

	info, err := s.Files.Stat(ctx, storageapi.StatParams{Principal: by, Storage: storage, Path: file})

	switch {
	case errors.Is(err, storageapi.ErrDenied):
		refuse(w, FileDenied, "you may not read "+storage)

		return queue.Facts{}, false
	case errors.Is(err, storageapi.ErrNoFile), errors.Is(err, storageapi.ErrUnreadable):
		refuse(w, FileNotFound, "there is no file "+file+" in "+storage)

		return queue.Facts{}, false
	case err != nil:
		refuse(w, AccessUnknown, "the storage did not answer. Try again in a moment.")

		return queue.Facts{}, false
	case info.IsDir:
		refuse(w, NotPrintable, file+" is a folder")

		return queue.Facts{}, false
	}

	facts := queue.Facts{Size: info.Size}
	if info.Print != nil {
		facts.EstimatedSeconds = info.Print.EstimatedSeconds
		facts.PrinterModel = info.Print.PrinterModel
		facts.LayerHeightMM = info.Print.LayerHeightMM
		facts.Material = info.Print.Material
	}

	return facts, true
}

// onPrinter refuses a path the printer holds no file at, and answers with a
// file how long the slicer said it prints. It answers false when
// it refused.
func (s Service) onPrinter(
	ctx context.Context, w http.ResponseWriter, by, device, file string,
) (queue.Facts, bool) {
	held, err := s.Runner.Listed(ctx, by, device, file)

	switch {
	case errors.Is(err, runner.ErrNoPrinterFile):
		refuse(w, FileNotFound, "the printer holds no file "+file)

		return queue.Facts{}, false
	case errors.Is(err, runner.ErrPrinterFolder):
		refuse(w, NotPrintable, file+" is a folder")

		return queue.Facts{}, false
	case err != nil:
		refuse(w, PrinterUnknown, "the printer did not answer. Try again in a moment.")

		return queue.Facts{}, false
	}

	// The listing of a folder names no slicer's facts. What the server holds
	// about the file itself adds them, at once, when it holds anything. The
	// size and the time stay the listing's, which name the version the
	// file's picture is kept under.
	facts := runner.FactsOf(held)
	if about, ok := s.Runner.HeldFile(ctx, by, device, file); ok {
		facts = runner.FactsOf(about)
		facts.Size, facts.Modified = held.Size, held.Modified
	}

	return facts, true
}

func (s Service) remove(w http.ResponseWriter, r *http.Request, _ string, at printerAt) {
	id := r.PathValue("entry")

	held, err := s.Store.Update(r.Context(), at.device, func(q *queue.Queue) error {
		return q.Remove(id) //nolint:wrapcheck // the queue's own refusal.
	})
	if err != nil {
		refuseQueue(w, err)

		return
	}

	s.answerQueue(w, r, http.StatusOK, held, at)
}

// moveBody is the body of the move route.
type moveBody struct {
	// Position is the new place among the waiting files, 0 being the next one.
	Position int `json:"position"`
}

func (s Service) move(w http.ResponseWriter, r *http.Request, _ string, at printerAt) {
	var body moveBody
	if !readJSON(w, r, &body) {
		return
	}

	id := r.PathValue("entry")

	held, err := s.Store.Update(r.Context(), at.device, func(q *queue.Queue) error {
		return q.Move(id, body.Position) //nolint:wrapcheck // the queue's own refusal.
	})
	if err != nil {
		refuseQueue(w, err)

		return
	}

	s.answerQueue(w, r, http.StatusOK, held, at)
}

// start sends the next file now, as the person.
func (s Service) start(w http.ResponseWriter, r *http.Request, by string, at printerAt) {
	// A start runs to its end when the browser leaves, so no entry is left
	// half sent.
	held, err := s.Runner.Start(context.WithoutCancel(r.Context()), at.device, by, "")
	if err != nil {
		refuseQueue(w, err)

		return
	}

	s.answerQueue(w, r, http.StatusOK, held, at)
}

// readyBody is the body of the ready route.
type readyBody struct {
	// Ready true says the bed is clear. False takes that back.
	Ready bool `json:"ready"`
}

// ready marks the printer as ready for the next print, or takes that back.
// A queue with a file waiting starts it as soon as the printer reports it is
// ready.
func (s Service) ready(w http.ResponseWriter, r *http.Request, by string, at printerAt) {
	var body readyBody
	if !readJSON(w, r, &body) {
		return
	}

	if !at.printer.CanBeReady {
		refuse(w, ReadyUnsupported, "the printer does not take the command that marks it as ready")

		return
	}

	result, err := s.Runner.SetReady(r.Context(), at.device, by, body.Ready)
	if err != nil {
		refuseQueue(w, err)

		return
	}

	if !result.OK {
		detail := result.MachineReason
		if detail == "" {
			detail = "the printer refused the command"
		}

		refuse(w, SendFailed, detail)

		return
	}

	held, err := s.Store.Read(r.Context(), at.device)
	if err != nil {
		refuse(w, StoreFailed, StoreFailed.Title+". Try again in a moment.")

		return
	}

	s.answerQueue(w, r, http.StatusOK, held, at)
}

func (s Service) clearHistory(w http.ResponseWriter, r *http.Request, _ string, at printerAt) {
	held, err := s.Store.Update(r.Context(), at.device, func(q *queue.Queue) error {
		if len(q.History) == 0 {
			return queue.ErrUnchanged
		}

		q.ClearHistory()

		return nil
	})
	if err != nil {
		refuseQueue(w, err)

		return
	}

	s.answerQueue(w, r, http.StatusOK, held, at)
}

// refusePrinter answers a printer the server would not read for the person.
func refusePrinter(w http.ResponseWriter, device string, err error) {
	if errors.Is(err, deviceapi.ErrNoDevice) || errors.Is(err, deviceapi.ErrDenied) {
		refuse(w, PrinterNotFound, "there is no printer "+device)

		return
	}

	refuse(w, PrinterUnknown, PrinterUnknown.Title+". Try again in a moment.")
}

// refuseQueue answers a change the queue, the store or the printer refused.
func refuseQueue(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, queue.ErrFull):
		refuse(w, QueueFull, err.Error())
	case errors.Is(err, queue.ErrEmpty):
		refuse(w, QueueEmpty, "add a file to the queue first")
	case errors.Is(err, queue.ErrNoEntry):
		refuse(w, EntryNotFound, "the queue holds no such entry, or it moved on")
	case errors.Is(err, queue.ErrActive):
		refuse(w, EntryActive, "stop the print on the printer first")
	case errors.Is(err, queue.ErrBusy):
		refuse(w, PrinterBusy, "wait until the printer is done with the file it has, then try again")
	case errors.Is(err, runner.ErrNotFree):
		refuse(w, PrinterNotFree, strings.TrimPrefix(err.Error(), runner.ErrNotFree.Error()+": ")+
			". Start the file once the printer is done.")
	case errors.Is(err, runner.ErrNoStorage):
		refuse(w, NoStorage, "put a USB drive into the printer and try again")
	case errors.Is(err, runner.ErrNoPrinterFile):
		refuse(w, FileNotFound, "the file has not reached the printer. Wait for the copy to end, then try again")
	case errors.Is(err, runner.ErrSendFailed):
		refuse(w, SendFailed, strings.TrimPrefix(err.Error(), runner.ErrSendFailed.Error()+": "))
	case errors.Is(err, runner.ErrPrinter):
		refuse(w, PrinterUnknown, PrinterUnknown.Title+". Try again in a moment.")
	default:
		refuse(w, StoreFailed, StoreFailed.Title+". Try again in a moment.")
	}
}

// isPrintable reports whether a file name ends like a print file. A queue
// holds print files only, so a project or a model put on it by mistake is
// refused at once and not when its turn comes.
func isPrintable(file string) bool {
	name := path.Base(file)

	return storageapi.IsPrintableExtension(name) && name != path.Ext(name)
}

// readJSON reads a bounded JSON body into into, and refuses one it cannot.
func readJSON(w http.ResponseWriter, r *http.Request, into any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, MaxBodyBytes))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(into); err != nil {
		if tooLarge := (*http.MaxBytesError)(nil); errors.As(err, &tooLarge) {
			refuse(w, BodyTooLarge, "a body is at most "+strconv.Itoa(MaxBodyBytes)+" bytes")

			return false
		}

		refuse(w, Invalid, "the body is not the JSON this route reads: "+err.Error())

		return false
	}

	return true
}
