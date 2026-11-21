package runner

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/prusa3d/prusa-connect/sdk/busapi"
	"github.com/prusa3d/prusa-connect/sdk/event"

	"github.com/prusa3d/simple-queue/internal/queue"
)

// Feed opens the host's event stream. The adapter in main wraps
// busapi.Subscribe over the module's connection, and a test feeds windows
// itself.
type Feed interface {
	Subscribe(ctx context.Context, params busapi.SubscribeParams, onWindow func(busapi.Window) error) error
}

const (
	// eventBuffer is how many events wait for the loop. The stream blocks
	// once it is full, which is the back pressure the host expects.
	eventBuffer = 64
	// retryAfter is the pause before a closed stream is opened again.
	retryAfter = 5 * time.Second
	// windowMS is the coalescing window the queue asks for. The queue reads
	// durable events only, so a long window costs nothing but latency.
	windowMS = 1000
	// startsHeld is how many start commands the queue remembers while it
	// waits for how they ended. A command whose end never arrives is
	// forgotten with the rest once there are more.
	startsHeld = 256
)

// watched is the events the queue reads: a printer whose state changed, a
// print that began, a print that ended, a transfer that began and how it
// ended, and a start command and how it ended.
var watched = []string{
	event.TypeDeviceStateChanged,
	event.TypeDeviceJobStarted,
	event.TypeDeviceJobSettled,
	event.TypeDeviceTransferStarted,
	event.TypeDeviceTransferSettled,
	event.TypeDeviceCommandRequested,
	event.TypeDeviceCommandSettled,
	event.TypeDeviceFilesChanged,
}

// news is one thing the stream said: an event about a printer, or that the
// stream lost continuity and the queues should be compared with the printers.
type news struct {
	device   string
	envelope event.Envelope
	resync   bool
}

// streamState is the place the stream resumes from after it closed. It lives
// only in this process: after a restart the sweep compares every queue with
// its printer, which covers what happened in between.
type streamState struct {
	cursor string
}

// follow reads the event stream into events until ctx ends, and opens it
// again whenever it closes.
func (r *Runner) follow(ctx context.Context, feed Feed, events chan<- news) {
	defer close(events)

	for ctx.Err() == nil {
		err := feed.Subscribe(ctx, busapi.SubscribeParams{
			Types:      watched,
			Resume:     r.stream.cursor,
			IntervalMS: windowMS,
		}, func(window busapi.Window) error {
			return r.window(ctx, window, events)
		})
		if err != nil && ctx.Err() == nil && !errors.Is(err, context.Canceled) {
			r.log("simple-queue: the event stream closed: %v\n", err)
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(retryAfter):
		}
	}
}

// window hands each event of one window to the loop and keeps the cursor.
func (r *Runner) window(ctx context.Context, window busapi.Window, events chan<- news) error {
	if window.Resync {
		if err := send(ctx, events, news{resync: true}); err != nil {
			return err
		}
	}

	for _, raw := range window.Events {
		var envelope event.Envelope
		if json.Unmarshal(raw, &envelope) != nil {
			continue
		}

		origin, err := event.ParseOrigin(envelope.Subject)
		if err != nil || origin.Kind() != event.KindDevice {
			continue
		}

		if err = send(ctx, events, news{device: origin.ID(), envelope: envelope}); err != nil {
			return err
		}
	}

	if window.Cursor != "" {
		r.stream.cursor = window.Cursor
	}

	return nil
}

// send hands one item to the loop, or gives up when ctx ends.
func send(ctx context.Context, events chan<- news, item news) error {
	select {
	case events <- item:
		return nil
	case <-ctx.Done():
		return ctx.Err() //nolint:wrapcheck // the caller's own context.
	}
}

// sentBy reports whether a print seen at when can be the one the queue sent:
// the queue sent its file no later than that. A print of a file with the same
// name that began before the send is somebody else's.
func sentBy(q *queue.Queue, when time.Time) bool {
	active := q.Active()

	return active != nil && !when.Before(active.Changed.Add(-startSkew))
}

// jobStarted records a print the printer opened, when it is the queue's.
func (r *Runner) jobStarted(ctx context.Context, device string, started event.JobStarted) {
	r.update(ctx, device, func(q *queue.Queue) bool {
		if !sentBy(q, started.Started) {
			return false
		}

		// A print started by the server's command reports the path the
		// command named. One started on the printer's screen reports the FAT
		// short name, such as /usb/BRACKE~1.BGC, and the name the queue gave
		// the file only as its display name.
		return q.Started(started.JobID, started.Path, started.Started) ||
			q.Started(started.JobID, started.DisplayName, started.Started)
	})
}

// jobSettled records the end of a print, when it is the queue's.
func (r *Runner) jobSettled(ctx context.Context, device string, settled event.JobSettled) {
	keep := r.settings().KeepHistory

	r.update(ctx, device, func(q *queue.Queue) bool {
		// The print is the queue's when it is the job the queue already
		// follows, or one that began after the send. Its end says nothing:
		// the end of an earlier print of the same name can arrive after the
		// next send.
		active := q.Active()
		known := active != nil && active.JobID != "" && active.JobID == settled.JobID
		if !known && !sentBy(q, settled.Started) {
			return false
		}

		outcome := outcomeOf(settled.Outcome)

		return q.Settled(settled.JobID, settled.Path, outcome, settled.Ended, keep) ||
			q.Settled(settled.JobID, settled.DisplayName, outcome, settled.Ended, keep)
	})
}

// transferSettled records the end of a copy to the printer: of a file the
// queue follows on its way, and of the file the queue is sending.
func (r *Runner) transferSettled(ctx context.Context, device string, settled event.TransferSettled) {
	landing := r.copies[settled.TransferID]
	delete(r.copies, settled.TransferID)
	r.copyEnded(ctx, device, landing, settled)

	if settled.State != event.TransferDone {
		r.transferEnded(ctx, device, landing, settled)
	}
}

// filesChanged has the printer's files the server knew nothing about asked
// about again once a read of its files landed, which may name them.
func (r *Runner) filesChanged(device string, data json.RawMessage) {
	var changed struct {
		Reading bool `json:"reading"`
	}
	if json.Unmarshal(data, &changed) == nil && !changed.Reading {
		r.forgetUnheard(device)
	}
}

// apply moves a printer's queue by one event. An event about a printer that
// has no queue, or about a print the queue did not send, changes nothing.
func (r *Runner) apply(ctx context.Context, item news) {
	if item.resync {
		r.unheard.Clear()
		r.Sweep(ctx)

		return
	}

	envelope := item.envelope

	switch envelope.Type {
	case event.TypeDeviceStateChanged:
		// A printer that came to rest may take its next file, and one that
		// stopped being free holds it with the reason, so its queue is looked
		// at now rather than on the next sweep.
		r.sweepOne(ctx, item.device)
	case event.TypeDeviceFilesChanged:
		r.filesChanged(item.device, envelope.Data)
	case event.TypeDeviceJobStarted:
		var started event.JobStarted
		if json.Unmarshal(envelope.Data, &started) != nil {
			return
		}

		r.jobStarted(ctx, item.device, started)
	case event.TypeDeviceJobSettled:
		var settled event.JobSettled
		if json.Unmarshal(envelope.Data, &settled) != nil {
			return
		}

		r.jobSettled(ctx, item.device, settled)
	case event.TypeDeviceTransferSettled:
		var settled event.TransferSettled
		if json.Unmarshal(envelope.Data, &settled) != nil {
			return
		}

		r.transferSettled(ctx, item.device, settled)
	case event.TypeDeviceTransferStarted:
		var started event.TransferStarted
		if json.Unmarshal(envelope.Data, &started) != nil {
			return
		}

		if len(r.copies) >= startsHeld {
			clear(r.copies)
		}

		r.copies[started.TransferID] = started.Destination
	case event.TypeDeviceCommandRequested:
		var requested event.CommandRequested
		if json.Unmarshal(envelope.Data, &requested) != nil || requested.Verb != VerbStartPrint {
			return
		}

		if len(r.starts) >= startsHeld {
			clear(r.starts)
		}

		r.starts[requested.CommandID] = requested.Args[verbPathArgument]
	case event.TypeDeviceCommandSettled:
		var settled event.CommandSettled
		if json.Unmarshal(envelope.Data, &settled) != nil || settled.Verb != VerbStartPrint {
			return
		}

		// A start the queue never saw asked for names no file, which matches
		// no entry that has no command id of its own.
		started := r.starts[settled.CommandID]
		delete(r.starts, settled.CommandID)
		r.startEnded(ctx, item.device, started, settled)
	}
}

// startEnded gives back the entry whose file the printer took and would not
// start. Without it the entry would read as on its way until the sweep gave
// up on it, hours later. A start whose end the host never saw is left to the
// sweep, because the printer may be printing it.
func (r *Runner) startEnded(ctx context.Context, device, started string, settled event.CommandSettled) {
	if settled.OK || settled.OutcomeUnknown {
		return
	}

	detail := settled.MachineReason
	if detail == "" {
		detail = settled.FaultCode
	}

	if detail == "" {
		detail = settled.Status
	}

	now := r.now()

	r.update(ctx, device, func(q *queue.Queue) bool {
		active := q.Active()
		if active == nil || active.State != queue.Sending {
			return false
		}

		// An entry that knows the command it was started with is given back
		// by that command alone. Somebody else's start of a file of the same
		// name ends as refused while the queue's own is printing, and must
		// not put the entry back to be printed again. A file sent from a
		// storage has no command id yet, because the server sends the start
		// after the file has landed, so that one is known by its name.
		if active.CommandID != "" {
			if active.CommandID != settled.CommandID {
				return false
			}
		} else if !queue.SameFile(active.Landing, started) {
			return false
		}

		return q.GiveBack(active.ID, queue.NoteSendFailed, detail, now) == nil
	})
}

// copyEnded tells a file that was queued while it was copied to the printer
// how the copy ended. One that landed is a file on the printer like any
// other. One that did not keeps waiting with the reason, for a person.
//
// landing is where the transfer wrote, and empty for a transfer whose start
// the queue did not hear. Such a copy is found out on the entry's next start,
// which looks for the file.
func (r *Runner) copyEnded(ctx context.Context, device, landing string, settled event.TransferSettled) {
	if landing == "" {
		return
	}

	detail := settled.Reason
	if detail == "" {
		detail = settled.State
	}

	// What a file that landed says about itself is filled in when somebody
	// next reads the queue, as that person: the module alone may not have
	// the printer read a file.
	r.update(ctx, device, func(q *queue.Queue) bool {
		return q.CopyEnded(landing, settled.State == event.TransferDone, detail)
	})

	// A file that landed on a ready printer starts now, not on the next
	// sweep.
	if settled.State == event.TransferDone {
		r.WakePrinter(device)
	}
}

// transferEnded moves the entry whose file did not arrive back to the head of
// the queue: with the reason for a transfer that failed, and with the note
// that a person stopped it for one that was cancelled.
//
// A transfer can end before the queue has written its id on the entry, so an
// entry with no id yet matches by landing, where the transfer writes.
func (r *Runner) transferEnded(ctx context.Context, device, landing string, settled event.TransferSettled) {
	now := r.now()

	r.update(ctx, device, func(q *queue.Queue) bool {
		active := q.Active()
		if active == nil || active.State != queue.Sending {
			return false
		}

		if active.TransferID != settled.TransferID &&
			(active.TransferID != "" || !queue.SameFile(active.Landing, landing)) {
			return false
		}

		// A person stopped the transfer, from the Queue tab or from the list
		// of files on their way. The server sends no start command after a
		// stopped transfer, so the file is safely back at the head of the
		// queue, and it waits there for a person.
		if settled.State == event.TransferCancelled {
			return q.GiveBack(active.ID, queue.NoteTakenBack, "", now) == nil
		}

		detail := settled.Reason
		if detail == "" {
			detail = settled.State
		}

		return q.GiveBack(active.ID, queue.NoteTransferFailed, detail, now) == nil
	})
}

// outcomeOf reads the core's word for how a print ended. A word from a later
// build is an outcome the queue does not know.
func outcomeOf(word string) queue.Outcome {
	switch word {
	case event.JobFinished:
		return queue.Finished
	case event.JobFailed:
		return queue.Failed
	case event.JobCancelled:
		return queue.Cancelled
	}

	return queue.Unknown
}
