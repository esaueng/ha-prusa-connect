// Package hostfake is the host this module talks to, held in memory for the
// tests: the record store, the printers, the questions about people, the
// storages and the event stream.
//
// A printer here does what the queue needs from one and nothing more. A print
// sent to it is recorded and the printer starts printing the file, unless the
// test made it refuse. Its state changes only when a test says so.
package hostfake

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prusa3d/prusa-connect/sdk/accessapi"
	"github.com/prusa3d/prusa-connect/sdk/busapi"
	"github.com/prusa3d/prusa-connect/sdk/connectorapi"
	"github.com/prusa3d/prusa-connect/sdk/deviceapi"
	"github.com/prusa3d/prusa-connect/sdk/storageapi"

	"github.com/prusa3d/simple-queue/internal/queue"
)

// windowBuffer is how many windows Emit writes before a reader takes one, and
// fileBytes is the size a file of a storage reports until a test sets it.
const (
	windowBuffer = 16
	fileBytes    = 1024
)

// Self is the subject the host answers for when the module asks as itself.
const Self = ""

// Printer is one printer as a test arranges it.
type Printer struct {
	Name       string
	Model      string
	State      string
	Connected  bool
	CanBeReady bool
	// Accepts and Ready are what the server says of the printer's state. A
	// printer with Accepts nil is answered as a Prusa printer in State.
	Accepts []string
	Ready   bool
	// Silent is a printer whose connector declares no accepted verbs.
	Silent bool
	// Storages is the storages the printer reports.
	Storages []deviceapi.Storage
	// Files is the files on the printer, by the storage-prefixed path the
	// printer reports, such as usb/bracket.bgcode.
	Files []deviceapi.File
	// Refuse makes every print fail with this error.
	Refuse error
	// Holds are the paths the printer already holds a file at. A send from a
	// storage onto one of them is deviceapi.ErrNameTaken.
	Holds map[string]bool
	Jobs  deviceapi.Jobs
}

// Sent is one print the queue asked for.
type Sent struct {
	Principal string
	Device    string
	Storage   string
	Source    string
	Path      string
}

// Host is the whole fake.
type Host struct {
	mu       sync.Mutex
	printers map[string]*Printer
	order    []string
	grants   map[string]bool
	served   map[string]bool
	noPeople bool
	files    map[string]storageapi.FileInfo
	sent     []Sent
	commands []deviceapi.CommandParams
	windows  chan busapi.Window
	serial   int
	// looks counts the reads of one printer.
	looks    int
	fileAsks int
	checks   int
	// fileInfo, when set, holds a question about one file until it closes.
	fileInfo chan struct{}
	// read holds the files a cached ask has had read, by printer and path.
	read map[string]bool
}

// New answers an empty host.
func New() *Host {
	return &Host{
		printers: map[string]*Printer{},
		grants:   map[string]bool{},
		served:   map[string]bool{},
		files:    map[string]storageapi.FileInfo{},
		read:     map[string]bool{},
		windows:  make(chan busapi.Window, windowBuffer),
	}
}

// AddPrinter puts a printer on the host.
func (h *Host) AddPrinter(id string, printer Printer) {
	h.mu.Lock()
	defer h.mu.Unlock()

	held := printer
	if _, known := h.printers[id]; !known {
		h.order = append(h.order, id)
	}

	h.printers[id] = &held
}

// SetState changes what a printer reports.
func (h *Host) SetState(id, state string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.printers[id].State = state
}

// Says makes a printer one whose connector uses words of its own: it reports
// state, takes accepts in it, and is ready or not.
func (h *Host) Says(id, state string, ready bool, accepts ...string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	printer := h.printers[id]
	printer.State, printer.Ready, printer.Accepts = state, ready, append([]string{}, accepts...)
}

// Silent makes a printer one whose connector declares no accepted verbs: it
// reports state, and is ready or not.
func (h *Host) Silent(id, state string, ready bool) {
	h.mu.Lock()
	defer h.mu.Unlock()

	printer := h.printers[id]
	printer.Silent, printer.State, printer.Ready = true, state, ready
}

// SetJobs changes the prints a printer reports.
func (h *Host) SetJobs(id string, jobs deviceapi.Jobs) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.printers[id].Jobs = jobs
}

// PutPrinterFile puts a file on the printer at path, as a copy that landed. A
// file still arriving there lands.
func (h *Host) PutPrinterFile(id, path string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	for index := range h.printers[id].Files {
		if h.printers[id].Files[index].Path == path {
			h.printers[id].Files[index].ReadOnly = false

			return
		}
	}

	h.printers[id].Files = append(h.printers[id].Files, deviceapi.File{
		Path: path, Name: path[strings.LastIndex(path, "/")+1:],
	})
}

// PutArrivingFile puts a file on the printer at path that is still being
// copied, as a Buddy printer lists one: read-only, at its full length.
// PutPrinterFile on the same path then lands it.
func (h *Host) PutArrivingFile(id, path string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.printers[id].Files = append(h.printers[id].Files, deviceapi.File{
		Path: path, Name: path[strings.LastIndex(path, "/")+1:], ReadOnly: true,
	})
}

// SetPrinterFileFacts says what a file the printer holds says about itself,
// as the printer reads it out of the file.
func (h *Host) SetPrinterFileFacts(id, path string, facts queue.Facts) {
	h.mu.Lock()
	defer h.mu.Unlock()

	for index := range h.printers[id].Files {
		file := &h.printers[id].Files[index]
		if file.Path == path {
			file.EstimatedSeconds, file.PrinterModel = facts.EstimatedSeconds, facts.PrinterModel
			file.LayerHeightMM, file.Material, file.Size = facts.LayerHeightMM, facts.Material, facts.Size
			file.Modified = facts.Modified
		}
	}
}

// SetFileFacts says what a file in a storage says about itself, as the
// server reads it out of the file.
func (h *Host) SetFileFacts(storage, path string, facts queue.Facts) {
	h.mu.Lock()
	defer h.mu.Unlock()

	info := h.files[storage+":"+path]
	info.Size = facts.Size
	info.Print = &storageapi.PrintInfo{
		EstimatedSeconds: facts.EstimatedSeconds, PrinterModel: facts.PrinterModel,
		LayerHeightMM: facts.LayerHeightMM, Material: facts.Material,
	}
	h.files[storage+":"+path] = info
}

// SetHolds says the printer already holds a file at path.
func (h *Host) SetHolds(id, path string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.printers[id].Holds == nil {
		h.printers[id].Holds = map[string]bool{}
	}

	h.printers[id].Holds[path] = true
}

// SetRefuse makes a printer refuse every print with err, or take them again
// with nil.
func (h *Host) SetRefuse(id string, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.printers[id].Refuse = err
}

// Grant lets subject take action on the object.
func (h *Host) Grant(subject, action, objectType, objectID string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.grants[grantKey(subject, action, objectType, objectID)] = true
}

// Revoke takes a grant back.
func (h *Host) Revoke(subject, action, objectType, objectID string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	delete(h.grants, grantKey(subject, action, objectType, objectID))
}

// NoPeople makes this a server that keeps no list of people, as one with an
// OpenID Connect login or in open mode is. It answers about each of served
// from the grants, as the host does for a person the module served since it
// started, and ErrUnknownSubject about anybody else.
func (h *Host) NoPeople(served ...string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.noPeople = true
	for _, subject := range served {
		h.served[subject] = true
	}
}

// AddFile puts a file into a storage of this server.
func (h *Host) AddFile(storage, path string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	name := path[strings.LastIndex(path, "/")+1:]
	h.files[storage+":"+path] = storageapi.FileInfo{Name: name, Path: path, Size: fileBytes}
}

// Sent answers the prints the queue asked for, in order.
func (h *Host) Sent() []Sent {
	h.mu.Lock()
	defer h.mu.Unlock()

	return slices.Clone(h.sent)
}

// Commands answers the commands the queue sent, in order.
func (h *Host) Commands() []deviceapi.CommandParams {
	h.mu.Lock()
	defer h.mu.Unlock()

	return slices.Clone(h.commands)
}

// Emit hands one event about a printer to the stream.
func (h *Host) Emit(device, kind string, data any) {
	encoded, err := json.Marshal(data)
	if err != nil {
		panic(err)
	}

	h.mu.Lock()
	h.serial++
	serial := h.serial
	h.mu.Unlock()

	envelope, err := json.Marshal(map[string]any{
		"specversion": "1.0",
		"id":          strconv.Itoa(serial),
		"source":      "test",
		"type":        kind,
		"time":        time.Now(),
		"subject":     "device:" + device,
		"hops":        0,
		"evclass":     "durable",
		"data":        json.RawMessage(encoded),
	})
	if err != nil {
		panic(err)
	}

	h.windows <- busapi.Window{Events: []json.RawMessage{envelope}, Cursor: strconv.Itoa(serial)}
}

// Resync tells the stream continuity was lost.
func (h *Host) Resync() {
	h.windows <- busapi.Window{Resync: true}
}

// Subscribe is the event stream. It hands over what Emit wrote until ctx
// ends.
func (h *Host) Subscribe(ctx context.Context, _ busapi.SubscribeParams, onWindow func(busapi.Window) error) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err() //nolint:wrapcheck // the caller's own context.
		case window := <-h.windows:
			if err := onWindow(window); err != nil {
				return err
			}
		}
	}
}

// Check is accessapi's Check.
func (h *Host) Check(_ context.Context, params accessapi.CheckParams) (accessapi.Decision, error) {
	h.mu.Lock()
	h.checks++
	h.mu.Unlock()

	h.mu.Lock()
	defer h.mu.Unlock()

	if h.noPeople && params.Subject != "" && !h.served[params.Subject] {
		return accessapi.Decision{}, accessapi.ErrUnknownSubject
	}

	return accessapi.Decision{
		Allowed: h.grants[grantKey(params.Subject, params.Action, params.ObjectType, params.ObjectID)],
	}, nil
}

// Stat is storageapi's Stat, for a person who may read every storage.
func (h *Host) Stat(_ context.Context, params storageapi.StatParams) (storageapi.FileInfo, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	info, ok := h.files[params.Storage+":"+params.Path]
	if !ok {
		return storageapi.FileInfo{}, storageapi.ErrNoFile
	}

	return info, nil
}

// device answers a printer the principal may see.
func (h *Host) device(principal, id string) (*Printer, error) {
	printer, ok := h.printers[id]
	if !ok || !h.grants[grantKey(principal, "observe", "device", id)] {
		return nil, deviceapi.ErrNoDevice
	}

	return printer, nil
}

// Fleet answers the part of the host that is deviceapi. It is a type of its
// own because deviceapi and stateapi both name a method Get.
func (h *Host) Fleet() *Fleet { return &Fleet{host: h} }

// Fleet is deviceapi's half of the host.
type Fleet struct {
	host *Host
}

// List is deviceapi's List, in the order the printers were added.
func (f *Fleet) List(_ context.Context, params deviceapi.ListParams) (deviceapi.Page, error) {
	f.host.mu.Lock()
	defer f.host.mu.Unlock()

	var seen []deviceapi.Device

	for _, id := range f.host.order {
		printer, err := f.host.device(params.Principal, id)
		if err == nil {
			seen = append(seen, deviceOf(id, printer))
		}
	}

	page := deviceapi.Page{Total: len(seen), Limit: params.Limit, Offset: params.Offset}
	if params.Offset < len(seen) {
		page.Items = seen[params.Offset:min(len(seen), params.Offset+params.Limit)]
	}

	return page, nil
}

// Get is deviceapi's Get.
func (f *Fleet) Get(_ context.Context, params deviceapi.GetParams) (deviceapi.Device, error) {
	f.host.mu.Lock()
	defer f.host.mu.Unlock()

	f.host.looks++

	printer, err := f.host.device(params.Principal, params.ID)
	if err != nil {
		return deviceapi.Device{}, err
	}

	return deviceOf(params.ID, printer), nil
}

// FileAsks counts the questions about a printer's files, and Checks the
// questions about access.
func (h *Host) FileAsks() int {
	h.mu.Lock()
	defer h.mu.Unlock()

	return h.fileAsks
}

// Checks counts the questions about access.
func (h *Host) Checks() int {
	h.mu.Lock()
	defer h.mu.Unlock()

	return h.checks
}

// Looks counts the reads of one printer, so a test knows a sweep has read
// the printer's state.
func (h *Host) Looks() int {
	h.mu.Lock()
	defer h.mu.Unlock()

	return h.looks
}

// deviceOf is a printer as deviceapi answers it.
func deviceOf(id string, printer *Printer) deviceapi.Device {
	state, _ := json.Marshal(printer.State)
	out := deviceapi.Device{
		Kind: "printer", ID: id, Name: printer.Name, Model: printer.Model, Connected: printer.Connected,
		State: map[string]json.RawMessage{"/state": state},
	}

	if printer.CanBeReady {
		out.Capabilities = []string{string(connectorapi.VerbSetPrinterReady), "core.command.cancel_printer_ready"}
	}

	switch {
	case printer.Silent:
		out.Ready = printer.Ready
	case printer.Accepts != nil:
		out.Accepts, out.Ready = printer.Accepts, printer.Ready
	default:
		out.Accepts = []string{}
		if printer.Connected && slices.Contains([]string{"IDLE", "READY", "FINISHED", "STOPPED"}, printer.State) {
			out.Accepts = []string{"core.command.start_print"}
		}

		out.Ready = printer.Connected && printer.State == "READY"
	}

	return out
}

// Jobs is deviceapi's Jobs.
func (f *Fleet) Jobs(_ context.Context, params deviceapi.JobsParams) (deviceapi.Jobs, error) {
	f.host.mu.Lock()
	defer f.host.mu.Unlock()

	printer, err := f.host.device(params.Principal, params.ID)
	if err != nil {
		return deviceapi.Jobs{}, err
	}

	return printer.Jobs, nil
}

// Files is deviceapi's Files: the storages at the root, the entries of a
// storage or a folder, and one file. A folder is answered at once, as the
// core answers it from what it last read. A question about one file waits
// while HoldFileInfo holds it, as a printer takes seconds to answer one.
func (f *Fleet) Files(ctx context.Context, params deviceapi.FilesParams) (deviceapi.Listing, error) {
	f.host.mu.Lock()
	f.host.fileAsks++
	printer, err := f.host.device(params.Principal, params.ID)
	if err != nil {
		f.host.mu.Unlock()

		return deviceapi.Listing{}, err
	}

	if params.Path == "" {
		defer f.host.mu.Unlock()

		return deviceapi.Listing{Storages: slices.Clone(printer.Storages)}, nil
	}

	// Any path but the root is asked of the printer, which takes operate, as
	// the core holds it.
	if !f.host.grants[grantKey(params.Principal, "operate", "device", params.ID)] {
		f.host.mu.Unlock()

		return deviceapi.Listing{}, ErrNotOperator
	}

	folder := slices.ContainsFunc(printer.Storages, func(s deviceapi.Storage) bool { return s.Name == params.Path })
	var found *deviceapi.File
	for _, file := range printer.Files {
		if file.Path == params.Path {
			found = &file
			folder = folder || file.Folder
		}
	}

	if folder {
		defer f.host.mu.Unlock()
		listing := deviceapi.Listing{Entry: &deviceapi.File{Path: params.Path, Folder: true}}
		for _, file := range printer.Files {
			// A listing of a folder names no slicer's facts: only the
			// answer about one file carries them.
			if path.Dir(file.Path) == params.Path {
				listing.Items = append(listing.Items, deviceapi.File{
					Path: file.Path, Name: file.Name, DisplayName: file.DisplayName,
					Folder: file.Folder, Size: file.Size, Modified: file.Modified,
				})
			}
		}
		listing.Total = len(listing.Items)

		return listing, nil
	}

	if params.Cached {
		defer f.host.mu.Unlock()

		// The server holds nothing of a path the printer has no file at, and
		// answers that with no entry and no error.
		if found == nil {
			return deviceapi.Listing{}, nil
		}

		return f.host.cached(params.ID, params.Path, found), nil
	}

	gate := f.host.fileInfo
	f.host.mu.Unlock()

	if found == nil {
		return deviceapi.Listing{}, deviceapi.ErrNoFile
	}

	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return deviceapi.Listing{}, ctx.Err()
		}
	}

	return deviceapi.Listing{Entry: aboutOne(found)}, nil
}

// aboutOne is a file as the printer answers a question about it alone: with
// what the slicer wrote, and without the time its folder's listing names, as
// a Prusa printer answers.
func aboutOne(file *deviceapi.File) *deviceapi.File {
	one := *file
	one.Modified = 0

	return &one
}

// cached answers a cached ask about one file at once. The first one finds
// nothing read yet and starts the read, as the core does, and the next finds
// it. The caller holds the lock.
func (h *Host) cached(device, path string, found *deviceapi.File) deviceapi.Listing {
	key := device + ":" + path
	if !h.read[key] {
		h.read[key] = true

		return deviceapi.Listing{}
	}

	return deviceapi.Listing{Entry: aboutOne(found)}
}

// Read makes the server hold an answer about one file on a printer, as when
// a page showed the file's picture.
func (h *Host) Read(device, path string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.read[device+":"+path] = true
}

// HoldFileInfo makes a question about one file on a printer wait until the
// answer it gives is closed.
func (h *Host) HoldFileInfo() (release func()) {
	h.mu.Lock()
	defer h.mu.Unlock()

	gate := make(chan struct{})
	h.fileInfo = gate

	return func() { close(gate) }
}

// ErrNotOperator is a print or a command the principal may not send.
var ErrNotOperator = errors.New("hostfake: the principal may not operate the printer")

// Print is deviceapi's Print. The printer starts printing the file at once.
func (f *Fleet) Print(_ context.Context, params deviceapi.PrintParams) (deviceapi.Started, error) {
	f.host.mu.Lock()
	defer f.host.mu.Unlock()

	printer, err := f.host.device(params.Principal, params.ID)
	if err != nil {
		return deviceapi.Started{}, err
	}

	if !f.host.grants[grantKey(params.Principal, "operate", "device", params.ID)] {
		return deviceapi.Started{}, ErrNotOperator
	}

	if printer.Refuse != nil {
		return deviceapi.Started{}, printer.Refuse
	}

	at := params.Path

	if params.Storage != "" && printer.Holds[at] {
		if !params.KeepBoth {
			return deviceapi.Started{}, deviceapi.ErrNameTaken
		}

		// The host's free name: the first number the printer holds nothing at.
		extension := path.Ext(at)
		for n := 1; printer.Holds[at]; n++ {
			at = fmt.Sprintf("%s[%d]%s", strings.TrimSuffix(params.Path, extension), n, extension)
		}
	}

	f.host.serial++
	f.host.sent = append(f.host.sent, Sent{
		Principal: params.Principal, Device: params.ID, Storage: params.Storage,
		Source: params.Source, Path: at,
	})

	// One of the two ids, as the host answers: a file sent from a storage has
	// a transfer and no command yet, because the start is sent once the file
	// has landed. A file the printer already holds has the command at once.
	if params.Storage != "" {
		return deviceapi.Started{TransferID: fmt.Sprintf("transfer-%d", f.host.serial), Path: at}, nil
	}

	return deviceapi.Started{CommandID: fmt.Sprintf("command-%d", f.host.serial), Path: at}, nil
}

// Command is deviceapi's Command. The ready verbs change the printer's state.
func (f *Fleet) Command(_ context.Context, params deviceapi.CommandParams) (deviceapi.Result, error) {
	f.host.mu.Lock()
	defer f.host.mu.Unlock()

	printer, err := f.host.device(params.Principal, params.ID)
	if err != nil {
		return deviceapi.Result{}, err
	}

	if !f.host.grants[grantKey(params.Principal, "operate", "device", params.ID)] {
		return deviceapi.Result{}, ErrNotOperator
	}

	f.host.commands = append(f.host.commands, params)

	switch params.Verb {
	case string(connectorapi.VerbSetPrinterReady):
		printer.State = "READY"
	case "core.command.cancel_printer_ready":
		printer.State = "IDLE"
	}

	return deviceapi.Result{OK: true}, nil
}

func grantKey(subject, action, objectType, objectID string) string {
	return subject + "|" + action + "|" + objectType + "|" + objectID
}
