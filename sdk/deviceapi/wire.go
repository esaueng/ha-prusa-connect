// Package deviceapi is how a module reads the fleet and drives one device.
//
// It is the fourth door in the same wall as sdk/routesapi, sdk/stateapi and
// sdk/busapi, and it is the first that travels inward: a request arrives
// through routes.serve, a record travels out through state.put, a fact travels
// out through bus.emit, and a reading comes back through the methods here.
//
// # Who a call is made as
//
// Every params object carries a Principal, and it is the same field
// routes.serve hands a module on the way in. A module serving a person's
// request passes that person's principal back out, the host checks it is one
// it handed this module in a request still being served, and the
// authorization graph answers for that person before the module sees a row.
// A principal the host never handed this module is ErrDenied.
//
// A call with no principal is the module's own work: a sync loop, a poll,
// anything with no person behind it. The host resolves it against the
// instance's own principal, which is `sink:<instance>` under an earlier design decision, so a
// module that was granted nothing lists nothing and no module can widen what
// an operator gave its instance.
//
// That is the same rule the HTTP surface serves callers under. One population
// is resolved one way, and a module reading a fleet sees what the caller would
// see at the API.
//
// # Why this is not an HTTP call
//
// A module is a child process the host launched, and the session it already
// holds is authenticated by construction: the host verified the artefact's
// digest before it executed anything. Reaching back over HTTP would need an
// address the child does not have and a credential this project does not mint,
// to ask a question the open pipe can already carry.
package deviceapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/prusa3d/prusa-connect/sdk/wirerpc"
)

// The calls a module makes to read the fleet and drive one device.
const (
	// MethodList is one page of the devices the module's principal may view.
	MethodList wirerpc.Method = "device.list"
	// MethodGet is one device by id.
	MethodGet wirerpc.Method = "device.get"
	// MethodCommand sends one verb to one device and answers what it made of
	// it.
	MethodCommand wirerpc.Method = "device.command"
	// MethodApprove admits one device that is waiting to be let onto this
	// server, by the code it is polling with.
	//
	// It decides admission and does not open a connection: the device appears
	// on the listing once it has dialled, which is its own next step. A module
	// that approves and then lists immediately will not see it yet.
	MethodApprove wirerpc.Method = "device.approve"
	// MethodJobs is what one device is printing and what it printed last, as
	// the record store holds them: a device whose session has ended still
	// answers.
	MethodJobs wirerpc.Method = "device.jobs"
	// MethodFiles is one directory of a device's own file tree, the storages
	// at its root, or one file's metadata by its path.
	//
	// It carries no file content in either direction. A file reaches a device
	// through the transfer route on the HTTP surface, which relays the bytes
	// through the core, and nothing on this wire is a second way to move one.
	MethodFiles wirerpc.Method = "device.files"
	// MethodJobHistory is a page of the prints that have ended: on one device,
	// or on every device the principal may observe.
	//
	// MethodJobs answers the print on the bed and the one before it, and this
	// answers the rest, which is what a report over a month reads.
	MethodJobHistory wirerpc.Method = "device.job_history"
	// MethodPrint starts a print of a file one of this server's storages holds,
	// or of one the printer already holds.
	//
	// It carries the file's name and never its bytes. The host reads the file
	// out of the storage it names and relays it, which is what the HTTP print
	// route does.
	MethodPrint wirerpc.Method = "device.print"
)

const (
	// MaxPageLimit is the largest page this contract answers. It is the bound
	// the HTTP listing already applies, so a module paging a fleet and a person
	// paging the same fleet take the same number of round trips.
	MaxPageLimit = 200

	// DefaultPageLimit is what an unset limit means.
	DefaultPageLimit = 50

	// MaxIDBytes bounds a device id, which the host quotes back in a refusal.
	MaxIDBytes = 128

	// MaxVerbBytes bounds a capability verb.
	MaxVerbBytes = 128

	// MaxArgs bounds how many arguments one command carries.
	MaxArgs = 64

	// MaxArgBytes bounds one argument value. A command carries settings and
	// never content: a file reaches a device through the transfer path and not
	// through an argument.
	MaxArgBytes = 1 << 10

	// MaxCodeBytes bounds a registration code, which the host quotes back in a
	// refusal.
	MaxCodeBytes = 128

	// MaxPrincipalBytes bounds a principal, which the host quotes back in a
	// refusal.
	MaxPrincipalBytes = 256

	// MaxPathBytes bounds a device path. It is the bound the HTTP files
	// route already applies, and a printer's own filesystem has far shorter
	// limits than this.
	MaxPathBytes = 1024

	// MaxJobObjects bounds Job.Objects. A print with more objects than this
	// keeps every cancelled one first, so the list still says which were
	// cancelled unless more than this many were. A client refuses a job that
	// carries more.
	MaxJobObjects = 256

	// MaxJobObjectNameBytes bounds JobObject.Name. The host cuts a longer
	// name on a character boundary, and a client refuses a longer one.
	MaxJobObjectNameBytes = 64
)

// The error codes this contract numbers, so a caller branches on the refusal
// rather than on the text of a message.
//
// They are positive and outside the range from -32768 to -32000, which
// JSON-RPC 2.0 reserves and wirerpc's own codes occupy, and outside the blocks
// sdk/stateapi and sdk/busapi took, so one client can hold every contract and
// tell their refusals apart.
const (
	// CodeNoDevice is ErrNoDevice.
	CodeNoDevice = 3000
	// CodeInvalidQuery is ErrInvalidQuery.
	CodeInvalidQuery = 3001
	// CodeVerbUnknown is ErrVerbUnknown.
	CodeVerbUnknown = 3002
	// CodeArgument is ErrArgument.
	CodeArgument = 3003
	// CodeNotReady is ErrNotReady.
	CodeNotReady = 3004
	// CodeBusy is ErrBusy.
	CodeBusy = 3005
	// CodeNoRegistration is ErrNoRegistration.
	CodeNoRegistration = 3006
	// CodeDenied is ErrDenied.
	CodeDenied = 3007
	// CodeNoFile is ErrNoFile.
	CodeNoFile = 3008
	// CodeNameTaken is ErrNameTaken.
	CodeNameTaken = 3009
	// CodePrintRefused is ErrPrintRefused.
	CodePrintRefused = 3010
	// CodePrintFailed is ErrPrintFailed.
	CodePrintFailed = 3011
)

// The refusals this contract publishes. A module branches on them with
// errors.Is.
var (
	// ErrNoDevice reports a device the module's principal may not see and one
	// this server has never been dialled by. They are one refusal on purpose: a
	// module that could tell them apart could enumerate a fleet it was granted
	// nothing on.
	ErrNoDevice = errors.New("deviceapi: no such device")
	// ErrInvalidQuery reports a page outside the bounds this contract
	// publishes.
	ErrInvalidQuery = errors.New("deviceapi: the query is outside the published bounds")
	// ErrVerbUnknown reports a verb the device's resolved capability set does
	// not carry. The refusal names the verb, and the host may suggest the
	// qualified spelling of one it does carry.
	ErrVerbUnknown = errors.New("deviceapi: the device has no such capability verb")
	// ErrArgument reports an argument the verb does not accept, or a value it
	// will not take.
	ErrArgument = errors.New("deviceapi: the command does not accept that argument")
	// ErrNotReady reports a call from a module the host has not marked ready,
	// or a device whose session has ended. It is a refusal to retry out of.
	ErrNotReady = errors.New("deviceapi: not ready")
	// ErrBusy reports an instance already making as many calls at once as the
	// host allows it. The excess is refused rather than queued.
	ErrBusy = errors.New("deviceapi: the instance is at its concurrent call limit")
	// ErrNoRegistration reports a code no connector is holding: one this server
	// has never been polled with, and one already decided, are the same
	// refusal, so nothing here tells a caller which codes it has seen.
	ErrNoRegistration = errors.New("deviceapi: no such registration")
	// ErrDenied reports a call the host refused to make as the principal it
	// named: one the host never handed this module in a request it is still
	// serving, or the module's own work outside what its instance was granted.
	// Reading the fleet and deciding who joins it are separate grants: a module
	// granted the whole fleet still cannot admit a device.
	ErrDenied = errors.New("deviceapi: the instance may not do that")
	// ErrNoFile reports a storage this server does not declare and a file that
	// storage does not hold. They are one refusal, for the reason ErrNoDevice
	// joins two: a caller that could tell them apart could list the storages of
	// a deployment it was granted nothing on.
	ErrNoFile = errors.New("deviceapi: no such storage or file")
	// ErrNameTaken reports a print onto a path where the printer already
	// holds a file the host will not stand in for the one named. Nothing was
	// sent and the printer's file stays, so a module may print under another
	// name.
	ErrNameTaken = errors.New("deviceapi: the printer already holds a file of that name")
	// ErrPrintRefused reports a print the printer would not start: it answered
	// the start command with a refusal, so no job began. The message carries
	// the printer's own reason where it gave one, which is the printer's word
	// and not this contract's, so show it to a person and do not branch on it.
	ErrPrintRefused = errors.New("deviceapi: the printer refused to start the print")
	// ErrPrintFailed reports a start command the printer took and could not
	// carry out. It is not ErrPrintRefused: a refusal changed nothing, so a
	// second start is a first attempt, while here the printer may have begun
	// and stopped. Read the printer's state before starting it again. The
	// message carries the printer's own reason where it gave one.
	ErrPrintFailed = errors.New("deviceapi: the printer took the print and could not start it")
)

// Device is one device as a module reads it.
//
// It is a flat view and not the record the HTTP surface serves: a module holds
// this contract and no internal type, so a field here is one the host promises
// rather than one that happened to be in scope.
type Device struct {
	// Kind is what type of device this is. It is `printer` on every record this
	// build produces, and it is open: a federated peer running a newer core may
	// name a kind this build does not carry (ADR-20260826-5yj rule 13).
	Kind string `json:"kind"`

	ID       string `json:"id"`
	Name     string `json:"name,omitempty"`
	Vendor   string `json:"vendor,omitempty"`
	Model    string `json:"model,omitempty"`
	Firmware string `json:"firmware,omitempty"`
	Serial   string `json:"serial,omitempty"`
	Driver   string `json:"driver,omitempty"`

	// Connected is false for a device that has been seen and is away. State is
	// then the last it reported.
	Connected bool `json:"connected"`

	// Capabilities is the resolved, enabled verb set, sorted. CapabilityEpoch
	// moves whenever the device reconnects, so a set cached under an older
	// epoch describes a device that is no longer there.
	Capabilities    []string `json:"capabilities,omitempty"`
	CapabilityEpoch uint64   `json:"capability_epoch,omitempty"`

	// Accepts is the enabled verbs the device takes in the state it is in,
	// sorted, as its connector declared them. It is nil for a device whose
	// connector declares no such list, and empty for one that takes nothing,
	// as a device that is away does. It is a hint for deciding what to offer,
	// and the device's own answer to a command stays the decision.
	Accepts []string `json:"accepts"`

	// Ready says a person told the device its bed is clear: it is connected
	// and in a state `core.command.set_printer_ready` leads to.
	Ready bool `json:"ready,omitempty"`

	// State is the addressable state tree the device last reported, keyed by
	// path. `/state` carries the state machine value.
	//
	// The values stay encoded, because a state tree is open: a driver may
	// report a path this build has never seen, and decoding into a fixed shape
	// here would drop it.
	State map[string]json.RawMessage `json:"state,omitempty"`

	// Tags are the plain words an operator put on the device, in the order the
	// operator gave them.
	Tags []string `json:"tags,omitempty"`

	// Labels is the same tags read as key and value labels. A tag "key: value"
	// is the label key with that value, and any other tag is a label with an
	// empty value. Read Tags instead, because Labels is removed in a later
	// release.
	Labels map[string]string `json:"labels,omitempty"`

	// Issues is what the host has found wrong with the device, worst first,
	// and empty when nothing is. It is the list the HTTP listing carries, so a
	// module reacts to the same issues a person sees.
	Issues []Issue `json:"issues,omitempty"`

	// Dialog is what the device is waiting on a person for, and nil when it
	// waits for nothing. A device that has departed carries none, because a
	// dialog up when it went away is not work waiting for anybody.
	Dialog *Dialog `json:"dialog,omitempty"`

	// Retired is true once the id no longer names a machine: an operator
	// removed the printer, or it was registered again under a new id. A
	// retired device never comes back. List leaves it out and Get still
	// answers it, so a module that kept something for the device can let it
	// go. The device.record.changed event names retired when it happens.
	Retired bool `json:"retired,omitempty"`
}

// ListParams is what MethodList carries.
//
// Limit above MaxPageLimit is refused rather than clamped, so a module that
// asked for more than the contract serves learns it did.
type ListParams struct {
	// Principal is who the call is made for, and empty for the module's own
	// work. See the package doc.
	Principal string `json:"principal,omitempty"`

	Limit  int `json:"limit,omitempty"`
	Offset int `json:"offset,omitempty"`

	// Tags narrows the page to devices carrying every one of these tags. Case,
	// joiners and emoji selectors are ignored in the match. It can only ever
	// narrow, because the permission is resolved in the query that produced the
	// rows and no tag reaches that query.
	Tags []string `json:"tags,omitempty"`

	// Labels narrows the page the same way, each clause `key` or `key=value`.
	// A clause `key=value` finds the tag "key: value", and a bare key finds the
	// tag "key" or any tag that starts with "key: ". Use Tags instead, because
	// Labels is removed in a later release.
	Labels []string `json:"labels,omitempty"`
}

// Validate holds one query to the bounds this contract publishes.
//
// It is checked on both sides. A client makes these checks before it spends a
// round trip, and the host makes them again, because a limit checked only in a
// client is a limit a peer that is not this client never made.
func (p ListParams) Validate() error {
	if err := validPrincipal(p.Principal); err != nil {
		return err
	}

	return validWindow(p.Limit, p.Offset)
}

// validPrincipal holds a principal to the bound the host quotes it back under.
// Whether the host will act as that principal is the host's question and is
// asked there.
func validPrincipal(principal string) error {
	if len(principal) > MaxPrincipalBytes {
		return fmt.Errorf("%w: the principal is %d bytes and the limit is %d",
			ErrDenied, len(principal), MaxPrincipalBytes)
	}

	return nil
}

// Page is what MethodList answers. Total counts every device the query matched
// over every page.
type Page struct {
	Items  []Device `json:"items"`
	Total  int      `json:"total"`
	Limit  int      `json:"limit"`
	Offset int      `json:"offset"`
}

// GetParams is what MethodGet carries.
type GetParams struct {
	// Principal is who the call is made for, and empty for the module's own
	// work.
	Principal string `json:"principal,omitempty"`

	ID string `json:"id"`
}

// Validate holds one lookup to the bounds this contract publishes.
func (p GetParams) Validate() error {
	if err := validPrincipal(p.Principal); err != nil {
		return err
	}

	return validID(p.ID)
}

// CommandParams is what MethodCommand carries.
//
// Verb is a capability verb the device's resolved set carries, in its qualified
// spelling. Args are the verb's own arguments.
type CommandParams struct {
	// Principal is who the call is made for, and empty for the module's own
	// work.
	Principal string `json:"principal,omitempty"`

	ID   string            `json:"id"`
	Verb string            `json:"verb"`
	Args map[string]string `json:"args,omitempty"`
}

// Validate holds one command to the bounds this contract publishes.
func (p CommandParams) Validate() error {
	if err := validPrincipal(p.Principal); err != nil {
		return err
	}

	if err := validID(p.ID); err != nil {
		return err
	}

	switch {
	case p.Verb == "":
		return fmt.Errorf("%w: the command names no verb", ErrVerbUnknown)
	case len(p.Verb) > MaxVerbBytes:
		return fmt.Errorf("%w: the verb is %d bytes and the limit is %d",
			ErrVerbUnknown, len(p.Verb), MaxVerbBytes)
	case len(p.Args) > MaxArgs:
		return fmt.Errorf("%w: the command carries %d arguments and the limit is %d",
			ErrArgument, len(p.Args), MaxArgs)
	}

	for name, value := range p.Args {
		if len(value) > MaxArgBytes {
			return fmt.Errorf("%w: %s is %d bytes and the limit is %d",
				ErrArgument, name, len(value), MaxArgBytes)
		}
	}

	return nil
}

// Registration is one device waiting to be let onto this server.
//
// It carries no credential. The token a connector issues is what the machine
// presents on its own wire afterwards, and this contract is not a second way to
// obtain one.
type Registration struct {
	// Code is what the device is polling with, and what an operator reads off
	// its screen. It is the id every operation here takes.
	Code string `json:"code"`

	// Connector is the connector instance holding this registration. A
	// deployment with several reads it to know which wire the device is on.
	Connector string `json:"connector,omitempty"`

	// Fingerprint is the identity the device keeps across reconnections. Once
	// approved, it is what the connector's allowlist is keyed by.
	Fingerprint string `json:"fingerprint,omitempty"`

	// The three below are the machine's own spelling, and each is absent for a
	// dialect whose handshake does not carry it. They are what an operator
	// matches against the machine in front of them before approving it.
	Serial   string `json:"serial,omitempty"`
	Model    string `json:"model,omitempty"`
	Firmware string `json:"firmware,omitempty"`

	// Approved is whether anything has let this device through. It is true on
	// what MethodApprove answers, including for a code that was already
	// approved: approving twice is the same answer as approving once.
	Approved bool `json:"approved"`
}

// ApproveParams is what MethodApprove carries.
type ApproveParams struct {
	// Principal is who the call is made for, and empty for the module's own
	// work.
	Principal string `json:"principal,omitempty"`

	Code string `json:"code"`
}

// Validate holds one approval to the bounds this contract publishes.
//
// It is checked on both sides, for the reason ListParams.Validate gives.
func (p ApproveParams) Validate() error {
	if err := validPrincipal(p.Principal); err != nil {
		return err
	}

	switch {
	case p.Code == "":
		return fmt.Errorf("%w: the call names no registration", ErrNoRegistration)
	case len(p.Code) > MaxCodeBytes:
		return fmt.Errorf("%w: the code is %d bytes and the limit is %d",
			ErrNoRegistration, len(p.Code), MaxCodeBytes)
	}

	return nil
}

// Job is one print as the record store holds it: what was printed, the last
// reading taken while it ran, and when it ran. It is the row the HTTP jobs
// route serves, field for field.
type Job struct {
	// ID is the print's identity, a version 7 UUID the host gives each print.
	// It is the same id the HTTP surface reports.
	ID string `json:"id"`
	// DeviceJobID is the job number the machine issued, as the machine spells
	// it. It is not unique: a machine reuses its numbers after a reset.
	DeviceJobID string `json:"device_job_id"`

	Path        string `json:"path"`
	DisplayName string `json:"display_name"`

	// State is the machine's own word for what the print was doing when this
	// row was last written. The states belong to the device kind that
	// declares them, so read it as an open value.
	State string `json:"state"`

	// Progress is whole percent as the machine reports it, and TimeRemaining
	// is the machine's own estimate in seconds.
	Progress      float64 `json:"progress"`
	TimeRemaining int64   `json:"time_remaining"`

	// Started is when this server first saw the print. Ended is when it saw
	// the print stop, and is absent while the print runs.
	Started time.Time  `json:"started"`
	Ended   *time.Time `json:"ended,omitempty"`

	// Outcome is how the print ended: finished, failed, cancelled or
	// unknown, as the machine's driver reported it. It is empty while the
	// print runs. A print whose machine said nothing reads unknown, and so
	// does one that ended before the host kept the outcome.
	Outcome string `json:"outcome,omitempty"`

	// ObjectCount is how many objects of the print the machine reported
	// that can be cancelled one by one, as of the last reading this row
	// holds. It is nil when the machine said nothing about them, and 0 for a
	// print that has none.
	//
	// Objects is those objects, ordered by id, at most MaxJobObjects of
	// them. A machine that reported more keeps every cancelled object before
	// the others, so an object missing from the list was not cancelled unless
	// more than MaxJobObjects were.
	//
	// Both stay on a print that has ended: they are the last set the machine
	// reported before the end, and say which objects the finished print
	// skipped.
	ObjectCount *int        `json:"object_count,omitempty"`
	Objects     []JobObject `json:"objects,omitempty"`
}

// Jobs is what MethodJobs answers: the print on the bed and the one before
// it. Both are optional and the four combinations are all real answers: a
// machine that has never printed carries neither, and one printing again
// carries both. The two are never the same row.
//
// It is the HTTP surface's shape and not a page. The core keeps the current
// print and the last one, and a longer history is not a thing it holds, so a
// page here would promise rows nothing can serve.
type Jobs struct {
	Current *Job `json:"current,omitempty"`
	Last    *Job `json:"last,omitempty"`
}

// JobsParams is what MethodJobs carries.
type JobsParams struct {
	// Principal is who the call is made for, and empty for the module's own
	// work.
	Principal string `json:"principal,omitempty"`

	ID string `json:"id"`
}

// Validate holds one jobs read to the bounds this contract publishes.
func (p JobsParams) Validate() error {
	if err := validPrincipal(p.Principal); err != nil {
		return err
	}

	return validID(p.ID)
}

// Storage is one storage device the printer reported, as the HTTP surface
// reports it. Mountpoint is the identity: two devices can report the same
// name and type.
type Storage struct {
	// Mountpoint is the printer-side path with its leading slash.
	Mountpoint string `json:"mountpoint"`
	// Name is the first path segment of every file on this storage, such as
	// usb.
	Name string `json:"name"`
	// Type is the device class as the printer spelled it. The set is the
	// firmware's, so read it rather than switch on it exhaustively.
	Type string `json:"type"`
	// FreeSpace is bytes left, or nil for a printer that reported no figure.
	// Zero is a full device, and only one of the two makes an upload fail.
	FreeSpace *int64 `json:"free_space"`
	ReadOnly  bool   `json:"read_only"`
}

// File is one entry in a printer's own file tree, as the HTTP surface reports
// it.
type File struct {
	// Path is storage-prefixed, such as usb/models/part.bgcode. It is what
	// MethodFiles takes.
	Path string `json:"path"`
	// Name is what the printer addresses the entry by. On a FAT storage that
	// is the short name, which is not what a person reads.
	Name string `json:"name"`
	// DisplayName is the name written for a person, absent when the printer
	// reports only one name.
	DisplayName string `json:"display_name,omitempty"`
	// Folder is whether the entry holds other entries.
	Folder bool `json:"folder"`
	// Size is bytes, zero for a folder. Modified is seconds since the Unix
	// epoch as the printer reported them, zero for a board with no clock.
	Size     int64 `json:"size"`
	Modified int64 `json:"modified"`
	ReadOnly bool  `json:"read_only"`
	// EstimatedSeconds is the print time the slicer estimated, in whole
	// seconds, as the printer read it out of the file. It comes only on
	// Listing.Entry, the answer about one print file, and is zero for a
	// printer or a file that names none.
	EstimatedSeconds int64 `json:"estimated_seconds,omitempty"`
	// PrinterModel, LayerHeightMM and Material are what the slicer wrote:
	// the printer it sliced for, the layer height in millimetres, and the
	// material, one for each tool joined by a semicolon. They come only on
	// Listing.Entry, and are empty or zero for a file that names none.
	PrinterModel  string  `json:"printer_model,omitempty"`
	LayerHeightMM float64 `json:"layer_height_mm,omitempty"`
	Material      string  `json:"material,omitempty"`
}

// FilesParams is what MethodFiles carries.
//
// An empty Path is the root, and the answer is the storages the printer
// reported. Any other path is answered from the machine: a folder answers its
// entries, and a file answers its own metadata and no entries. Limit and
// Offset window whichever list the answer carries, and Limit above
// MaxPageLimit is refused rather than clamped.
type FilesParams struct {
	// Principal is who the call is made for, and empty for the module's own
	// work.
	Principal string `json:"principal,omitempty"`

	ID   string `json:"id"`
	Path string `json:"path,omitempty"`

	Limit  int `json:"limit,omitempty"`
	Offset int `json:"offset,omitempty"`

	// Cached answers a path from what the server last read of it, at once
	// and without waiting on the printer, which runs one command at a time.
	// The rows may be out of date, and a file in a folder's rows carries no
	// slicer's facts. A path the server holds nothing of is answered with no
	// rows and no entry while the read it starts runs, so asking again a
	// little later finds it. A path whose last read failed is answered the
	// same way, and is not read again for this ask.
	Cached bool `json:"cached,omitempty"`
}

// Validate holds one files read to the bounds this contract publishes.
//
// It is checked on both sides, for the reason ListParams.Validate gives.
func (p FilesParams) Validate() error {
	if err := validPrincipal(p.Principal); err != nil {
		return err
	}

	if err := validID(p.ID); err != nil {
		return err
	}

	if len(p.Path) > MaxPathBytes {
		return fmt.Errorf("%w: the path is %d bytes and the limit is %d",
			ErrInvalidQuery, len(p.Path), MaxPathBytes)
	}

	return validWindow(p.Limit, p.Offset)
}

// Listing is what MethodFiles answers.
//
// Storages is filled at the root and Entry and Items under a path. Total
// counts the whole list the window was cut from, so a module sizes a pager
// from one answer, and Limit and Offset are what the host applied.
type Listing struct {
	// Entry is the entry that was asked for: a folder whose items follow, or
	// a file, which has none. Folder tells the two apart. It is absent at the
	// root.
	Entry *File `json:"entry,omitempty"`

	// Storages are the storage devices the printer reported, at the root.
	Storages []Storage `json:"storages,omitempty"`

	// Items is one window of what is directly inside Entry, in the order the
	// device reported them.
	Items []File `json:"items,omitempty"`

	Total  int `json:"total"`
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

// validID holds a device id to the bound the host quotes it back under.
func validID(id string) error {
	switch {
	case id == "":
		return fmt.Errorf("%w: the call names no device", ErrNoDevice)
	case len(id) > MaxIDBytes:
		return fmt.Errorf("%w: the id is %d bytes and the limit is %d",
			ErrNoDevice, len(id), MaxIDBytes)
	}

	return nil
}

// validWindow holds one page window to the bounds this contract publishes.
func validWindow(limit, offset int) error {
	switch {
	case limit < 0:
		return fmt.Errorf("%w: the limit is %d", ErrInvalidQuery, limit)
	case limit > MaxPageLimit:
		return fmt.Errorf("%w: the limit is %d and the maximum is %d",
			ErrInvalidQuery, limit, MaxPageLimit)
	case offset < 0:
		return fmt.Errorf("%w: the offset is %d", ErrInvalidQuery, offset)
	}

	return nil
}

// Result is what MethodCommand answers: whether the device took the verb, and
// what it said when it did not.
//
// A refused command is a Result with OK false and not a transport error. The
// call reached the device and the device answered, which is a fact a module
// reports rather than an outage it retries.
type Result struct {
	OK bool `json:"ok"`

	// MachineReason is the device's own word for a refusal, passed through as
	// it spelled it. It is the machine's vocabulary and not this contract's:
	// no set of values is published, a firmware may send one this build has
	// never seen, and two vendors may spell one condition differently. Show it
	// to a person rather than branching on it.
	MachineReason string `json:"machine_reason,omitempty"`

	// Ignored names the parameters the device dropped, and Substituted the ones
	// it replaced and with what.
	Ignored     []string          `json:"ignored,omitempty"`
	Substituted map[string]string `json:"substituted,omitempty"`
}

// Fleet is the host's side of this contract. The host supplies it and a module
// never constructs one.
//
// Each method takes the params object whole, because the principal on it is
// what the host resolves the call against, and a method that took the fields
// apart would have to take that one too.
type Fleet interface {
	List(ctx context.Context, params ListParams) (Page, error)
	Get(ctx context.Context, params GetParams) (Device, error)
	Command(ctx context.Context, params CommandParams) (Result, error)
	Approve(ctx context.Context, params ApproveParams) (Registration, error)
	Jobs(ctx context.Context, params JobsParams) (Jobs, error)
	Files(ctx context.Context, params FilesParams) (Listing, error)
	Print(ctx context.Context, params PrintParams) (Started, error)
	JobHistory(ctx context.Context, params JobHistoryParams) (JobPage, error)
}

// Caller is the half of a wirerpc session this contract needs, so a test holds
// a fake without standing up a session.
type Caller interface {
	Call(ctx context.Context, method wirerpc.Method, params any) (json.RawMessage, error)
}

// Client is a module's own way onto the fleet.
type Client struct {
	conn Caller
}

// NewClient returns the fleet a module reads through.
//
// A host that serves no fleet answers CodeMethodNotFound, which arrives as an
// ordinary error on the first call rather than as a failure to construct.
func NewClient(conn Caller) *Client {
	return &Client{conn: conn}
}

// List answers one page of the devices this module may see.
func (c *Client) List(ctx context.Context, params ListParams) (Page, error) {
	if err := params.Validate(); err != nil {
		return Page{}, fmt.Errorf("deviceapi: %s: %w", MethodList, err)
	}

	var page Page
	if err := c.call(ctx, MethodList, params, &page); err != nil {
		return Page{}, err
	}

	return page, nil
}

// Get answers one device.
func (c *Client) Get(ctx context.Context, params GetParams) (Device, error) {
	if err := params.Validate(); err != nil {
		return Device{}, fmt.Errorf("deviceapi: %s: %w", MethodGet, err)
	}

	var device Device
	if err := c.call(ctx, MethodGet, params, &device); err != nil {
		return Device{}, err
	}

	return device, nil
}

// Command sends one verb to one device.
//
// Arguments are text, which is the shape the capability descriptor checks them
// against: a verb declares its arguments and the host holds a value to that
// declaration before the device is reached.
func (c *Client) Command(ctx context.Context, params CommandParams) (Result, error) {
	if err := params.Validate(); err != nil {
		return Result{}, fmt.Errorf("deviceapi: %s: %w", MethodCommand, err)
	}

	var result Result
	if err := c.call(ctx, MethodCommand, params, &result); err != nil {
		return Result{}, err
	}

	return result, nil
}

// Approve admits one device that is waiting to be let onto this server.
//
// It takes a permission of its own, which reading the fleet does not carry: an
// instance granted every device still answers ErrDenied here until an operator
// grants it admit_device on the driver resource kind. Deciding who may join a
// fleet is a larger thing than reading one.
//
// An approval naming no principal is the module's own. The instance is
// recorded as what admitted the machine, and no person is: a device this
// server already knows keeps its id and its owner.
//
// A code no connector holds is ErrNoRegistration, and so is one already
// rejected. Approving an approved code answers the same registration again.
func (c *Client) Approve(ctx context.Context, params ApproveParams) (Registration, error) {
	if err := params.Validate(); err != nil {
		return Registration{}, fmt.Errorf("deviceapi: %s: %w", MethodApprove, err)
	}

	var registration Registration
	if err := c.call(ctx, MethodApprove, params, &registration); err != nil {
		return Registration{}, err
	}

	return registration, nil
}

// Jobs answers what one device is printing and what it printed last.
//
// It takes what Get takes, because a job is a reading about a machine, and it
// answers the same refusal: a device the principal may not see and one this
// server has never been dialled by are both ErrNoDevice. It reads the record
// store and not the session, so a device that has gone away still answers
// with what it was printing.
func (c *Client) Jobs(ctx context.Context, params JobsParams) (Jobs, error) {
	if err := params.Validate(); err != nil {
		return Jobs{}, fmt.Errorf("deviceapi: %s: %w", MethodJobs, err)
	}

	var jobs Jobs
	if err := c.call(ctx, MethodJobs, params, &jobs); err != nil {
		return Jobs{}, err
	}

	for _, job := range []*Job{jobs.Current, jobs.Last} {
		if err := job.validObjects(); err != nil {
			return Jobs{}, fmt.Errorf("deviceapi: %s: %w", MethodJobs, err)
		}
	}

	return jobs, nil
}

// Files answers one window of what a device holds.
//
// An empty path answers the storages the printer reported, which the printer
// volunteered when it connected, so it takes what Get takes and reaches no
// machine. Any other path is asked of the machine and takes what Command
// takes: a module that may only watch a device cannot make the host put a
// frame on its wire. A path naming a file answers that file's metadata as
// Entry and no items, which is the one read of a file this contract has.
//
// No file content crosses this wire. Moving a file onto a device is an HTTP
// operation, the transfer route, and the bytes go through the core.
//
// A path carrying a .. segment is ErrInvalidQuery and never reaches the
// device. A device whose session has ended is ErrNotReady, and one whose
// resolved capability set cannot report files is ErrVerbUnknown.
func (c *Client) Files(ctx context.Context, params FilesParams) (Listing, error) {
	if err := params.Validate(); err != nil {
		return Listing{}, fmt.Errorf("deviceapi: %s: %w", MethodFiles, err)
	}

	var listing Listing
	if err := c.call(ctx, MethodFiles, params, &listing); err != nil {
		return Listing{}, err
	}

	return listing, nil
}

// call makes one round trip and decodes the answer, mapping a coded refusal
// back to the sentinel the host raised.
func (c *Client) call(ctx context.Context, method wirerpc.Method, params, into any) error {
	if c.conn == nil {
		return fmt.Errorf("deviceapi: %s: the client holds no session", method)
	}

	body, err := c.conn.Call(ctx, method, params)
	if err != nil {
		if coded, ok := errors.AsType[*wirerpc.Error](err); ok {
			if sentinel := sentinelFor(coded.Code); sentinel != nil {
				return fmt.Errorf("%s: %w", method, &remote{sentinel: sentinel, message: coded.Message})
			}
		}

		return fmt.Errorf("deviceapi: %s: %w", method, err)
	}

	if len(body) == 0 {
		return nil
	}

	if err = json.Unmarshal(body, into); err != nil {
		return fmt.Errorf("deviceapi: %s: read the answer: %w", method, err)
	}

	return nil
}

// codedRefusals pairs each code this contract numbers with its sentinel. The
// host reads it to send a refusal and the module reads it to raise one, so
// the two sides cannot disagree about a code.
var codedRefusals = []struct {
	code     int
	sentinel error
}{
	{CodeNoDevice, ErrNoDevice},
	{CodeNoFile, ErrNoFile},
	{CodeNoRegistration, ErrNoRegistration},
	{CodeDenied, ErrDenied},
	{CodeInvalidQuery, ErrInvalidQuery},
	{CodeVerbUnknown, ErrVerbUnknown},
	{CodeArgument, ErrArgument},
	{CodeNotReady, ErrNotReady},
	{CodeBusy, ErrBusy},
	{CodeNameTaken, ErrNameTaken},
	{CodePrintRefused, ErrPrintRefused},
	{CodePrintFailed, ErrPrintFailed},
}

// remote carries the host's own message under this contract's sentinel, so a
// module reads both what was refused and why.
type remote struct {
	sentinel error
	message  string
}

func (r *remote) Error() string { return r.message }
func (r *remote) Unwrap() error { return r.sentinel }

func sentinelFor(code int) error {
	for _, known := range codedRefusals {
		if known.code == code {
			return known.sentinel
		}
	}

	return nil
}

// HostFailed is what a module is told when a call failed for a reason of the
// host's own. It is a fixed sentence rather than the failure's text, which
// names this host's internals, and the host has the detail in its own log.
const HostFailed = "deviceapi: the host could not answer"

// refusal renders a host-side error as the code this contract numbers, so the
// child maps it back to the same sentinel the host raised.
//
// A coded refusal crosses whole, because each one names something an author
// acts on. Anything else is the host's own failure and does not cross at all.
func refusal(err error) error {
	for _, known := range codedRefusals {
		if errors.Is(err, known.sentinel) {
			return &wirerpc.Error{Code: known.code, Message: err.Error()}
		}
	}

	return &wirerpc.Error{Code: wirerpc.CodeInternal, Message: HostFailed}
}

// Handlers is the host's method set for this contract, bound to one module's
// fleet view.
//
// A nil fleet is a host that serves none: it returns an empty set, so a call
// reads CodeMethodNotFound rather than reaching a nil and panicking inside a
// supervised start.
func Handlers(fleet Fleet) wirerpc.Methods {
	if fleet == nil {
		return wirerpc.Methods{}
	}

	return wirerpc.Methods{
		MethodList:    listHandler(fleet),
		MethodGet:     getHandler(fleet),
		MethodCommand: commandHandler(fleet),
		MethodApprove: approveHandler(fleet),
		MethodJobs:    jobsHandler(fleet),
		MethodFiles:   filesHandler(fleet),
		MethodPrint:   printHandler(fleet),

		MethodJobHistory: jobHistoryHandler(fleet),
	}
}

func jobHistoryHandler(fleet Fleet) wirerpc.Handler {
	return func(ctx context.Context, call wirerpc.Call) (any, error) {
		var params JobHistoryParams

		// An absent params object is the first page over the whole fleet,
		// because that is the question a report asks first.
		if len(call.Params) > 0 {
			if err := json.Unmarshal(call.Params, &params); err != nil {
				return nil, badParams(MethodJobHistory)
			}
		}

		if err := params.Validate(); err != nil {
			return nil, refusal(err)
		}

		page, err := fleet.JobHistory(ctx, params)
		if err != nil {
			return nil, refusal(err)
		}

		return page, nil
	}
}

func printHandler(fleet Fleet) wirerpc.Handler {
	return func(ctx context.Context, call wirerpc.Call) (any, error) {
		var params PrintParams

		if len(call.Params) == 0 {
			return nil, badParams(MethodPrint)
		}

		if err := json.Unmarshal(call.Params, &params); err != nil {
			return nil, badParams(MethodPrint)
		}

		if err := params.Validate(); err != nil {
			return nil, refusal(err)
		}

		started, err := fleet.Print(ctx, params)
		if err != nil {
			return nil, refusal(err)
		}

		return started, nil
	}
}

func listHandler(fleet Fleet) wirerpc.Handler {
	return func(ctx context.Context, call wirerpc.Call) (any, error) {
		var params ListParams

		// An absent params object is the whole first page, because a listing
		// with no narrowing is the question a module asks first.
		if len(call.Params) > 0 {
			if err := json.Unmarshal(call.Params, &params); err != nil {
				return nil, badParams(MethodList)
			}
		}

		if err := params.Validate(); err != nil {
			return nil, refusal(err)
		}

		page, err := fleet.List(ctx, params)
		if err != nil {
			return nil, refusal(err)
		}

		return page, nil
	}
}

func getHandler(fleet Fleet) wirerpc.Handler {
	return func(ctx context.Context, call wirerpc.Call) (any, error) {
		var params GetParams

		if len(call.Params) == 0 {
			return nil, badParams(MethodGet)
		}

		if err := json.Unmarshal(call.Params, &params); err != nil {
			return nil, badParams(MethodGet)
		}

		if err := params.Validate(); err != nil {
			return nil, refusal(err)
		}

		device, err := fleet.Get(ctx, params)
		if err != nil {
			return nil, refusal(err)
		}

		return device, nil
	}
}

func commandHandler(fleet Fleet) wirerpc.Handler {
	return func(ctx context.Context, call wirerpc.Call) (any, error) {
		var params CommandParams

		if len(call.Params) == 0 {
			return nil, badParams(MethodCommand)
		}

		if err := json.Unmarshal(call.Params, &params); err != nil {
			return nil, badParams(MethodCommand)
		}

		if err := params.Validate(); err != nil {
			return nil, refusal(err)
		}

		result, err := fleet.Command(ctx, params)
		if err != nil {
			return nil, refusal(err)
		}

		return result, nil
	}
}

func approveHandler(fleet Fleet) wirerpc.Handler {
	return func(ctx context.Context, call wirerpc.Call) (any, error) {
		var params ApproveParams

		if len(call.Params) == 0 {
			return nil, badParams(MethodApprove)
		}

		if err := json.Unmarshal(call.Params, &params); err != nil {
			return nil, badParams(MethodApprove)
		}

		if err := params.Validate(); err != nil {
			return nil, refusal(err)
		}

		registration, err := fleet.Approve(ctx, params)
		if err != nil {
			return nil, refusal(err)
		}

		return registration, nil
	}
}

func jobsHandler(fleet Fleet) wirerpc.Handler {
	return func(ctx context.Context, call wirerpc.Call) (any, error) {
		var params JobsParams

		if len(call.Params) == 0 {
			return nil, badParams(MethodJobs)
		}

		if err := json.Unmarshal(call.Params, &params); err != nil {
			return nil, badParams(MethodJobs)
		}

		if err := params.Validate(); err != nil {
			return nil, refusal(err)
		}

		jobs, err := fleet.Jobs(ctx, params)
		if err != nil {
			return nil, refusal(err)
		}

		return jobs, nil
	}
}

func filesHandler(fleet Fleet) wirerpc.Handler {
	return func(ctx context.Context, call wirerpc.Call) (any, error) {
		var params FilesParams

		if len(call.Params) == 0 {
			return nil, badParams(MethodFiles)
		}

		if err := json.Unmarshal(call.Params, &params); err != nil {
			return nil, badParams(MethodFiles)
		}

		if err := params.Validate(); err != nil {
			return nil, refusal(err)
		}

		listing, err := fleet.Files(ctx, params)
		if err != nil {
			return nil, refusal(err)
		}

		return listing, nil
	}
}

func badParams(method wirerpc.Method) error {
	return &wirerpc.Error{
		Code:    wirerpc.CodeInvalidParams,
		Message: string(method) + " carries a params object",
	}
}
