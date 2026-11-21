package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/prusa3d/prusa-connect/internal/app/resourcehost"
	"github.com/prusa3d/prusa-connect/internal/authz"
	"github.com/prusa3d/prusa-connect/internal/kernel/admission"
	"github.com/prusa3d/prusa-connect/internal/kernel/deviceissue"
	"github.com/prusa3d/prusa-connect/internal/kernel/devicestate"
	"github.com/prusa3d/prusa-connect/internal/kernel/machinetext"
	"github.com/prusa3d/prusa-connect/internal/kernel/ref"
	"github.com/prusa3d/prusa-connect/internal/transport/http/httpapi"
	"github.com/prusa3d/prusa-connect/sdk/connectorapi"
	"github.com/prusa3d/prusa-connect/sdk/deviceapi"
	"github.com/prusa3d/prusa-connect/sdk/fault"
)

// moduleFleet answers sdk/deviceapi for one module instance.
//
// A call names the person the module is serving, or nobody. The supervision
// plane has already refused a person it did not hand the module, so a named
// principal is passed into the call as it stands and the listing resolves
// what that person may see. A call naming nobody is the module's own work and
// runs as the instance's own principal inside the ceiling the operator wrote
// for it (moduleStanding), so a module reads the population an operator
// granted that instance and no other. The resolution is the listing's own: the
// same walk answers a person and a module, and neither can widen it.
type moduleFleet struct {
	fleet moduleDevices
	// admissions is nil until the connectors are mounted, and stays nil in a
	// deployment whose connectors keep no registration table. Either answers
	// ErrNoRegistration for every code.
	admissions *registrations
	// grants answers the one question this contract asks the graph directly.
	// Reading the fleet resolves the principal inside the listing; admitting a
	// device is a permission of its own and is asked for here.
	grants httpapi.GrantStore
	// states is the state set each connector instance declared, and nil
	// where none declared one.
	states map[string]connectorapi.StateSet
	// standing is who a call naming nobody runs as, and what it may do.
	standing moduleStanding
	// data is the instance's data directory, and empty when the operator
	// granted none. A print from deviceapi.ModuleDataStorage reads its print
	// folder and no other.
	data string
	// log is where the reason behind a refusal that names nothing is written.
	log *refusalLog
}

var _ deviceapi.Fleet = moduleFleet{}

// moduleDevices is the fleet a module reads. A printer listing on it waits for
// the printer, because the module wire carries no reading state.
type moduleDevices interface {
	httpapi.DeviceFleet
	FilesNow(ctx context.Context, principal, id, path string) (httpapi.DeviceListing, error)
	FilesHeld(ctx context.Context, principal, id, path string) (httpapi.DeviceListing, error)
}

// moduleFleets binds an instance to the fleet it may read.
//
// It is a late binding on purpose. The supervision plane starts before the
// fleet exists, because the fleet stages uploads through the plane, so the
// plane cannot be handed a fleet at construction. The holder is filled once,
// after both exist and before the first reconcile starts a declared instance.
// The fields are plain and nothing locks on the way to them, so that order is
// what puts the writes before every read a module makes as it starts. A plane
// run with no fleet, which the tests do, reads CodeMethodNotFound rather than
// a nil.
type moduleFleets struct {
	fleet      moduleDevices
	states     map[string]connectorapi.StateSet
	admissions *registrations
	grants     httpapi.GrantStore
	log        *refusalLog
}

// bind fills the holder. It runs once, before any module starts.
func (m *moduleFleets) bind(fleet moduleDevices) { m.fleet = fleet }

// bindStates fills what says which verbs a device takes in its state. It runs
// before any module starts, for the reason bind does.
func (m *moduleFleets) bindStates(installed devicestate.Installed) {
	m.states = statesByInstance(installed)
}

// statesByInstance is the declared state sets keyed by connector instance,
// which is what a device names under driver.
func statesByInstance(installed devicestate.Installed) map[string]connectorapi.StateSet {
	out := map[string]connectorapi.StateSet{}

	for _, declaration := range installed.Declarations() {
		for _, instance := range declaration.Instances {
			out[instance] = declaration.States
		}
	}

	return out
}

// bindAdmission fills what device.approve needs.
//
// It is a second step because the registration tables belong to the connectors,
// and the connectors are mounted onto the fleet, so they cannot exist before it.
// It runs before any module starts, for the reason bind does. An unfilled
// holder refuses every approval, and so does a deployment whose connectors
// keep no table. A module reading the fleet is unaffected by either.
func (m *moduleFleets) bindAdmission(admissions *registrations, grants httpapi.GrantStore) {
	m.admissions = admissions
	m.grants = grants
}

// forModule is what resourcehost.Config.Fleet takes.
func (m *moduleFleets) forModule(module resourcehost.Module) deviceapi.Fleet {
	if m.fleet == nil {
		return nil
	}

	return moduleFleet{
		fleet:      m.fleet,
		states:     m.states,
		admissions: m.admissions,
		grants:     m.grants,
		standing:   standingOf(module),
		data:       module.Data,
		log:        m.log,
	}
}

// List answers one page of the devices this module may see.
func (f moduleFleet) List(ctx context.Context, params deviceapi.ListParams) (deviceapi.Page, error) {
	limit := params.Limit
	if limit == 0 {
		limit = deviceapi.DefaultPageLimit
	}

	who, err := f.as(params.Principal, authz.ActionObserve)
	if err != nil {
		return deviceapi.Page{}, err
	}

	page, err := f.fleet.List(ctx, who, httpapi.DeviceQuery{
		Page:   httpapi.Page{Limit: limit, Offset: params.Offset},
		Tags:   params.Tags,
		Labels: params.Labels,
	})
	if err != nil {
		return deviceapi.Page{}, moduleRefusal(err)
	}

	items := make([]deviceapi.Device, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, f.device(item))
	}

	return deviceapi.Page{
		Items:  items,
		Total:  page.Total,
		Limit:  page.Limit,
		Offset: page.Offset,
	}, nil
}

// as is who a call runs as: the person it names, or the instance itself when
// the action is inside its ceiling. A call outside the ceiling is refused with
// this contract's own sentinel.
func (f moduleFleet) as(principal string, action authz.Action) (string, error) {
	who, err := f.standing.as(principal, action)
	if err != nil {
		return "", fmt.Errorf("%w: %s takes %s, and %w", deviceapi.ErrDenied, f.standing.principal, action, err)
	}

	return who, nil
}

// Get answers one device.
func (f moduleFleet) Get(ctx context.Context, params deviceapi.GetParams) (deviceapi.Device, error) {
	who, err := f.as(params.Principal, authz.ActionObserve)
	if err != nil {
		return deviceapi.Device{}, err
	}

	device, err := f.fleet.Get(ctx, who, params.ID)
	if err != nil {
		return deviceapi.Device{}, moduleRefusal(err)
	}

	return f.device(device), nil
}

// Command sends one verb to one device.
//
// A device that refused the verb is a Result with OK false and not an error:
// the call reached the machine and the machine answered, which is a fact a
// module reports rather than an outage it retries.
func (f moduleFleet) Command(
	ctx context.Context, params deviceapi.CommandParams,
) (deviceapi.Result, error) {
	who, err := f.as(params.Principal, f.commandAction(ctx, params))
	if err != nil {
		return deviceapi.Result{}, err
	}

	result, err := f.fleet.Command(ctx, who, params.ID, httpapi.PrinterCommand{
		Verb: params.Verb,
		Args: params.Args,
	})
	if err != nil {
		return deviceapi.Result{}, moduleRefusal(err)
	}

	return deviceapi.Result{
		OK:            result.OK,
		MachineReason: result.MachineReason,
		Ignored:       result.Ignored,
		Substituted:   result.Substituted,
	}, nil
}

// commandAction is the class the verb carries on the device's kind, which is
// what the ceiling is held against.
//
// It is empty for a command the module sends for a person, because that one is
// decided against the person and never against the ceiling. It is also empty
// when this build does not know the verb or the caller cannot read the device,
// and no ceiling holds the empty class, so such a command is refused here and
// the fleet never sees it. The refusal names the principal and no fact about
// the device, so a device that is not there and a device the caller may not
// see are one answer.
//
// The device is read as whoever the command is for: the instance for its own
// work, and the server that joined this one for a call a link relays. Reading
// it as the instance would let a link with a wide grant resolve a class for a
// device the server above may not see.
func (f moduleFleet) commandAction(ctx context.Context, params deviceapi.CommandParams) authz.Action {
	who := params.Principal

	switch {
	case who == "":
		who = f.standing.principal
	case !namesANode(who):
		return ""
	}

	device, err := f.fleet.Get(ctx, who, params.ID)
	if err != nil {
		return ""
	}

	return authz.ActionForVerb(authz.DeviceKind(device.Kind), params.Verb)
}

// Approve admits one device that is waiting to be let onto this server.
//
// The permission is asked for here rather than resolved inside the call, which
// is what separates this from List: a listing narrows to what the principal may
// see, and there is no narrower form of admitting a device. The question is the
// one the HTTP surface asks before it approves, on the same object:
// admit_device on the driver resource kind. A module and a person are held to
// one rule.
//
// An approval for a person the module is serving is that person's, recorded
// and decided as the API would. One the module makes as itself records the
// instance as what admitted the machine and names no person: nothing is owned,
// nothing is transferred, and a machine this fleet already has keeps its id.
func (f moduleFleet) Approve(
	ctx context.Context, params deviceapi.ApproveParams,
) (deviceapi.Registration, error) {
	code := params.Code

	who, err := f.as(params.Principal, authz.ActionAdmitDevice)
	if err != nil {
		return deviceapi.Registration{}, err
	}

	if err = f.mayAdmit(ctx, who); err != nil {
		return deviceapi.Registration{}, err
	}

	if f.admissions == nil {
		return deviceapi.Registration{}, fmt.Errorf(
			"%w: this server holds no registration table", deviceapi.ErrNoRegistration)
	}

	approved, err := f.admissions.admit(ctx, code, who, params.Principal)
	if err != nil {
		if errors.Is(err, admission.ErrUnknownCode) {
			return deviceapi.Registration{}, fmt.Errorf("%w: %w", deviceapi.ErrNoRegistration, err)
		}

		return deviceapi.Registration{}, moduleRefusal(err)
	}

	return deviceapi.Registration{
		Code:        approved.Code,
		Connector:   approved.Connector,
		Fingerprint: approved.Fingerprint,
		Serial:      approved.Serial,
		Model:       approved.Model,
		Firmware:    approved.Firmware,
		Approved:    approved.Approved,
	}, nil
}

// Jobs answers what one device is printing and what it printed last.
//
// It takes what Get takes and calls what the HTTP jobs route calls: a job is a
// reading about a machine, read from the record store rather than the session,
// so a device that has gone away still answers.
func (f moduleFleet) Jobs(ctx context.Context, params deviceapi.JobsParams) (deviceapi.Jobs, error) {
	who, err := f.as(params.Principal, authz.ActionObserve)
	if err != nil {
		return deviceapi.Jobs{}, err
	}

	jobs, err := f.fleet.Jobs(ctx, who, params.ID)
	if err != nil {
		return deviceapi.Jobs{}, moduleRefusal(err)
	}

	return deviceapi.Jobs{Current: moduleJob(jobs.Current), Last: moduleJob(jobs.Last)}, nil
}

// Files answers one window of what a device holds.
//
// The root and a path are two calls on the HTTP surface, and they take two
// permissions. The storages are what the printer volunteered when it
// connected, so the root takes observe and reaches no machine. A path is
// asked of the machine, which puts a frame on a wire that serialises one
// command at a time, so it takes operate: a module that may only watch a
// device cannot make this server queue work at it. Both go to the same core
// functions the routes call, and the window is cut here from what they
// answered, because neither route pages.
func (f moduleFleet) Files(ctx context.Context, params deviceapi.FilesParams) (deviceapi.Listing, error) {
	limit := params.Limit
	if limit == 0 {
		limit = deviceapi.DefaultPageLimit
	}

	if params.Path == "" {
		return f.storages(ctx, params, limit)
	}

	who, err := f.as(params.Principal, authz.ActionOperate)
	if err != nil {
		return deviceapi.Listing{}, err
	}

	// From the printer, unless the module asked for what this server holds.
	// The listing carries no stale mark on the module wire, so a module that
	// takes the held rows knows only that it asked for them.
	listing, err := f.listing(ctx, who, params)
	if err != nil {
		return deviceapi.Listing{}, moduleRefusal(err)
	}

	// A held answer is the one marked stale. Without one the listing names the
	// path as an empty folder, which is a guess: the path may be a file. The
	// module is told nothing about it.
	if params.Cached && !listing.Stale {
		return deviceapi.Listing{Items: []deviceapi.File{}, Limit: limit, Offset: params.Offset}, nil
	}

	entry := moduleFile(listing.Directory)
	low, high := pageWindow(len(listing.Items), limit, params.Offset)

	items := make([]deviceapi.File, 0, high-low)
	for _, item := range listing.Items[low:high] {
		items = append(items, moduleFile(item))
	}

	return deviceapi.Listing{
		Entry:  &entry,
		Items:  items,
		Total:  len(listing.Items),
		Limit:  limit,
		Offset: params.Offset,
	}, nil
}

// Print starts a print of a file one of this server's storages holds, or of one
// the printer already holds.
//
// It takes the two permissions the HTTP print route takes, and takes them in
// the same order: operate on the printer, then read_storage on the storage
// kind. A module's own print is held to its ceiling on both, so an instance
// installed with operate and without read_storage prints what a machine already
// holds and nothing out of a storage.
//
// A module granted a data directory names deviceapi.ModuleDataStorage to print
// a file from its own print folder. The folder rides on the context to the
// staging, which is the only reader of that name.
//
// It answers when the transfer has started. The host goes on relaying the file
// and sends the start command when the last byte has arrived, so a module that
// asked for a large file is not holding a call open for the minutes it takes. A
// module follows the rest through device.transfer.settled and
// device.command.settled on the event stream.
func (f moduleFleet) Print(
	ctx context.Context, params deviceapi.PrintParams,
) (deviceapi.Started, error) {
	who, err := f.as(params.Principal, authz.ActionOperate)
	if err != nil {
		return deviceapi.Started{}, err
	}

	if params.Storage != "" {
		if _, err = f.as(params.Principal, authz.ActionReadStorage); err != nil {
			return deviceapi.Started{}, err
		}
	}

	if params.Storage == deviceapi.ModuleDataStorage {
		if f.data == "" {
			return deviceapi.Started{}, fmt.Errorf("%w: %s was granted no data directory, so it has no %s",
				deviceapi.ErrDenied, f.standing.principal, deviceapi.ModuleDataStorage)
		}

		ctx = withModuleData(ctx, f.data)
	}

	result, err := f.fleet.StartPrint(ctx, who, params.ID, httpapi.DevicePrint{
		Storage:  params.Storage,
		Source:   params.Source,
		Path:     params.Path,
		KeepBoth: params.KeepBoth,
	})
	if err != nil {
		return deviceapi.Started{}, f.printRefusal(ctx, err, params.Path)
	}

	if result.Transfer == nil && !result.Command.OK {
		return deviceapi.Started{}, unstarted(deviceapi.ErrPrintRefused, result.Path,
			result.Command.FaultCode, result.Command.MachineReason, result.Command.CommandID)
	}

	started := deviceapi.Started{CommandID: result.Command.CommandID, Path: result.Path}
	if result.Transfer != nil {
		started.TransferID = result.Transfer.TransferID
	}

	return started, nil
}

// unstarted is the error for a start command the printer answered without
// starting the print: ErrPrintRefused for a refusal and ErrPrintFailed for a
// command it took and then failed. The HTTP route answers a refusal as a 200
// whose body says ok false, but Started has no such field, so on this wire it
// is an error: a module told nothing would read it as a print that began.
//
// It names the fault code, the printer's own reason where it gave one, and the
// command id, which device.command.settled carries for the same command.
func unstarted(sentinel error, path, code, reason, commandID string) error {
	why := "the printer gave no reason"

	switch {
	case code != "" && reason != "":
		why = code + ": " + reason
	case code != "":
		why = code
	case reason != "":
		why = reason
	}

	return fmt.Errorf("%w: %s: %s (command %s)", sentinel, path, why, commandID)
}

// failedStart is ErrPrintFailed for a start command the printer took and could
// not carry out, which the driver reports as an error and not as a refusal. A
// failed command of any other verb, such as the upload before a start, is not
// a failed start and is left to moduleRefusal.
func failedStart(err error, path string) error {
	failure, ok := errors.AsType[*fault.Fault](err)
	if !ok {
		return nil
	}

	if failure.Code() != connectorapi.CommandFailed.Code() &&
		failure.Code() != connectorapi.CommandFailedWithoutReason.Code() {
		return nil
	}

	params := failure.Params()
	if verb, _ := params.Get("verb"); verb.String() != httpapi.StartPrintVerb {
		return nil
	}

	reason, _ := params.Get("machine_reason")
	commandID, _ := params.Get("command_id")

	return unstarted(deviceapi.ErrPrintFailed, path, failure.Code().String(),
		machinetext.Clip(reason.String(), machinetext.MaxBytes), commandID.String())
}

// printRefusal maps what a print can fail with onto this contract's sentinels.
// A start command the printer took and failed is ErrPrintFailed.
//
// Every way the file can be out of reach answers absentFileMessage, word for
// word: the storage is not declared, it is not running, it holds no such file,
// it will not read the file, or the path leaves it. A sentence naming which one
// it was would let a module send one storage name after another and collect the
// names this deployment declares. The reason is in this server's log.
func (f moduleFleet) printRefusal(ctx context.Context, err error, path string) error {
	if failed := failedStart(err, path); failed != nil {
		return failed
	}

	for _, missing := range []*fault.Fault{
		fault.StorageNotFound.New(""),
		fault.StorageFileNotFound.New(""),
		fault.StorageNotReadable.New(""),
		fault.StorageInvalidPath.New(""),
	} {
		if errors.Is(err, missing) {
			f.logAbsence(ctx, err)

			return fmt.Errorf("%w: %s", deviceapi.ErrNoFile, absentFileMessage)
		}
	}

	return moduleRefusal(err)
}

// logAbsence writes the reason a module was not given the file it asked to
// print. It is the one place that reason exists, because the answer the module
// reads names nothing.
func (f moduleFleet) logAbsence(ctx context.Context, err error) {
	logs := f.log
	if logs == nil {
		logs = newRefusalLog(nil)
	}

	logs.refused(ctx, "a module was not given a file to print", f.standing.principal, err)
}

// JobHistory answers one page of the prints that have ended.
//
// It takes observe, which is what Jobs takes. A call naming no device is a page
// over every device the caller may observe, and the permission is resolved
// inside the query that produced the rows: a module granted nothing reads an
// empty page rather than a refusal, and the paging of a page it does read
// carries no count of what it may not.
func (f moduleFleet) JobHistory(
	ctx context.Context, params deviceapi.JobHistoryParams,
) (deviceapi.JobPage, error) {
	who, err := f.as(params.Principal, authz.ActionObserve)
	if err != nil {
		return deviceapi.JobPage{}, err
	}

	page, err := f.fleet.JobHistory(ctx, who, params.ID, httpapi.DeviceJobQuery{
		From:   params.From,
		To:     params.To,
		Limit:  params.Limit,
		Cursor: params.Cursor,
	})
	if err != nil {
		return deviceapi.JobPage{}, moduleRefusal(err)
	}

	items := make([]deviceapi.Job, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, *moduleJob(&item))
	}

	return deviceapi.JobPage{Items: items, Cursor: page.Cursor, Limit: page.Limit}, nil
}

// listing answers one path: what this server holds of it at once when the
// module asked for that, and the printer's answer otherwise. A path nothing
// is held of is answered with no rows while the read it starts runs.
func (f moduleFleet) listing(
	ctx context.Context, who string, params deviceapi.FilesParams,
) (httpapi.DeviceListing, error) {
	if params.Cached {
		return f.fleet.FilesHeld(ctx, who, params.ID, params.Path) //nolint:wrapcheck // moduleRefusal names it.
	}

	return f.fleet.FilesNow(ctx, who, params.ID, params.Path) //nolint:wrapcheck // moduleRefusal names it.
}

// storages is the root of a device's file tree: the storage devices it
// reported, windowed.
func (f moduleFleet) storages(
	ctx context.Context, params deviceapi.FilesParams, limit int,
) (deviceapi.Listing, error) {
	who, err := f.as(params.Principal, authz.ActionObserve)
	if err != nil {
		return deviceapi.Listing{}, err
	}

	held, err := f.fleet.Storages(ctx, who, params.ID, false)
	if err != nil {
		return deviceapi.Listing{}, moduleRefusal(err)
	}

	storages := held.Items

	low, high := pageWindow(len(storages), limit, params.Offset)

	items := make([]deviceapi.Storage, 0, high-low)
	for _, storage := range storages[low:high] {
		items = append(items, deviceapi.Storage(storage))
	}

	return deviceapi.Listing{
		Storages: items,
		Total:    len(storages),
		Limit:    limit,
		Offset:   params.Offset,
	}, nil
}

// pageWindow is the slice bounds one page covers of a list of total items. An
// offset past the end is an empty window and not a refusal, which is how the
// HTTP listing answers a page past the last one.
func pageWindow(total, limit, offset int) (int, int) {
	low := min(offset, total)
	high := min(low+limit, total)

	return low, high
}

// moduleJob renders one stored job as the contract publishes it, and nil for
// none.
func moduleJob(job *httpapi.DeviceJob) *deviceapi.Job {
	if job == nil {
		return nil
	}

	return &deviceapi.Job{
		ID:            job.ID,
		DeviceJobID:   job.DeviceJobID,
		Path:          job.Path,
		DisplayName:   job.DisplayName,
		State:         job.State,
		Progress:      job.Progress,
		TimeRemaining: job.TimeRemainingMS / millisecondsPerSecond,
		Started:       job.Started,
		Ended:         job.Ended,
		Outcome:       job.Outcome,
		ObjectCount:   job.ObjectCount,
		Objects:       moduleJobObjects(job.Objects),
	}
}

// moduleJobObjects renders a print's objects as the contract publishes them,
// and nil for none.
func moduleJobObjects(objects []httpapi.JobObject) []deviceapi.JobObject {
	if objects == nil {
		return nil
	}

	out := make([]deviceapi.JobObject, 0, len(objects))
	for _, object := range objects {
		out = append(out, deviceapi.JobObject(object))
	}

	return out
}

// moduleFile renders one entry of a device's file tree as the contract
// publishes it. The contract keeps whole seconds since the Unix epoch and the
// HTTP surface keeps an instant, so an entry the printer gave no timestamp for
// goes to a module as zero.
func moduleFile(file httpapi.DeviceFile) deviceapi.File {
	out := deviceapi.File{
		Path:        file.Path,
		Name:        file.Name,
		DisplayName: file.DisplayName,
		Folder:      file.Folder,
		Size:        file.Size,
		ReadOnly:    file.ReadOnly,

		EstimatedSeconds: file.EstimatedSeconds,
		PrinterModel:     file.PrinterModel,
		LayerHeightMM:    file.LayerHeightMM,
		Material:         file.Material,
	}

	if file.Modified != nil {
		out.Modified = file.Modified.Unix()
	}

	return out
}

// mayAdmit asks the graph whether this instance may decide admission.
//
// A graph that cannot be asked is a refusal. A host wired without one has no
// way to know the answer, so it admits no device.
func (f moduleFleet) mayAdmit(ctx context.Context, principal string) error {
	if f.grants == nil {
		return fmt.Errorf("%w: this server cannot resolve permissions", deviceapi.ErrDenied)
	}

	who, err := authz.ParseSubject(principal)
	if err != nil {
		return fmt.Errorf("%w: %s is not a principal", deviceapi.ErrDenied, principal)
	}

	object, err := authz.NewObject(authz.TypeResourceKind, ref.KindDriver.String())
	if err != nil {
		return fmt.Errorf("%w: name the driver resource kind: %w", deviceapi.ErrDenied, err)
	}

	allowed, err := f.grants.Can(ctx, who, authz.ActionAdmitDevice, object)
	if err != nil {
		return fmt.Errorf("%w: ask the authorization graph: %w", deviceapi.ErrDenied, err)
	}

	if !allowed {
		return fmt.Errorf("%w: %s holds no %s on %s",
			deviceapi.ErrDenied, principal, authz.ActionAdmitDevice, object)
	}

	return nil
}

// moduleDevice renders the listing's record as the flat view the contract
// publishes.
func moduleDevice(device httpapi.Device) deviceapi.Device {
	return deviceapi.Device{
		Kind:            device.Kind,
		ID:              device.ID,
		Name:            device.Name,
		Vendor:          device.Vendor,
		Model:           device.Model,
		Firmware:        device.Firmware,
		Serial:          device.Serial,
		Driver:          device.Driver,
		Connected:       device.Connected,
		Capabilities:    device.Capabilities,
		CapabilityEpoch: device.CapabilityEpoch,
		State:           device.State,
		Tags:            device.Tags,
		Labels:          device.Labels,
		Issues:          moduleIssues(device.Issues),
		Dialog:          moduleDialog(device),
		Retired:         device.Retired != nil,
	}
}

// device is one device as a module reads it, with what its connector declared
// about the state it is in.
func (f moduleFleet) device(device httpapi.Device) deviceapi.Device {
	out := moduleDevice(device)

	set, declared := f.states[device.Driver]
	if !declared {
		return out
	}

	state := connectorapi.StateName(jobText(device.State[printerStatePath]))
	out.Ready = device.Connected && slices.ContainsFunc(set.Transitions, func(move connectorapi.Transition) bool {
		return move.Verb == connectorapi.VerbSetPrinterReady && move.To == state
	})

	if !set.Guarded() {
		return out
	}

	out.Accepts = []string{}

	if !device.Connected {
		return out
	}

	for _, verb := range device.Capabilities {
		if set.Accepted(state, connectorapi.Capability(verb)) {
			out.Accepts = append(out.Accepts, verb)
		}
	}

	slices.Sort(out.Accepts)

	return out
}

// moduleIssues is the listing's issues as the contract publishes them.
func moduleIssues(issues []httpapi.DeviceIssue) []deviceapi.Issue {
	if len(issues) == 0 {
		return nil
	}

	out := make([]deviceapi.Issue, 0, len(issues))
	for _, issue := range issues {
		out = append(out, deviceapi.Issue{Reason: issue.Reason, Severity: issue.Severity, Detail: issue.Detail})
	}

	return out
}

// moduleDialog is the dialog the device is waiting on, read from the reserved
// dialog subtree of its state tree. The id is what says a dialog is up, as it
// is for the awaiting_operator issue, and a device that has departed carries
// none, because the issue list does not name its dialog either.
func moduleDialog(device httpapi.Device) *deviceapi.Dialog {
	if device.Departed != nil {
		return nil
	}

	return dialogInState(device.State)
}

// dialogInState is the dialog a state tree says the device is waiting on, and nil
// when its id is not there.
func dialogInState(state map[string]json.RawMessage) *deviceapi.Dialog {
	id, raised := state[deviceissue.DialogIDPath]
	if !raised {
		return nil
	}

	return &deviceapi.Dialog{
		ID:      id,
		Code:    scalarText(state[deviceissue.DialogCodePath]),
		Title:   plainText(state[deviceissue.DialogTitlePath]),
		Text:    plainText(state[deviceissue.DialogTextPath]),
		Buttons: buttonLabels(state[deviceissue.DialogButtonsPath]),
	}
}

// scalarText is a state tree value as text: a string as it reads, a number as
// it is written, and empty for anything else. A firmware may encode a code
// either way.
func scalarText(raw json.RawMessage) string {
	if text := plainText(raw); text != "" {
		return text
	}

	var number json.Number
	if json.Unmarshal(raw, &number) != nil {
		return ""
	}

	return number.String()
}

// buttonLabels is a state tree value read as a list of strings, and nil for
// anything else.
func buttonLabels(raw json.RawMessage) []string {
	var texts []string
	if len(raw) == 0 || json.Unmarshal(raw, &texts) != nil {
		return nil
	}

	return texts
}

// moduleRefusal maps the fleet's own fault onto the sentinel this contract
// publishes, so a module branches on a refusal it can act on.
//
// The match is by fault code, which is the idiom sdk/fault documents on
// Fault.Is and what makes it work through wrapping. A refusal the graph raised
// for a caller who reads the device and does not hold the class is ErrDenied,
// the same answer the ceiling gives, because to a module both mean the same
// thing: this call may not be made as this principal.
//
// A fault with no mapping does not cross. It names this host's internals, the
// host already has it in its own log, and deviceapi renders it as one sentence.
func moduleRefusal(err error) error {
	var raised *fault.Fault
	if errors.As(err, &raised) && raised.Code() == authz.CodeAccessDenied() {
		return fmt.Errorf("%w: %w", deviceapi.ErrDenied, err)
	}

	for sentinel, faults := range map[error][]*fault.Fault{
		deviceapi.ErrNoDevice: {fault.PrinterNotFound.New("")},
		deviceapi.ErrVerbUnknown: {
			fault.PrinterVerbUnknown.New(""),
			connectorapi.CapabilityNotSupported.New(""),
		},
		deviceapi.ErrNotReady: {fault.PrinterSessionEnded.New("")},
		deviceapi.ErrBusy: {
			connectorapi.CommandQueueFull.New(""),
			connectorapi.TransferBusy.New(""),
		},
		deviceapi.ErrInvalidQuery: {connectorapi.PathNotAllowed.New("")},
		deviceapi.ErrNameTaken:    {fault.PrinterFileNameTaken.New("")},
		deviceapi.ErrArgument: {
			connectorapi.ArgumentUnknown.New(""),
			connectorapi.ArgumentMissing.New(""),
			connectorapi.ArgumentNotSupported.New(""),
			connectorapi.ArgumentMalformed.New(""),
			connectorapi.ArgumentOutOfRange.New(""),
		},
	} {
		for _, known := range faults {
			if errors.Is(err, known) {
				return fmt.Errorf("%w: %w", sentinel, err)
			}
		}
	}

	return err
}
