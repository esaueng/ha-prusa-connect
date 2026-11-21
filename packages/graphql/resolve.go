package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/graphql-go/graphql"

	"github.com/prusa3d/graphql/internal/schema"

	"github.com/prusa3d/prusa-connect/sdk/deviceapi"
)

// The resolvers, one per root field.
//
// Each one reads the fleet through sdk/deviceapi and shapes the answer into
// the core's own record. Nothing here opens a socket and nothing here holds a
// credential: the call goes back down the pipe the host launched this process
// on, carrying the principal the host resolved for the request being served,
// and the authorization graph answers for that person before a row is read.
//
// Every answer is plain JSON values, so the library reads a member by the name
// the core spelled it with and no member name is written twice.

// principalKey is how the request's principal reaches a resolver. The HTTP
// handler puts it on the context and nothing else writes it.
type principalKey struct{}

// withPrincipal carries who the host said is calling into the resolvers.
func withPrincipal(ctx context.Context, principal string) context.Context {
	return context.WithValue(ctx, principalKey{}, principal)
}

// principalOf is who a resolver makes its call as. An empty principal would be
// the module's own work, which no field here does, so a resolver that found
// one refuses rather than reading the fleet as the instance.
func principalOf(ctx context.Context) (string, error) {
	principal, carried := ctx.Value(principalKey{}).(string)
	if !carried || principal == "" {
		return "", errors.New("the host named no principal for this request")
	}

	return principal, nil
}

// resolversFor binds every root field in the model to the fleet.
func resolversFor(fleet *deviceapi.Client) schema.Resolvers {
	return schema.Resolvers{
		"Query.devices":                 resolveDevices(fleet),
		"Query.device":                  resolveDevice(fleet),
		"Query.device_jobs":             resolveDeviceJobs(fleet),
		"Query.device_files":            resolveDeviceFiles(fleet),
		"Query.device_storages":         resolveDeviceStorages(fleet),
		"Mutation.send_device_command":  resolveSendCommand(fleet),
		"Mutation.approve_registration": resolveApprove(fleet),
	}
}

// resolveDevices answers one page of the devices the caller may see. The page
// is the core's own: the host applies the caller's visibility inside the query
// that produces the rows, so total counts what the caller may see and not what
// the fleet holds.
func resolveDevices(fleet *deviceapi.Client) graphql.FieldResolveFn {
	return func(p graphql.ResolveParams) (any, error) {
		principal, err := principalOf(p.Context)
		if err != nil {
			return nil, err
		}

		page, err := fleet.List(p.Context, deviceapi.ListParams{
			Principal: principal,
			Limit:     intArg(p, "limit"),
			Offset:    intArg(p, "offset"),
			Tags:      stringsArg(p, "tag"),
			Labels:    stringsArg(p, "labels"),
		})
		if err != nil {
			return nil, refuse(err, map[string]any{})
		}

		items := make([]any, 0, len(page.Items))
		for _, device := range page.Items {
			items = append(items, renderDevice(device))
		}

		return map[string]any{
			"items":  items,
			"total":  page.Total,
			"limit":  page.Limit,
			"offset": page.Offset,
		}, nil
	}
}

// resolveDevice answers one device.
func resolveDevice(fleet *deviceapi.Client) graphql.FieldResolveFn {
	return func(p graphql.ResolveParams) (any, error) {
		principal, err := principalOf(p.Context)
		if err != nil {
			return nil, err
		}

		id := stringArg(p, "device_id")

		device, err := fleet.Get(p.Context, deviceapi.GetParams{Principal: principal, ID: id})
		if err != nil {
			return nil, refuse(err, map[string]any{"device_id": id})
		}

		return renderDevice(device), nil
	}
}

// resolveDeviceJobs answers what one device is printing and what it printed
// last.
func resolveDeviceJobs(fleet *deviceapi.Client) graphql.FieldResolveFn {
	return func(p graphql.ResolveParams) (any, error) {
		principal, err := principalOf(p.Context)
		if err != nil {
			return nil, err
		}

		id := stringArg(p, "device_id")

		jobs, err := fleet.Jobs(p.Context, deviceapi.JobsParams{Principal: principal, ID: id})
		if err != nil {
			return nil, refuse(err, map[string]any{"device_id": id})
		}

		return map[string]any{
			"current": renderJob(jobs.Current),
			"last":    renderJob(jobs.Last),
		}, nil
	}
}

// errRootIsStorages reports a files read with no path. The core's own listing
// answers the storages there and this field answers a directory, so the two
// are separate fields here and the refusal says which one to ask.
var errRootIsStorages = errors.New(
	"a device's file tree has no directory at its root: ask device_storages for what is mounted, " +
		"then device_files with one of those names as the path")

// resolveDeviceFiles answers one directory of a device's own file tree.
func resolveDeviceFiles(fleet *deviceapi.Client) graphql.FieldResolveFn {
	return func(p graphql.ResolveParams) (any, error) {
		principal, err := principalOf(p.Context)
		if err != nil {
			return nil, err
		}

		id, path := stringArg(p, "device_id"), stringArg(p, "path")
		if path == "" {
			return nil, refuse(fmt.Errorf("%w: %w", deviceapi.ErrInvalidQuery, errRootIsStorages),
				map[string]any{"device_id": id})
		}

		listing, err := fleet.Files(p.Context, deviceapi.FilesParams{
			Principal: principal, ID: id, Path: path,
		})
		if err != nil {
			return nil, refuse(err, map[string]any{"device_id": id, "path": path})
		}

		if listing.Entry == nil {
			return nil, refuse(fmt.Errorf("%w: %w", deviceapi.ErrInvalidQuery, errRootIsStorages),
				map[string]any{"device_id": id, "path": path})
		}

		items := make([]any, 0, len(listing.Items))
		for _, file := range listing.Items {
			items = append(items, renderFile(file))
		}

		return map[string]any{"directory": renderFile(*listing.Entry), "items": items}, nil
	}
}

// resolveDeviceStorages answers the storage devices one printer reported. It
// reads the root of the same wire call the files field reads, which is what
// the core's own storages operation answers with.
func resolveDeviceStorages(fleet *deviceapi.Client) graphql.FieldResolveFn {
	return func(p graphql.ResolveParams) (any, error) {
		principal, err := principalOf(p.Context)
		if err != nil {
			return nil, err
		}

		id := stringArg(p, "device_id")

		listing, err := fleet.Files(p.Context, deviceapi.FilesParams{Principal: principal, ID: id})
		if err != nil {
			return nil, refuse(err, map[string]any{"device_id": id})
		}

		out := make([]any, 0, len(listing.Storages))
		for _, storage := range listing.Storages {
			out = append(out, map[string]any{
				"mountpoint": storage.Mountpoint,
				"name":       storage.Name,
				"type":       storage.Type,
				"free_space": optionalInt64(storage.FreeSpace),
				"read_only":  storage.ReadOnly,
			})
		}

		return map[string]any{"items": out}, nil
	}
}

// resolveSendCommand sends one verb to one device.
//
// A device that answered and refused is an answer and not an error: the
// PrinterResult carries ok false and the machine's own word for why. Only a
// call that never reached the device leaves as a GraphQL error.
func resolveSendCommand(fleet *deviceapi.Client) graphql.FieldResolveFn {
	return func(p graphql.ResolveParams) (any, error) {
		principal, err := principalOf(p.Context)
		if err != nil {
			return nil, err
		}

		id, verb := stringArg(p, "device_id"), stringArg(p, "verb")

		result, err := fleet.Command(p.Context, deviceapi.CommandParams{
			Principal: principal, ID: id, Verb: verb, Args: textArgs(p, "args"),
		})
		if err != nil {
			return nil, refuse(err, map[string]any{"device_id": id, "verb": verb})
		}

		return map[string]any{
			"ok":             result.OK,
			"machine_reason": optionalString(result.MachineReason),
			"ignored":        result.Ignored,
			"substituted":    optionalMap(result.Substituted),
		}, nil
	}
}

// resolveApprove admits one device that is waiting to be let onto this server.
func resolveApprove(fleet *deviceapi.Client) graphql.FieldResolveFn {
	return func(p graphql.ResolveParams) (any, error) {
		principal, err := principalOf(p.Context)
		if err != nil {
			return nil, err
		}

		code := stringArg(p, "code")

		registration, err := fleet.Approve(p.Context, deviceapi.ApproveParams{
			Principal: principal, Code: code,
		})
		if err != nil {
			return nil, refuse(err, map[string]any{"code": code})
		}

		return map[string]any{
			"code":        registration.Code,
			"connector":   registration.Connector,
			"fingerprint": registration.Fingerprint,
			"serial":      optionalString(registration.Serial),
			"model":       optionalString(registration.Model),
			"firmware":    optionalString(registration.Firmware),
			"approved":    registration.Approved,
		}, nil
	}
}

// renderDevice shapes one device the way the core's own Device record is
// shaped. Every member here is one the generated schema declares, and the
// generator refuses a schema whose members and this function's keys could
// disagree: a member the wire cannot fill is excused in the binding table and
// is not in the schema at all.
func renderDevice(device deviceapi.Device) map[string]any {
	return map[string]any{
		"kind":             device.Kind,
		"id":               device.ID,
		"name":             device.Name,
		"vendor":           device.Vendor,
		"model":            device.Model,
		"firmware":         device.Firmware,
		"serial":           device.Serial,
		"driver":           device.Driver,
		"connected":        device.Connected,
		"capabilities":     device.Capabilities,
		"capability_epoch": device.CapabilityEpoch,
		"state":            openObject(device.State),
		"tags":             openList(device.Tags),
		"labels":           openMap(device.Labels),
	}
}

// renderJob shapes one print, and answers nothing for a device that has none.
//
// The wire counts the remaining time in seconds and the core's record counts
// it in milliseconds. The record is what the schema was generated from, so the
// conversion happens here and a client reads one unit whichever surface it
// asks.
func renderJob(job *deviceapi.Job) any {
	if job == nil {
		return nil
	}

	return map[string]any{
		"id":                job.ID,
		"path":              job.Path,
		"display_name":      job.DisplayName,
		"state":             job.State,
		"progress":          job.Progress,
		"time_remaining_ms": job.TimeRemaining * int64(time.Second/time.Millisecond),
		"started":           job.Started.UTC().Format(time.RFC3339),
		"ended":             optionalTime(job.Ended),
	}
}

// renderFile shapes one entry in a printer's own file tree.
//
// The wire reports the modification time as seconds since the Unix epoch and
// the core's record as an RFC 3339 string. A board with no clock reports zero,
// which is not a time, so it is answered as absent rather than as 1970.
func renderFile(file deviceapi.File) map[string]any {
	var modified any
	if file.Modified > 0 {
		modified = time.Unix(file.Modified, 0).UTC().Format(time.RFC3339)
	}

	return map[string]any{
		"path":         file.Path,
		"name":         file.Name,
		"display_name": optionalString(file.DisplayName),
		"folder":       file.Folder,
		"size":         file.Size,
		"modified":     modified,
		"read_only":    file.ReadOnly,

		"estimated_seconds": optional(file.EstimatedSeconds),
		"printer_model":     optional(file.PrinterModel),
		"layer_height_mm":   optional(file.LayerHeightMM),
		"material":          optional(file.Material),
	}
}

// openObject answers a state tree, and an empty object for a device that
// reported none. The schema declares it as always there, so a nil map would be
// an answer the schema does not allow.
func openObject(state map[string]json.RawMessage) map[string]json.RawMessage {
	if state == nil {
		return map[string]json.RawMessage{}
	}

	return state
}

// openList answers an empty list for a device with no tags, so a client reads
// [] and never null.
func openList(values []string) []string {
	if values == nil {
		return []string{}
	}

	return values
}

// openMap answers a label set, and an empty object for a device nobody has
// labelled.
func openMap(labels map[string]string) map[string]string {
	if labels == nil {
		return map[string]string{}
	}

	return labels
}

// optionalString answers nothing for an empty value, because the schema
// declares these members as absent rather than as empty text.
func optionalString(value string) any {
	if value == "" {
		return nil
	}

	return value
}

// optionalMap answers nothing for an empty map, for the same reason.
func optionalMap(value map[string]string) any {
	if len(value) == 0 {
		return nil
	}

	return value
}

// optionalInt64 answers nothing for a figure the printer did not report. Zero
// is a full device and absent is a device that said nothing, and only one of
// the two makes a write fail.
func optionalInt64(value *int64) any {
	if value == nil {
		return nil
	}

	return *value
}

// optionalTime answers nothing for a print that is still running.
func optionalTime(at *time.Time) any {
	if at == nil {
		return nil
	}

	return at.UTC().Format(time.RFC3339)
}

// stringArg reads one text argument, and empty for one the caller left out.
func stringArg(p graphql.ResolveParams, name string) string {
	value, _ := p.Args[name].(string)

	return value
}

// intArg reads one whole-number argument. The Int64 scalar hands over an
// int64, and an absent argument is zero, which the wire reads as its own
// default.
func intArg(p graphql.ResolveParams, name string) int {
	switch held := p.Args[name].(type) {
	case int64:
		return int(held)
	case int:
		return held
	}

	return 0
}

// stringsArg reads one list-of-text argument.
func stringsArg(p graphql.ResolveParams, name string) []string {
	held, carried := p.Args[name].([]any)
	if !carried {
		return nil
	}

	out := make([]string, 0, len(held))

	for _, item := range held {
		if text, ok := item.(string); ok {
			out = append(out, text)
		}
	}

	return out
}

// textArgs reads the command arguments, which the capability descriptor checks
// as text. A number written into the query is rendered the way the core's own
// JSON body would carry it.
func textArgs(p graphql.ResolveParams, name string) map[string]string {
	held, carried := p.Args[name].(map[string]any)
	if !carried {
		return nil
	}

	out := make(map[string]string, len(held))

	for key, value := range held {
		if text, ok := value.(string); ok {
			out[key] = text

			continue
		}

		out[key] = fmt.Sprintf("%v", value)
	}

	return out
}

// optional answers nothing for a fact a file does not name: the zero value.
func optional[T comparable](value T) any {
	var zero T
	if value == zero {
		return nil
	}

	return value
}
