package app

// The device plane's projection onto the API surface. Every function here turns
// what the plane holds into a shape internal/transport/http/httpapi publishes,
// and none of them decides anything.
//
// The projection lives in the composition root because that is the only package
// allowed to see both sides of it. internal/transport may not import
// internal/platform, so a health.Rollup becomes an httpapi.ConnectorRollup
// here.

import (
	"encoding/json"
	"maps"
	"strings"
	"time"

	"github.com/prusa3d/prusa-connect/internal/authz"
	"github.com/prusa3d/prusa-connect/internal/kernel/machinetext"
	"github.com/prusa3d/prusa-connect/internal/kernel/ref"
	"github.com/prusa3d/prusa-connect/internal/platform/observability/health"
	"github.com/prusa3d/prusa-connect/internal/transport/http/httpapi"
	"github.com/prusa3d/prusa-connect/sdk/connectorapi"
	"github.com/prusa3d/prusa-connect/sdk/fault"
)

// presenceState is the device plane's two-valued reachability as the rollup's
// vocabulary. A printer whose session is open and whose link is down is
// disconnected rather than unknown: the driver keeps the session across a
// disconnection, so the host is not guessing about it.
func presenceState(connected bool) health.State {
	if connected {
		return health.StateConnected
	}

	return health.StateDisconnected
}

// projectRollup maps the rollup onto the wire shape. The mapping lives here
// because the HTTP transport may not import the platform group, and this is
// the composition root.
func projectRollup(rollup health.Rollup) httpapi.ConnectorRollup {
	out := httpapi.ConnectorRollup{
		Connector:         rollup.Connector,
		Status:            string(rollup.Status),
		Counts:            make(map[string]int, len(rollup.Counts)),
		Degraded:          make([]httpapi.ConnectorDevice, 0, len(rollup.Degraded)),
		DegradedTruncated: rollup.DegradedTruncated,
		Total:             rollup.Total,
	}

	for state, count := range rollup.Counts {
		out.Counts[string(state)] = count
	}

	for _, device := range rollup.Degraded {
		out.Degraded = append(out.Degraded, httpapi.ConnectorDevice{
			ID:       device.ID,
			Name:     device.Name,
			State:    string(device.State),
			Reason:   device.Reason,
			Since:    optionalTime(device.Since),
			LastSeen: optionalTime(device.LastSeen),
			Attempts: device.Attempts,
		})
	}

	return out
}

// optionalTime reports a zero timestamp as absent. A zero time on the wire
// reads as 1970, and a client rendering it says every device has been broken
// since then.
func optionalTime(at time.Time) *time.Time {
	if at.IsZero() {
		return nil
	}

	return &at
}

// refusalReason is the printer's own word for a refusal, and it is empty for a
// command the printer carried out.
//
// The driver contract already says the field is refusal-only and
// connectorapitest fails a driver that breaks it, so the test on OK looks
// redundant. It is here because a driver is third-party code: a caller
// holding both "the printer did it" and "here is why it did not" would act on
// whichever it read first, and this API can hold the rule for every driver
// rather than trusting each one to.
//
// It is the one place a machine reason becomes an answer, so it is where the
// bound on it goes. The command surface, the raw gcode surface and the
// developer command log all read what this returns.
func refusalReason(result connectorapi.Result) string {
	if result.OK {
		return ""
	}

	return machinetext.Clip(result.MachineReason, machinetext.MaxBytes)
}

// refusalCode is the registered code for a refusal the printer decided, and it
// is empty for a command the printer carried out.
//
// It reads the result rather than an error because that is where the driver
// contract puts a refusal: see connectorapi.Result. It guards OK and clips for
// the reason refusalReason does, because the value is a driver's and a driver
// is third-party code.
func refusalCode(result connectorapi.Result) string {
	if result.OK {
		return ""
	}

	return machinetext.Clip(result.FaultCode, machinetext.MaxBytes)
}

// commandAnswer is what the machine said back, read out of the terminal event
// the driver preserved in Result.Raw.
//
// It is read here and not in the driver because Raw is the vendor's own reply
// and nothing in the contract names a key inside it. A device that answers a
// G-code line in words puts the text under `answer`, and a device that answers
// nothing leaves the field empty, which is what real Buddy firmware does: it
// acknowledges a G frame and sends no reply text.
//
// A refused command carries no answer. The refusal already has its own two
// fields, and text a driver happened to leave in Raw beside a refusal is not
// the machine's word for why.
//
// The bound is machinetext's, for the reason refusalReason carries one: this is
// text a device chose and it reaches an HTTP answer and a log line.
func commandAnswer(result connectorapi.Result) string {
	if !result.OK || len(result.Raw) == 0 {
		return ""
	}

	var event struct {
		Data struct {
			Answer string `json:"answer"`
		} `json:"data"`
	}

	if err := json.Unmarshal(result.Raw, &event); err != nil {
		// A reply this server cannot read is a reply it does not show. The
		// command itself succeeded, and failing it over the shape of a field
		// nothing branches on would turn a working printer into a broken one.
		return ""
	}

	return machinetext.Clip(event.Data.Answer, machinetext.MaxBytes)
}

// storageOf renders one storage device.
//
// Free space is a pointer on the wire and a value plus a flag in the contract,
// because JSON has null and Go's zero value does not: a printer that reported
// no figure and one with nothing left must not read the same to a client.
func storageOf(device connectorapi.Storage) httpapi.DeviceStorage {
	out := httpapi.DeviceStorage{
		Mountpoint: device.Mountpoint,
		Name:       device.Name,
		Type:       device.Type,
		ReadOnly:   device.ReadOnly,
	}

	if device.FreeSpaceKnown {
		free := device.FreeSpace
		out.FreeSpace = &free
	}

	return out
}

// fileOf renders one entry on the wire. The connector reports whole seconds
// since the Unix epoch, and zero there is a board that reported no timestamp at
// all, so the entry goes out with no modified member rather than with 1970.
func fileOf(file connectorapi.File) httpapi.DeviceFile {
	out := httpapi.DeviceFile{
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

	if file.Modified > 0 {
		modified := time.Unix(file.Modified, 0).UTC()
		out.Modified = &modified
	}

	return out
}

// render is the entry on the wire. name is the display name the directory
// holds, and the operator's own name on the entry is preferred over it; a
// device with neither falls back to what the printer said it is. held is what the
// caller this row is being rendered for may do to it, conferred is the dangerous
// verbs a grant or ownership gives that caller on it, and epoch is the
// generation of the graph that answer was read at.
//
// found is what the host has found wrong with this printer, and link is the
// reading it was judged from. Both are parameters rather than calls from in
// here, for two reasons. The listing assesses every printer the caller may see
// and renders only the page, so assessing again would let one row disagree with
// the rollup printed beside it. And link.observe rolls a window as it answers,
// so reading it twice for one record is two readings of one accumulator.
func (e *device) render(
	name string, held []authz.Action, conferred []string, epoch uint64,
	found []httpapi.DeviceIssue, link httpapi.DeviceLink, prints *printBook, resident bool,
) httpapi.Device {
	// A device read back from the record store holds no session, and it reports
	// no capability and no epoch. A capability set is resolved when the machine
	// arrives, and the epoch moves on every arrival, so a persisted one would
	// be a claim about a machine this process has not spoken to. Every
	// operation that has to reach the machine refuses such an entry before
	// this is called.
	//
	// Both answers are taken through the two accessors that read one field
	// each. Asking for the capability set itself would deep-copy it per row,
	// and that copy is a large allocation on the listing path.
	verbs := []string{}

	var (
		capabilityEpoch uint64
		profileMatched  bool
	)

	if e.session != nil {
		verbs, capabilityEpoch = e.session.CapabilityNames()
		profileMatched = e.session.ProfileMatched()
	}

	// The operator's own name wins over the directory's, which register rewrites
	// from the reported model and serial on every arrival. A device nobody named
	// keeps reading under that, and one nothing holds a directory row for reads
	// under what the machine said it is.
	name = e.displayedName(name)

	departed := e.departure()

	// The tree is taken once and the print is read out of that copy. Taking it
	// twice would be two readings of one machine, and the second could carry a
	// job the first did not.
	state := e.snapshot()
	tools, volume := toolFactsOf(e.identity.Model, state)

	dangerous := make(map[string]bool, len(conferred))
	for _, verb := range conferred {
		dangerous[verb] = true
	}

	return httpapi.Device{
		// The plane names the kind, because the plane is what admitted the
		// device and knows what type it is.
		Kind: ref.KindPrinter.String(),

		ID:              e.id,
		Name:            name,
		Vendor:          e.identity.Vendor,
		Model:           e.identity.Model,
		Firmware:        firmwareText(e.identity.Firmware),
		Serial:          serialOf(e.identity),
		UnitUID:         e.identity.UnitUID,
		Driver:          e.driver,
		Connected:       e.connected(),
		Departed:        departed,
		Resident:        resident,
		Retired:         e.retirementRow(),
		Capabilities:    verbs,
		CapabilityEpoch: capabilityEpoch,

		Actions:            authz.ActionNames(held),
		Verbs:              authz.VerbsReached(authz.DeviceKindPrinter, authz.MaskOf(held), dangerous),
		AuthorizationEpoch: epoch,

		ProfileMatched: profileMatched,
		Tags:           e.tagList(),
		Labels:         e.labelSet(),
		State:          state,
		Job:            jobOf(state, prints, e.id),
		Tools:          tools,
		BuildVolume:    volume,
		Link:           link,
		Issues:         found,
	}
}

// retirementRow is why this id stopped naming a machine, and reports nil while
// it still does.
func (e *device) retirementRow() *httpapi.DeviceRetirement {
	reason, at, retired := e.retirement()
	if !retired {
		return nil
	}

	return &httpapi.DeviceRetirement{Reason: reason, At: at}
}

// departure is why this printer's session ended, and reports nil while it is
// open.
func (e *device) departure() *httpapi.DeviceDeparture {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.gone == "" {
		return nil
	}

	return &httpapi.DeviceDeparture{Reason: e.gone, At: e.goneAt}
}

// departedAt is when this printer's session ended, and is the zero time while
// it is open.
func (e *device) departedAt() time.Time {
	e.mu.Lock()
	defer e.mu.Unlock()

	return e.goneAt
}

func (e *device) snapshot() httpapi.DeviceState {
	e.mu.Lock()
	defer e.mu.Unlock()

	out := make(httpapi.DeviceState, len(e.state))
	maps.Copy(out, e.state)

	return out
}

// serialOf is what a person matches a machine by, and it falls back to the unit
// id for a printer that reported no serial number.
//
// The fallback is not a guess. Buddy 6.5.7 and 6.8.1 send no sn when they
// register, so Identity.Serial is empty for them, while the fingerprint they
// do send arrives as Identity.UnitUID. Rendering the empty string would leave
// an operator a column of blanks on a fleet of machines that have each told
// this server exactly what they are.
//
// Nothing is lost by the fallback, because the projection publishes unit_uid
// beside the serial: a reader that has to know which of the two it is holding
// compares them. The stored device record keeps the two fields as the printer
// reported them, so the record still says the serial was never sent.
func serialOf(identity connectorapi.Identity) string {
	if identity.Serial != "" {
		return identity.Serial
	}

	return identity.UnitUID
}

// displayName is what a person reads in the listing until somebody renames the
// printer, which is the model and the serial it reported.
func displayName(id string, identity connectorapi.Identity) string {
	name := strings.TrimSpace(identity.Model + " " + serialOf(identity))
	if name == "" {
		name = id
	}

	if len(name) > printerNameLimit {
		name = name[:printerNameLimit]
	}

	return name
}

// firmwareText renders a reported firmware version for display.
//
// It goes through MarshalText rather than String because the forbidigo rule on
// that method keeps a version comparison out of every package but the profile
// resolver and the capability resolver, and this is a rendering: nothing here
// orders it, and behaviour comes from the resolved capability set.
func firmwareText(label connectorapi.FirmwareLabel) string {
	text, err := label.MarshalText()
	if err != nil {
		return ""
	}

	return string(text)
}

// notFound is the one refusal this surface has for an id it will not talk
// about. A printer the caller holds nothing on is reported exactly as one this
// server has never been dialled by, so a refusal never tells anybody a printer
// exists.
func notFound(id string) error {
	return fault.PrinterNotFound.New("this server has no printer with that id").
		With(fault.StringParam("device_id", id))
}
