package app

// Printing a file in one call. The HTTP route and the module wire both arrive
// here, so the transfer and the start command are composed once.

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"time"

	"github.com/prusa3d/prusa-connect/internal/authz"
	"github.com/prusa3d/prusa-connect/internal/transport/http/httpapi"
	"github.com/prusa3d/prusa-connect/sdk/connectorapi"
	"github.com/prusa3d/prusa-connect/sdk/fault"
)

// detachedPrintBudget bounds a print the caller has stopped waiting for. The
// transfer, the start command and everything between them run inside it, and a
// file that has not reached the printer by then is abandoned with the transfer
// recorded as failed.
//
// It exists because the caller's context is gone: a module that asked for a
// print and was answered cannot cancel one, so something has to end the work.
// Stopping a transfer before it expires is the stop route's job.
const detachedPrintBudget = 2 * time.Hour

// maxDetachedPrints is how many prints this server relays at once with nobody
// waiting for them.
//
// A caller that asked for a print is answered as soon as the transfer opens,
// and the relay and the start command then run on a goroutine of this plane's
// own for up to detachedPrintBudget. Nothing on the caller's side slows it
// down: it can ask again the moment it is answered. Without a limit a module
// asking in a loop would put a goroutine and an open file on this server for
// every ask. A caller over HTTP that waits for its own upload is not counted
// here, because the HTTP server already bounds how many requests it holds.
const maxDetachedPrints = 16

// claimDetached takes one of the places for a print nobody is waiting for, and
// reports whether there was one free.
func (r *transferRegister) claimDetached() bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.detached >= maxDetachedPrints {
		return false
	}

	r.detached++

	return true
}

// releaseDetached gives one place back, once the print has ended. A release
// with nothing claimed changes nothing, so a caller that releases twice cannot
// lift the limit.
func (r *transferRegister) releaseDetached() {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.detached > 0 {
		r.detached--
	}
}

// Print relays a file onto a printer and starts it.
//
// It composes the two operations that already exist rather than reaching past
// them: Send relays the bytes and Command starts the print, and both run their
// own permission checks in that order. So a caller who may not write to the
// machine is refused before anything is transferred, and this is not a second
// door onto either.
//
// It returns when the printer has answered the start command, or when the
// caller stops waiting. A caller that leaves while the file moves still gets
// the print it asked for: the relay and the start command run on together, as
// outliveCaller says.
func (d *devices) Print(
	ctx context.Context, principal string, id string, request httpapi.DevicePrint,
) (httpapi.DevicePrintResult, error) {
	return outliveCaller(ctx, d, id, request.Path, func(relaying context.Context) (httpapi.DevicePrintResult, error) {
		return d.printing(relaying, principal, id, request, nil)
	})
}

// printing is Print, telling opened the transfer as soon as its record is
// written. opened is nil for a caller that waits for the whole print.
func (d *devices) printing(
	ctx context.Context, principal string, id string, request httpapi.DevicePrint,
	opened func(httpapi.DeviceTransfer),
) (httpapi.DevicePrintResult, error) {
	if err := httpapi.ValidatePrint(request); err != nil {
		return httpapi.DevicePrintResult{}, err
	}

	out := httpapi.DevicePrintResult{Path: request.Path}

	if request.Storage != "" {
		transfer, err := d.send(ctx, principal, id, httpapi.DeviceUpload(request), opened, true)

		switch {
		case errors.Is(err, errHeldSame):
			// The printer holds this file already, so the print starts on it.
		case err != nil:
			return httpapi.DevicePrintResult{}, err
		default:
			out.Transfer = &transfer
			// keep_both may have sent it under a free name.
			out.Path = transfer.Path
		}
	}

	result, err := d.Command(ctx, principal, id, httpapi.PrinterCommand{
		Verb: httpapi.StartPrintVerb,
		Args: map[string]string{httpapi.StartPrintPathArgument: out.Path},
	})
	if err != nil {
		return httpapi.DevicePrintResult{}, err
	}

	out.Command = result

	return out, nil
}

// StartPrint begins a print and answers once the transfer has opened.
//
// A print of a file the printer already holds moves nothing, so that one runs
// here and answers what the machine made of the start command. A print from a
// storage answers with the transfer's id, and the relay and the start command
// then run on a goroutine of this plane's own, under detachedPrintBudget.
//
// Both permissions are decided before anything is detached, so a caller who may
// not operate the printer or may not read the storage is told so rather than
// being answered and refused out of sight. The detached half checks them again,
// because Send and Command are the same calls the HTTP route makes.
//
// Two limits hold how much a caller can start. One printer fetches one file at
// a time, which the transfer register holds, and this server relays at most
// maxDetachedPrints files with nobody waiting for them. Over either, the answer
// is core.printer/TRANSFER_BUSY and the caller may ask again.
func (d *devices) StartPrint(
	ctx context.Context, principal string, id string, request httpapi.DevicePrint,
) (httpapi.DevicePrintResult, error) {
	if err := httpapi.ValidatePrint(request); err != nil {
		return httpapi.DevicePrintResult{}, err
	}

	if request.Storage == "" {
		return d.printing(ctx, principal, id, request, nil)
	}

	if _, err := d.reachLive(ctx, principal, id, authz.ActionOperate); err != nil {
		return httpapi.DevicePrintResult{}, err
	}

	if err := permitStorageContents(ctx, d.graph, principal, authz.ActionReadStorage); err != nil {
		return httpapi.DevicePrintResult{}, err
	}

	if !d.transfers.claimDetached() {
		return httpapi.DevicePrintResult{}, connectorapi.TransferBusy.
			New("this server is already relaying as many files as it takes at once").
			With(fault.StringParam("path", request.Path)).
			Wrap(connectorapi.ErrTransferBusy)
	}

	// Buffered, so the goroutine never blocks on a caller that has gone.
	// printed answers a print of a file the printer already held, which opens
	// no transfer.
	started := make(chan httpapi.DeviceTransfer, 1)
	failed := make(chan error, 1)
	printed := make(chan httpapi.DevicePrintResult, 1)

	// Detached from the caller's context and bounded by its own deadline: the
	// caller is answered while the file is still moving, so its context is gone
	// long before the transfer is.
	relaying, stop := context.WithTimeout(context.WithoutCancel(ctx), detachedPrintBudget)

	go func() {
		transfer := ""

		defer d.transfers.releaseDetached()
		defer stop()

		// A driver is code this project did not write, and a panic in it comes
		// up through the relay to the top of this goroutine. Nothing above a
		// goroutine catches one, so the process would end and every printer
		// this server holds would go with it. It is written down here and goes
		// no further. The record of the transfer is already closed by the relay.
		defer func() {
			recovered := recover()
			if recovered == nil {
				return
			}

			// The caller is answered as well, for a panic raised before the
			// transfer opened. One that comes later finds the caller gone and
			// the buffered place free, so neither send waits.
			select {
			case failed <- driverStopped(request.Path):
			default:
			}

			// The stack is written beside the panic value, because this
			// goroutine is the only place that still holds where the driver
			// stopped.
			d.log.ErrorContext(relaying, "a driver stopped while relaying a print",
				"device_id", id, "transfer_id", transfer, "storage", request.Storage,
				"path", request.Path, "panic", fmt.Sprint(recovered),
				"stack", string(debug.Stack()))
		}()

		result, err := d.printing(relaying, principal, id, request, func(opened httpapi.DeviceTransfer) {
			transfer = opened.TransferID
			started <- opened
		})
		if err == nil && transfer == "" {
			printed <- result
		}

		if err != nil {
			failed <- err

			d.log.WarnContext(relaying, "a print started for a module did not finish",
				"device_id", id, "storage", request.Storage, "path", request.Path, "error", err.Error())
		}
	}()

	select {
	case transfer := <-started:
		return httpapi.DevicePrintResult{Path: transfer.Path, Transfer: &transfer}, nil
	case result := <-printed:
		return result, nil
	case err := <-failed:
		return httpapi.DevicePrintResult{}, err
	case <-ctx.Done():
		return httpapi.DevicePrintResult{}, ctx.Err()
	}
}
